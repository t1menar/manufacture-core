package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/spec"
)

// cancelLive stops the live attempts of a run (stage < 0: every stage) and
// sends step.cancel to all replicas of their modules. A budget cancel keeps
// confirmed attempts, and so does keepConfirmed.
func (e *Engine) cancelLive(ctx context.Context, tx pgx.Tx, r *runRow, stage int, reason contract.CancelReason, keepConfirmed bool) error {
	q := `SELECT a.id, a.module, a.number, a.status, a.confirmed_at IS NOT NULL, s.id
		FROM step_attempts a JOIN steps s ON s.current_attempt_id = a.id
		WHERE a.run_id = $1 AND a.status IN ('scheduled','dispatched','running','waiting') AND ($2 < 0 OR s.stage_index = $2)
		ORDER BY a.id FOR UPDATE OF a, s`
	rows, err := tx.Query(ctx, q, r.ID, stage)
	if err != nil {
		return err
	}
	type live struct {
		id        uuid.UUID
		module    string
		number    int
		status    string
		confirmed bool
		step      int64
	}
	var list []live
	for rows.Next() {
		var l live
		if err := rows.Scan(&l.id, &l.module, &l.number, &l.status, &l.confirmed, &l.step); err != nil {
			rows.Close()
			return err
		}
		list = append(list, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range list {
		if (keepConfirmed || reason == contract.CancelBudget) && l.confirmed {
			continue
		}
		errInfo := &ErrorInfo{Class: "cancelled", Code: string(reason), Message: "cancelled: " + string(reason)}
		stepStatus := StepCancelled
		if reason == contract.CancelBudget {
			errInfo = &ErrorInfo{Class: "fatal", Code: contract.CodeBudget, Message: "the run budget is exhausted"}
			stepStatus = StepFailed
		}
		if _, err := tx.Exec(ctx, `UPDATE step_attempts SET status = 'cancelled', error = $2, finished_at = now(), lease_until = NULL WHERE id = $1`,
			l.id, errInfo); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE steps SET status = $2, error = $3, waiting = NULL, wait_deadline = NULL, finished_at = now() WHERE id = $1`,
			l.step, stepStatus, errInfo); err != nil {
			return err
		}
		if err := emitStep(ctx, tx, l.step, stepStatus, map[string]any{"error": errInfo}); err != nil {
			return err
		}
		if l.status == AttemptScheduled {
			continue // never sent
		}
		if err := e.sendCancel(ctx, tx, r.ID, l.id, l.number, l.module, reason); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) sendCancel(ctx context.Context, tx pgx.Tx, runID, attempt uuid.UUID, number int, module string, reason contract.CancelReason) error {
	env, err := envelope(contract.ResultMessageID(attempt, contract.TypeStepCancel), contract.TypeStepCancel, &runID,
		&contract.AttemptRef{ID: attempt, Number: number}, contract.StepCancel{Reason: reason})
	if err != nil {
		return err
	}
	return e.send(ctx, tx, e.o.Topo.Control(), contract.ControlKey(module), 0, env)
}

// sweep handles expired leases, timeouts, wait timeouts and lost dispatches.
func (e *Engine) sweep(ctx context.Context) error {
	type cand struct {
		id   uuid.UUID
		kind string
	}
	rows, err := e.o.Pool.Query(ctx, `
		SELECT id, 'lease' FROM step_attempts WHERE status = 'running' AND lease_until < now()
			AND (confirmed_until IS NULL OR confirmed_until < now())
		UNION ALL
		SELECT id, 'timeout' FROM step_attempts WHERE status = 'running' AND deadline < now()
			AND (confirmed_until IS NULL OR confirmed_until < now())
		UNION ALL
		SELECT current_attempt_id, 'wait' FROM steps WHERE status = 'waiting' AND wait_deadline < now() AND current_attempt_id IS NOT NULL
		UNION ALL
		SELECT a.id, 'redispatch' FROM step_attempts a WHERE a.status = 'dispatched' AND a.dispatched_at < $1
			AND EXISTS (SELECT 1 FROM module_replicas m WHERE m.module = a.module AND m.status = 'up')
		LIMIT 500`, time.Now().Add(-e.o.RedispatchAfter))
	if err != nil {
		return err
	}
	var list []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.kind); err != nil {
			rows.Close()
			return err
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	seen := map[uuid.UUID]bool{}
	for _, c := range list {
		if seen[c.id] {
			continue
		}
		seen[c.id] = true
		if err := e.tx(ctx, func(tx pgx.Tx) error { return e.expire(ctx, tx, c.id, c.kind) }); err != nil {
			return fmt.Errorf("attempt %s (%s): %w", c.id, c.kind, err)
		}
	}
	return nil
}

func (e *Engine) expire(ctx context.Context, tx pgx.Tx, id uuid.UUID, kind string) error {
	c, err := e.lockAttempt(ctx, tx, id)
	if err != nil {
		return err
	}
	if !c.live() && (kind != "redispatch" || c.att.Status != StepDispatched) {
		return nil
	}
	var due bool
	if err := tx.QueryRow(ctx, `SELECT CASE $2
			WHEN 'lease' THEN status = 'running' AND lease_until < now() AND (confirmed_until IS NULL OR confirmed_until < now())
			WHEN 'timeout' THEN status = 'running' AND deadline < now() AND (confirmed_until IS NULL OR confirmed_until < now())
			WHEN 'wait' THEN status = 'waiting'
			ELSE status = 'dispatched' AND dispatched_at < $3 END
		FROM step_attempts WHERE id = $1`, id, kind, time.Now().Add(-e.o.RedispatchAfter)).Scan(&due); err != nil || !due {
		return err
	}
	if kind == "wait" {
		var waitDue bool
		if err := tx.QueryRow(ctx, `SELECT wait_deadline < now() FROM steps WHERE id = $1`, c.step.ID).Scan(&waitDue); err != nil || !waitDue {
			return err
		}
	}
	switch kind {
	case "redispatch": // same attempt id, same message id: the relay publishes the outbox row again
		if _, err := tx.Exec(ctx, `UPDATE outbox SET status = 'pending', next_attempt_at = now(), publish_attempts = 0
			WHERE message_id = $1 AND status <> 'pending'`, contract.ResultMessageID(id, contract.TypeStepRequested)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE step_attempts SET dispatched_at = now() WHERE id = $1`, id)
		if err == nil {
			e.log.Info("step.requested re-sent", "attempt", id, "module", c.att.Module)
		}
		return err
	case "lease":
		errInfo := &ErrorInfo{Class: "lost", Code: "lease_expired", Message: "no heartbeat within the lease"}
		if err := e.finishAttempt(ctx, tx, c.att, AttemptLost, errInfo); err != nil {
			return err
		}
		if err := e.sendCancel(ctx, tx, c.run.ID, id, c.att.Number, c.att.Module, contract.CancelLost); err != nil {
			return err
		}
		return e.retryOrAttention(ctx, tx, c, spec.ErrorLost, c.pol.backoff(c.att.Number), errInfo)
	default: // timeout, wait
		msg := "the attempt exceeded its timeout"
		if kind == "wait" {
			msg = "the step waited longer than wait.timeout"
		}
		errInfo := &ErrorInfo{Class: "timeout", Code: "timeout", Message: msg}
		if err := e.finishAttempt(ctx, tx, c.att, AttemptTimedOut, errInfo); err != nil {
			return err
		}
		if err := e.sendCancel(ctx, tx, c.run.ID, id, c.att.Number, c.att.Module, contract.CancelTimeout); err != nil {
			return err
		}
		if kind == "wait" {
			if err := e.endWait(ctx, tx, c); err != nil {
				return err
			}
		}
		switch c.pol.OnTimeout {
		case spec.OnTimeoutFail:
			return e.failStep(ctx, tx, c, errInfo)
		case spec.OnTimeoutAttention:
			if err := e.setStepError(ctx, tx, c, errInfo); err != nil {
				return err
			}
			return e.attention(ctx, tx, c.run, CodeTimeout, fmt.Sprintf("step %s: %s", c.step.StepID, msg))
		default:
			return e.retryOrAttention(ctx, tx, c, spec.ErrorTimeout, 0, errInfo)
		}
	}
}

// endWait adds the open wait to the step total.
func (e *Engine) endWait(ctx context.Context, tx pgx.Tx, c *attemptCtx) error {
	if c.att.WaitingSince == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE steps SET wait_total = wait_total + (now() - $2::timestamptz), waiting = NULL, wait_deadline = NULL WHERE id = $1`,
		c.step.ID, *c.att.WaitingSince)
	if err == nil && c.run.Status == RunWaitingHuman {
		err = e.setRunStatus(ctx, tx, c.run, RunRunning, nil)
	}
	return err
}
