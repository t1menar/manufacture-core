package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/t1menar/manufacture-core/internal/bus"
	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/spec"
)

func (e *Engine) onInvalid(d amqp.Delivery, err error) {
	e.log.Warn("invalid message on core events", "routing_key", d.RoutingKey, "user_id", d.UserId, "err", err)
}

// invalid records a message that can never apply (payload does not decode).
func (e *Engine) invalid(m bus.Message, err error) (string, error) {
	e.log.Warn("message payload rejected", "type", m.Type, "message_id", m.MessageID, "producer", m.Producer, "err", err)
	return bus.Invalid, nil
}

// handle applies one message from mf.core.events.
func (e *Engine) handle(ctx context.Context, tx pgx.Tx, m bus.Message) (string, error) {
	module := contract.ModuleOf(m.Producer)
	if module == "" {
		return bus.Ignored, nil
	}
	// Per-module broker users (C5) are named mf-module-<name>; the broker validates
	// user_id, so a module cannot speak for another one. With RequireUserID a
	// message without that user id is refused.
	if (e.o.RequireUserID || strings.HasPrefix(m.UserID, BrokerUserPrefix)) && m.UserID != BrokerUserPrefix+module {
		e.log.Warn("producer does not match the broker user", "producer", m.Producer, "user_id", m.UserID)
		return bus.Invalid, nil
	}
	var out string
	var err error
	switch m.Type {
	case contract.TypeStepHeartbeat, contract.TypeStepWaiting, contract.TypeStepSucceeded, contract.TypeStepFailed:
		out, err = e.handleAttempt(ctx, tx, module, m)
	case contract.TypeRunRequested:
		out, err = e.handleRunRequested(ctx, tx, module, m)
	case contract.TypeModuleOnline, contract.TypeModuleHeartbeat, contract.TypeModuleOffline:
		out, err = e.handleReplica(ctx, tx, module, m)
	default:
		out = bus.Ignored
	}
	if err == nil && m.Attempt != nil && out != bus.Invalid {
		if r, lerr := runOfAttempt(ctx, tx, m.Attempt.ID); lerr == nil && r != nil {
			err = e.checkBudget(ctx, tx, r)
		}
	}
	if out == bus.Stale && err == nil {
		e.o.Metrics.StaleResult(module, string(m.Type))
	}
	return out, err
}

// attemptCtx is a locked attempt with its run and step.
type attemptCtx struct {
	run  *runRow
	step *stepRow
	att  *attemptRow
	p    *spec.Pipeline
	st   spec.Step
	pol  policy
}

func (e *Engine) lockAttempt(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*attemptCtx, error) {
	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT run_id FROM step_attempts WHERE id = $1`, id).Scan(&runID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r, err := lockRun(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	a := &attemptRow{ID: id}
	if err := tx.QueryRow(ctx, `SELECT step_row, step_key, module, number, status, request, timeout_seconds, lease_seconds,
			started_at, deadline, not_after, waiting_since, waiting_seq, confirmed_at
		FROM step_attempts WHERE id = $1 FOR UPDATE`, id).Scan(&a.StepRow, &a.StepKey, &a.Module, &a.Number, &a.Status, &a.Request,
		&a.TimeoutSeconds, &a.LeaseSeconds, &a.StartedAt, &a.Deadline, &a.NotAfter, &a.WaitingSince, &a.WaitingSeq, &a.ConfirmedAt); err != nil {
		return nil, err
	}
	a.RunID = runID
	s, err := lockStep(ctx, tx, a.StepRow)
	if err != nil {
		return nil, err
	}
	c := &attemptCtx{run: r, step: s, att: a}
	if c.p, err = e.pipeline(ctx, r.Pipeline, r.Revision); err != nil {
		return nil, err
	}
	if s.StageIndex >= len(c.p.Spec.Stages) {
		return nil, fmt.Errorf("step %s: stage %d out of range", s.StepID, s.StageIndex)
	}
	for _, st := range c.p.Spec.Stages[s.StageIndex].Steps() {
		if st.ID == s.StepID {
			c.st = st
		}
	}
	c.pol = resolvePolicy(c.st, c.p.Spec.Defaults)
	return c, nil
}

func lockStep(ctx context.Context, tx pgx.Tx, id int64) (*stepRow, error) {
	s := &stepRow{ID: id}
	err := tx.QueryRow(ctx, `SELECT run_id, stage, stage_index, step_id, item_index, visit, step_key, module, status, current_attempt_id, output, error
		FROM steps WHERE id = $1 FOR UPDATE`, id).Scan(&s.RunID, &s.Stage, &s.StageIndex, &s.StepID, &s.ItemIndex, &s.Visit, &s.StepKey,
		&s.Module, &s.Status, &s.Current, &s.Output, &s.Error)
	return s, err
}

// live reports whether results of the attempt still count.
func (c *attemptCtx) live() bool {
	return !isTerminalRun(c.run.Status) && c.step.Current != nil && *c.step.Current == c.att.ID &&
		!isTerminalStep(c.step.Status) && isLiveAttempt(c.att.Status) && c.att.Status != AttemptScheduled
}

func (e *Engine) handleAttempt(ctx context.Context, tx pgx.Tx, module string, m bus.Message) (string, error) {
	if m.Attempt == nil {
		return bus.Invalid, nil
	}
	c, err := e.lockAttempt(ctx, tx, m.Attempt.ID)
	if errors.Is(err, ErrNotFound) {
		return bus.Stale, nil
	}
	if err != nil {
		return "", err
	}
	if c.att.Module != module {
		e.log.Warn("result from a module that does not own the attempt", "attempt", c.att.ID, "producer", m.Producer, "owner", c.att.Module)
		return bus.Invalid, nil
	}
	switch m.Type {
	case contract.TypeStepHeartbeat:
		var p contract.StepHeartbeat
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if p.Usage != nil {
			if err := e.recordUsage(ctx, tx, c, *p.Usage); err != nil {
				return "", err
			}
		}
		if !c.live() || isTerminalRun(c.run.Status) {
			return bus.Stale, nil
		}
		return bus.Applied, e.heartbeat(ctx, tx, c, p)
	case contract.TypeStepWaiting:
		var p contract.StepWaiting
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if p.Usage != nil {
			if err := e.recordUsage(ctx, tx, c, *p.Usage); err != nil {
				return "", err
			}
		}
		if !c.live() || p.Seq <= c.att.WaitingSeq {
			return bus.Stale, nil
		}
		return bus.Applied, e.waiting(ctx, tx, c, p)
	case contract.TypeStepSucceeded:
		var p contract.StepSucceeded
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if p.Usage != nil {
			if err := e.recordUsage(ctx, tx, c, *p.Usage); err != nil {
				return "", err
			}
		}
		if !c.live() {
			return bus.Stale, nil
		}
		return bus.Applied, e.succeeded(ctx, tx, c, p)
	default:
		var p contract.StepFailed
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if p.Usage != nil {
			if err := e.recordUsage(ctx, tx, c, *p.Usage); err != nil {
				return "", err
			}
		}
		if !c.live() {
			return bus.Stale, nil
		}
		return bus.Applied, e.failed(ctx, tx, c, p)
	}
}

func (e *Engine) heartbeat(ctx context.Context, tx pgx.Tx, c *attemptCtx, p contract.StepHeartbeat) error {
	a := c.att
	now := time.Now()
	lease := now.Add(time.Duration(a.LeaseSeconds) * time.Second)
	switch a.Status {
	case StepDispatched: // the first heartbeat starts the timeout clock
		deadline := now.Add(time.Duration(a.TimeoutSeconds) * time.Second)
		if a.NotAfter != nil && a.NotAfter.Before(deadline) {
			deadline = *a.NotAfter
		}
		if _, err := tx.Exec(ctx, `UPDATE step_attempts SET status = 'running', started_at = $2, deadline = $3, lease_until = $4,
			progress = $5, replica_id = coalesce($6, replica_id) WHERE id = $1`, a.ID, now, deadline, lease, p.Progress, nullStr(p.ReplicaID)); err != nil {
			return err
		}
		return e.setStep(ctx, tx, c, StepRunning, `started_at = coalesce(started_at, now())`)
	case StepWaiting: // a heartbeat ends the wait; waited time is not counted
		waited := time.Duration(0)
		if a.WaitingSince != nil {
			waited = now.Sub(*a.WaitingSince)
		}
		if _, err := tx.Exec(ctx, `UPDATE step_attempts SET status = 'running', waiting_since = NULL, expected_by = NULL,
			deadline = CASE WHEN not_after IS NULL THEN deadline + $2::interval ELSE least(deadline + $2::interval, not_after) END,
			lease_until = $3, progress = coalesce($4, progress) WHERE id = $1`, a.ID, waited, lease, p.Progress); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE steps SET wait_total = wait_total + $2::interval, waiting = NULL, wait_deadline = NULL WHERE id = $1`,
			c.step.ID, waited); err != nil {
			return err
		}
		if err := e.setStep(ctx, tx, c, StepRunning, ""); err != nil {
			return err
		}
		return e.recomputeWaiting(ctx, tx, c.run)
	default:
		_, err := tx.Exec(ctx, `UPDATE step_attempts SET lease_until = $2, progress = coalesce($3, progress) WHERE id = $1`, a.ID, lease, p.Progress)
		return err
	}
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (e *Engine) setStep(ctx context.Context, tx pgx.Tx, c *attemptCtx, status, extraSet string, extra ...map[string]any) error {
	set := "status = $2"
	if extraSet != "" {
		set += ", " + extraSet
	}
	if isTerminalStep(status) {
		set += ", finished_at = now()"
	}
	if _, err := tx.Exec(ctx, `UPDATE steps SET `+set+` WHERE id = $1`, c.step.ID, status); err != nil {
		return err
	}
	if c.step.Status == status {
		return nil
	}
	c.step.Status = status
	var ex map[string]any
	if len(extra) > 0 {
		ex = extra[0]
	}
	return emitStep(ctx, tx, c.step.ID, status, ex)
}

func (e *Engine) waiting(ctx context.Context, tx pgx.Tx, c *attemptCtx, p contract.StepWaiting) error {
	a := c.att
	now := time.Now()
	since := now
	if a.WaitingSince != nil {
		since = *a.WaitingSince
	}
	if _, err := tx.Exec(ctx, `UPDATE step_attempts SET status = 'waiting', waiting_since = $2, expected_by = $3, waiting_seq = $4,
		started_at = coalesce(started_at, now()), lease_until = NULL WHERE id = $1`, a.ID, since, p.ExpectedBy, p.Seq); err != nil {
		return err
	}
	if a.Status == StepDispatched { // waiting before any heartbeat: the timeout clock starts now, paused
		if _, err := tx.Exec(ctx, `UPDATE step_attempts SET deadline = $2 WHERE id = $1`, a.ID,
			now.Add(time.Duration(a.TimeoutSeconds)*time.Second)); err != nil {
			return err
		}
	}
	waitTimeout := time.Duration(spec.DefaultWaitTimeout)
	if c.st.Wait != nil && c.st.Wait.Timeout > 0 {
		waitTimeout = time.Duration(c.st.Wait.Timeout)
	}
	info := map[string]any{"reason": p.Reason, "message": p.Message, "since": since.UTC()}
	if p.ExpectedBy != nil {
		info["expected_by"] = p.ExpectedBy.UTC()
	}
	// The wait deadline counts total waiting of the step visit.
	if _, err := tx.Exec(ctx, `UPDATE steps SET waiting = $2, wait_deadline = $3::timestamptz + ($4::interval - wait_total) WHERE id = $1`,
		c.step.ID, info, since, waitTimeout); err != nil {
		return err
	}
	if err := e.setStep(ctx, tx, c, StepWaiting, `started_at = coalesce(started_at, now())`, map[string]any{"waiting": info}); err != nil {
		return err
	}
	return e.recomputeWaiting(ctx, tx, c.run)
}

// recomputeWaiting switches a run between running and waiting_human: it is
// waiting_human when every unfinished step of the current stage waits for a human.
func (e *Engine) recomputeWaiting(ctx context.Context, tx pgx.Tx, r *runRow) error {
	if r.Status != RunRunning && r.Status != RunWaitingHuman {
		return nil
	}
	var open, human int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status NOT IN ('succeeded','failed','skipped','cancelled')),
			count(*) FILTER (WHERE status = 'waiting' AND waiting->>'reason' = 'human')
		FROM steps WHERE run_id = $1 AND stage_index = $2 AND visit = (SELECT max(visit) FROM steps WHERE run_id = $1 AND stage_index = $2)`,
		r.ID, r.StageIndex).Scan(&open, &human); err != nil {
		return err
	}
	want := RunRunning
	if open > 0 && open == human {
		want = RunWaitingHuman
	}
	return e.setRunStatus(ctx, tx, r, want, nil)
}

func (e *Engine) finishAttempt(ctx context.Context, tx pgx.Tx, a *attemptRow, status string, errInfo *ErrorInfo) error {
	a.Status = status
	_, err := tx.Exec(ctx, `UPDATE step_attempts SET status = $2, error = $3, finished_at = now(), lease_until = NULL WHERE id = $1`, a.ID, status, errInfo)
	return err
}

func (e *Engine) succeeded(ctx context.Context, tx pgx.Tx, c *attemptCtx, p contract.StepSucceeded) error {
	if err := e.finishAttempt(ctx, tx, c.att, StepSucceeded, nil); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE steps SET output = $2, waiting = NULL, wait_deadline = NULL WHERE id = $1`, c.step.ID, p.Output); err != nil {
		return err
	}
	if c.st.Merge != spec.MergeNone && len(p.Output) > 0 {
		var patch any
		if err := json.Unmarshal(p.Output, &patch); err != nil {
			return err
		}
		if obj, ok := patch.(map[string]any); ok {
			merged, _ := mergePatch(c.run.Data, obj).(map[string]any)
			b, err := json.Marshal(merged)
			if err != nil {
				return err
			}
			if limit := maxBytes(c.p); len(b) > limit {
				errInfo := &ErrorInfo{Class: "fatal", Code: "data_too_large", Message: fmt.Sprintf("run data would be %d bytes, the limit is %d", len(b), limit)}
				if err := e.setStepError(ctx, tx, c, errInfo); err != nil {
					return err
				}
				return e.finishRun(ctx, tx, c.run, RunFailed, errInfo)
			}
			c.run.Data = merged
			if _, err := tx.Exec(ctx, `UPDATE runs SET data = $2, updated_at = now() WHERE id = $1`, c.run.ID, b); err != nil {
				return err
			}
		}
	}
	if err := e.setStep(ctx, tx, c, StepSucceeded, ""); err != nil {
		return err
	}
	if c.run.Status == RunWaitingHuman {
		if err := e.recomputeWaiting(ctx, tx, c.run); err != nil {
			return err
		}
	}
	return e.checkStage(ctx, tx, c.run, c.step.StageIndex, c.step.Visit)
}

func (e *Engine) failed(ctx context.Context, tx pgx.Tx, c *attemptCtx, p contract.StepFailed) error {
	errInfo := &ErrorInfo{Class: string(p.Class), Code: p.Code, Message: p.Message}
	if err := e.finishAttempt(ctx, tx, c.att, StepFailed, errInfo); err != nil {
		return err
	}
	switch p.Class {
	case contract.ErrorTransient:
		delay := c.pol.backoff(c.att.Number)
		if p.RetryAfterSeconds > 0 {
			delay = time.Duration(p.RetryAfterSeconds) * time.Second
		}
		return e.retryOrAttention(ctx, tx, c, spec.ErrorTransient, delay, errInfo)
	case contract.ErrorAttention:
		if err := e.setStepError(ctx, tx, c, errInfo); err != nil {
			return err
		}
		return e.attention(ctx, tx, c.run, CodeStepFailed, fmt.Sprintf("step %s: %s", c.step.StepID, p.Message))
	default:
		return e.failStep(ctx, tx, c, errInfo)
	}
}

func (e *Engine) setStepError(ctx context.Context, tx pgx.Tx, c *attemptCtx, errInfo *ErrorInfo) error {
	if _, err := tx.Exec(ctx, `UPDATE steps SET error = $2, waiting = NULL, wait_deadline = NULL WHERE id = $1`, c.step.ID, errInfo); err != nil {
		return err
	}
	return e.setStep(ctx, tx, c, StepFailed, "", map[string]any{"error": errInfo})
}

// failStep fails the step for good: an optional step lets the stage go on,
// otherwise the run fails.
func (e *Engine) failStep(ctx context.Context, tx pgx.Tx, c *attemptCtx, errInfo *ErrorInfo) error {
	if err := e.setStepError(ctx, tx, c, errInfo); err != nil {
		return err
	}
	if c.st.Optional {
		return e.checkStage(ctx, tx, c.run, c.step.StageIndex, c.step.Visit)
	}
	join := c.p.Spec.Stages[c.step.StageIndex].Join
	if join != nil && join.Mode != "" && join.Mode != spec.JoinAll {
		return e.checkStage(ctx, tx, c.run, c.step.StageIndex, c.step.Visit)
	}
	return e.finishRun(ctx, tx, c.run, RunFailed, &ErrorInfo{Class: errInfo.Class, Code: errInfo.Code,
		Message: fmt.Sprintf("step %s: %s", c.step.StepID, errInfo.Message)})
}

// retryOrAttention schedules the next attempt of the same visit (same step_key)
// when the policy retries the class and attempts are left; otherwise the run
// needs attention.
func (e *Engine) retryOrAttention(ctx context.Context, tx pgx.Tx, c *attemptCtx, class spec.ErrorClass, delay time.Duration, errInfo *ErrorInfo) error {
	if c.att.ConfirmedAt != nil {
		if err := e.setStepError(ctx, tx, c, errInfo); err != nil {
			return err
		}
		return e.attention(ctx, tx, c.run, CodeOutcomeUnknown,
			fmt.Sprintf("step %s: attempt %d confirmed an external action and then %s; check the outcome", c.step.StepID, c.att.Number, errInfo.Message))
	}
	if c.pol.retries(class) && c.att.Number < c.pol.maxAttempts {
		var req contract.StepRequested
		if err := json.Unmarshal(c.att.Request, &req); err != nil {
			return err
		}
		if err := e.createAttempt(ctx, tx, c.run.ID, c.step.ID, c.step.StepKey, c.att.Module, c.att.Number+1, req, time.Now().Add(delay)); err != nil {
			return err
		}
		c.step.Status = "" // force the event
		return e.setStep(ctx, tx, c, StepPending, "error = NULL, waiting = NULL, wait_deadline = NULL",
			map[string]any{"retry_in_seconds": int(delay / time.Second), "error": errInfo})
	}
	if err := e.setStepError(ctx, tx, c, errInfo); err != nil {
		return err
	}
	return e.attention(ctx, tx, c.run, CodeRetriesExhausted, fmt.Sprintf("step %s failed after %d attempt(s): %s", c.step.StepID, c.att.Number, errInfo.Message))
}

// checkStage completes a stage visit once its join condition holds.
func (e *Engine) checkStage(ctx context.Context, tx pgx.Tx, r *runRow, idx, visit int) error {
	if r.Status != RunRunning && r.Status != RunWaitingHuman {
		return nil
	}
	if r.StageIndex != idx {
		return nil
	}
	p, err := e.pipeline(ctx, r.Pipeline, r.Revision)
	if err != nil {
		return err
	}
	stage := p.Spec.Stages[idx]
	optional := map[string]bool{}
	for _, st := range stage.Steps() {
		optional[st.ID] = st.Optional
	}
	rows, err := tx.Query(ctx, `SELECT step_id, status FROM steps WHERE run_id = $1 AND stage_index = $2 AND visit = $3`, r.ID, idx, visit)
	if err != nil {
		return err
	}
	var total, done, ok, failedRequired int
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			rows.Close()
			return err
		}
		total++
		if isTerminalStep(status) {
			done++
		}
		if status == StepSucceeded || status == StepSkipped {
			ok++
		}
		if status == StepFailed && !optional[id] {
			failedRequired++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	mode, need := spec.JoinAll, total
	if stage.Join != nil && stage.Join.Mode != "" {
		mode = stage.Join.Mode
	}
	switch mode {
	case spec.JoinAll:
	case spec.JoinAny:
		need = 1
	case spec.JoinQuorum:
		need = stage.Join.Quorum
	}
	switch {
	case mode == spec.JoinAll && done == total:
		if failedRequired > 0 {
			return e.finishRun(ctx, tx, r, RunFailed, &ErrorInfo{Class: "fatal", Code: CodeStepFailed, Message: fmt.Sprintf("stage %s failed", stage.Name)})
		}
	case mode != spec.JoinAll && ok >= need:
		if err := e.cancelLive(ctx, tx, r, idx, contract.CancelSuperseded, false); err != nil {
			return err
		}
	case mode != spec.JoinAll && ok+(total-done) < need:
		return e.finishRun(ctx, tx, r, RunFailed, &ErrorInfo{Class: "fatal", Code: CodeStepFailed,
			Message: fmt.Sprintf("stage %s: %d of %d steps succeeded, %d needed", stage.Name, ok, total, need)})
	default:
		return nil
	}
	next, done2, err := e.route(ctx, tx, r, p, idx, visit)
	if err != nil || done2 {
		return err
	}
	return e.startStage(ctx, tx, r, next)
}

// route snapshots run data after a stage visit and picks the next stage.
// done is true when the run left the running state.
func (e *Engine) route(ctx context.Context, tx pgx.Tx, r *runRow, p *spec.Pipeline, idx, visit int) (int, bool, error) {
	body, err := json.Marshal(r.Data)
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO context_snapshots (run_id, after_stage, visit, sha256, size, body)
		VALUES ($1, $2, $3, encode(sha256(convert_to($4::text, 'UTF8')), 'hex'), $5, $4::jsonb) ON CONFLICT DO NOTHING`, r.ID, idx, visit, string(body), len(body)); err != nil {
		return 0, false, err
	}
	stage := p.Spec.Stages[idx]
	if len(stage.Routes) == 0 {
		return idx + 1, false, nil
	}
	vars, err := e.scope(ctx, tx, r, visit)
	if err != nil {
		return 0, false, err
	}
	for _, rt := range stage.Routes {
		ok, err := evalRoute(rt.When, vars)
		if err != nil {
			return 0, true, e.attention(ctx, tx, r, CodeInvalidPipeline, fmt.Sprintf("stage %s: route to %s: %v", stage.Name, rt.Goto, err))
		}
		if !ok {
			continue
		}
		target := -1
		for i, s := range p.Spec.Stages {
			if s.Name == rt.Goto {
				target = i
			}
		}
		if target < 0 {
			return 0, true, e.attention(ctx, tx, r, CodeInvalidPipeline, fmt.Sprintf("stage %s: unknown route target %s", stage.Name, rt.Goto))
		}
		if rt.MaxVisits > 0 {
			var visits int
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(visit), 0) FROM steps WHERE run_id = $1 AND stage_index = $2`, r.ID, target).Scan(&visits); err != nil {
				return 0, false, err
			}
			if visits >= rt.MaxVisits {
				return 0, true, e.attention(ctx, tx, r, CodeMaxVisitsExceeded,
					fmt.Sprintf("stage %s was visited %d time(s); max_visits is %d", rt.Goto, visits, rt.MaxVisits))
			}
		}
		return target, false, nil
	}
	return idx + 1, false, nil
}

// runOfAttempt reloads the (already locked) run of an attempt.
func runOfAttempt(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*runRow, error) {
	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT run_id FROM step_attempts WHERE id = $1`, id).Scan(&runID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return lockRun(ctx, tx, runID)
}
