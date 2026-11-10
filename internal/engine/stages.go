package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/spec/expr"
)

// policy is the resolved reliability settings of a step.
type policy struct {
	spec.StepPolicy
	timeout     time.Duration
	lease       time.Duration
	maxAttempts int
}

func resolvePolicy(st spec.Step, defaults *spec.StepPolicy) policy {
	p := policy{StepPolicy: st.Policy(defaults), maxAttempts: spec.DefaultMaxAttempts}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = spec.DefaultTimeout
	}
	lease := p.Lease
	if lease <= 0 {
		lease = spec.DefaultLease(timeout)
	}
	// The wire carries whole seconds: round the timeout up, keep the lease >= MinLease.
	p.timeout = time.Duration(timeout).Round(time.Second)
	if p.timeout < time.Duration(timeout) || p.timeout == 0 {
		p.timeout += time.Second
	}
	p.lease = max(time.Duration(spec.MinLease), time.Duration(lease).Truncate(time.Second))
	if p.Retry != nil && p.Retry.MaxAttempts > 0 {
		p.maxAttempts = p.Retry.MaxAttempts
	}
	return p
}

// retries reports whether the error class is retried (docs/architecture.md §3.4 defaults).
func (p policy) retries(c spec.ErrorClass) bool {
	if p.Retry == nil {
		return slices.Contains(spec.DefaultRetryOn, c)
	}
	return p.Retry.Retries(c)
}

// backoff is the delay before attempt number n+1 after attempt n failed.
func (p policy) backoff(n int) time.Duration {
	d, limit := time.Duration(spec.DefaultRetryInitial), time.Duration(spec.DefaultRetryMax)
	if r := p.Retry; r != nil {
		if r.Initial > 0 {
			d = time.Duration(r.Initial)
		}
		if r.Max > 0 {
			limit = time.Duration(r.Max)
		}
		if r.Backoff == spec.BackoffFixed {
			limit = d
		}
	}
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	d = min(d, limit)
	if p.Retry == nil || p.Retry.Jitter == nil || *p.Retry.Jitter {
		d = d/2 + time.Duration(rand.Int64N(int64(d)/2+1)) //nolint:gosec // jitter, not security
	}
	return d
}

// scope loads the CEL variables of a run.
func (e *Engine) scope(ctx context.Context, tx pgx.Tx, r *runRow, visit int) (expr.Vars, error) {
	rows, err := tx.Query(ctx, `SELECT step_id, visit, status, output FROM steps WHERE run_id = $1 AND item_index IS NULL ORDER BY id`, r.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := map[string]any{}
	for rows.Next() {
		var id, status string
		var v int
		var out []byte
		if err := rows.Scan(&id, &v, &status, &out); err != nil {
			return nil, err
		}
		var output any
		if len(out) > 0 {
			if err := json.Unmarshal(out, &output); err != nil {
				return nil, err
			}
		}
		cur, _ := steps[id].(map[string]any)
		if cur == nil {
			cur = map[string]any{"visits": map[int64]any{}}
			steps[id] = cur
		}
		entry := map[string]any{"status": status, "output": output, "visit": int64(v)}
		cur["visits"].(map[int64]any)[int64(v)] = entry
		cur["status"], cur["output"], cur["visit"] = status, output, int64(v)
		if status == StepSucceeded || status == StepFailed {
			cur["last"] = entry
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return expr.Vars{
		"data":    toAny(r.Data),
		"steps":   steps,
		"run":     map[string]any{"id": r.ID.String(), "pipeline": r.Pipeline, "revision": int64(r.Revision), "labels": toAny(r.Labels), "priority": int64(r.Priority), "trigger": map[string]any{"kind": r.Trigger.Kind, "by": r.Trigger.By}},
		"attempt": int64(0),
		"visit":   int64(visit),
	}, nil
}

func toAny(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// startStage creates the step rows and first attempts of a stage visit.
func (e *Engine) startStage(ctx context.Context, tx pgx.Tx, r *runRow, idx int) error {
	p, err := e.pipeline(ctx, r.Pipeline, r.Revision)
	if err != nil {
		return err
	}
	for ; idx < len(p.Spec.Stages); idx++ {
		stage := p.Spec.Stages[idx]
		var visit int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(visit), 0) + 1 FROM steps WHERE run_id = $1 AND stage_index = $2`, r.ID, idx).Scan(&visit); err != nil {
			return err
		}
		r.StageIndex = idx
		if _, err := tx.Exec(ctx, `UPDATE runs SET stage_index = $2, updated_at = now() WHERE id = $1`, r.ID, idx); err != nil {
			return err
		}
		vars, err := e.scope(ctx, tx, r, visit)
		if err != nil {
			return err
		}
		if stage.Kind() == spec.StageForeach {
			return e.attention(ctx, tx, r, CodeNotImplemented, fmt.Sprintf("stage %s: foreach stages are not supported yet", stage.Name))
		}
		run := true
		if stage.When != "" {
			if run, err = expr.EvalBool(stage.When, expr.ScopeRun, vars); err != nil {
				return e.attention(ctx, tx, r, CodeInvalidPipeline, fmt.Sprintf("stage %s: when: %v", stage.Name, err))
			}
		}
		started := 0
		for _, st := range stage.Steps() {
			ok := run
			if ok && st.When != "" {
				if ok, err = expr.EvalBool(st.When, expr.ScopeRun, vars); err != nil {
					return e.attention(ctx, tx, r, CodeInvalidPipeline, fmt.Sprintf("step %s: when: %v", st.ID, err))
				}
			}
			n, stop, err := e.startStep(ctx, tx, r, p, idx, stage, st, visit, vars, ok)
			if err != nil || stop {
				return err
			}
			started += n
		}
		if started > 0 {
			return nil
		}
		// Every step skipped: the stage is complete at once.
		next, done, err := e.route(ctx, tx, r, p, idx, visit)
		if err != nil || done {
			return err
		}
		if next != idx+1 {
			idx = next - 1
		}
	}
	return e.finishRun(ctx, tx, r, RunSucceeded, nil)
}

// startStep inserts the step row and its first attempt; it returns 1 when an
// attempt was created and stop=true when the run left the running state.
func (e *Engine) startStep(ctx context.Context, tx pgx.Tx, r *runRow, p *spec.Pipeline, idx int, stage spec.Stage, st spec.Step,
	visit int, vars expr.Vars, run bool) (int, bool, error) {
	key := contract.StepKey(r.ID, stage.Name, st.ID, nil, visit)
	status := StepPending
	if !run {
		status = StepSkipped
	}
	var rowID int64
	if err := tx.QueryRow(ctx, `INSERT INTO steps (run_id, stage, stage_index, step_id, visit, step_key, module, status, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CASE WHEN $8 = 'skipped' THEN now() END) RETURNING id`,
		r.ID, stage.Name, idx, st.ID, visit, key, st.Module, status).Scan(&rowID); err != nil {
		return 0, false, err
	}
	if !run {
		return 0, false, emitStep(ctx, tx, rowID, StepSkipped, nil)
	}
	pol := resolvePolicy(st, p.Spec.Defaults)
	req, err := e.buildRequest(ctx, r, stage, st, visit, key, pol, vars)
	if err != nil {
		return 0, true, e.attention(ctx, tx, r, CodeInvalidPipeline, fmt.Sprintf("step %s: %v", st.ID, err))
	}
	if need, err := e.minBudget(ctx, st); err != nil {
		return 0, false, err
	} else if need > 0 && r.Budget != nil && r.Budget.RunUSD > 0 && r.Budget.RunUSD-r.SpentUSD < need {
		return 0, true, e.attention(ctx, tx, r, CodeBudgetInsufficient,
			fmt.Sprintf("step %s needs at least $%.2f, $%.2f of the run budget is left", st.ID, need, r.Budget.RunUSD-r.SpentUSD))
	}
	if err := e.createAttempt(ctx, tx, r.ID, rowID, key, st.Module, 1, req, time.Now()); err != nil {
		return 0, false, err
	}
	return 1, false, nil
}

func (e *Engine) minBudget(ctx context.Context, st spec.Step) (float64, error) {
	if st.MinBudgetUSD != nil {
		return *st.MinBudgetUSD, nil
	}
	m, err := e.module(ctx, st.Module)
	if err != nil || m == nil || m.Spec.MinBudgetUSD == nil {
		return 0, err
	}
	return *m.Spec.MinBudgetUSD, nil
}

// buildRequest builds the immutable step.requested payload of a visit.
func (e *Engine) buildRequest(ctx context.Context, r *runRow, stage spec.Stage, st spec.Step, visit int, key uuid.UUID,
	pol policy, vars expr.Vars) (contract.StepRequested, error) {
	input := vars["data"]
	if len(st.Input) > 0 {
		in := map[string]any{}
		for k, src := range st.Input {
			v, err := expr.Eval(src, expr.ScopeRun, vars)
			if err != nil {
				return contract.StepRequested{}, fmt.Errorf("input %s: %w", k, err)
			}
			in[k] = v
		}
		input = in
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return contract.StepRequested{}, err
	}
	var cfg any
	if m, err := e.module(ctx, st.Module); err != nil {
		return contract.StepRequested{}, err
	} else if m != nil && len(m.Spec.Config) > 0 {
		if err := json.Unmarshal(m.Spec.Config, &cfg); err != nil {
			return contract.StepRequested{}, err
		}
	}
	if len(st.Config) > 0 {
		var patch any
		if err := json.Unmarshal(st.Config, &patch); err != nil {
			return contract.StepRequested{}, err
		}
		cfg = mergePatch(cfg, patch)
	}
	var cfgJSON json.RawMessage
	if cfg != nil {
		if cfgJSON, err = json.Marshal(cfg); err != nil {
			return contract.StepRequested{}, err
		}
	}
	req := contract.StepRequested{
		Pipeline: r.Pipeline, Revision: int64(r.Revision), Stage: stage.Name, Step: st.ID, StepKey: key, Visit: visit,
		Input: inputJSON, Config: cfgJSON, TimeoutSeconds: int(pol.timeout / time.Second), LeaseSeconds: int(pol.lease / time.Second),
		Priority: r.Priority, Labels: r.Labels,
	}
	if b := r.Budget; b != nil && b.RunUSD > 0 {
		req.Budget = &contract.StepBudget{RunUSD: b.RunUSD, SpentUSD: r.SpentUSD, RemainingUSD: max(0, b.RunUSD-r.SpentUSD)}
	}
	return req, nil
}

// createAttempt inserts a scheduled attempt; the dispatcher sends it at notBefore.
func (e *Engine) createAttempt(ctx context.Context, tx pgx.Tx, runID uuid.UUID, stepRow int64, key uuid.UUID, module string, number int,
	req contract.StepRequested, notBefore time.Time) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if req.Budget != nil { // refreshed for every attempt
		var spent float64
		if err := tx.QueryRow(ctx, `SELECT spent_usd::float8 FROM runs WHERE id = $1`, runID).Scan(&spent); err != nil {
			return err
		}
		req.Budget.SpentUSD, req.Budget.RemainingUSD = spent, max(0, req.Budget.RunUSD-spent)
	}
	req.BlobPrefix = fmt.Sprintf("s3://%s/runs/%s/%s/%s/", e.o.BlobBucket, runID, req.Step, id)
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO step_attempts (id, step_row, run_id, step_key, module, number, status, request, timeout_seconds, lease_seconds, not_before)
		VALUES ($1, $2, $3, $4, $5, $6, 'scheduled', $7, $8, $9, $10)`,
		id, stepRow, runID, key, module, number, body, req.TimeoutSeconds, req.LeaseSeconds, notBefore); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE steps SET current_attempt_id = $2, status = 'pending', error = NULL WHERE id = $1`, stepRow, id)
	return err
}

// dispatch sends due scheduled attempts.
func (e *Engine) dispatch(ctx context.Context) error {
	for {
		n := 0
		err := e.tx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT a.id, a.run_id, a.module, a.number, a.request, r.priority
				FROM step_attempts a JOIN runs r ON r.id = a.run_id
				WHERE a.status = 'scheduled' AND a.not_before <= now()
				ORDER BY r.priority DESC, a.not_before LIMIT 100 FOR UPDATE OF a SKIP LOCKED`)
			if err != nil {
				return err
			}
			type due struct {
				id, run  uuid.UUID
				module   string
				number   int
				request  json.RawMessage
				priority int
			}
			var list []due
			for rows.Next() {
				var d due
				if err := rows.Scan(&d.id, &d.run, &d.module, &d.number, &d.request, &d.priority); err != nil {
					rows.Close()
					return err
				}
				list = append(list, d)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, d := range list {
				env, err := envelope(contract.ResultMessageID(d.id, contract.TypeStepRequested), contract.TypeStepRequested,
					&d.run, &contract.AttemptRef{ID: d.id, Number: d.number}, d.request)
				if err != nil {
					return err
				}
				var prio uint8
				if d.priority > 0 {
					prio = 5
				}
				if err := e.send(ctx, tx, e.o.Topo.Commands(), contract.WorkKey(d.module), prio, env); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE step_attempts SET status = 'dispatched', dispatched_at = now() WHERE id = $1`, d.id); err != nil {
					return err
				}
				var row int64
				err = tx.QueryRow(ctx, `UPDATE steps SET status = 'dispatched' WHERE current_attempt_id = $1 AND status = 'pending' RETURNING id`, d.id).Scan(&row)
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				if err := emitStep(ctx, tx, row, StepDispatched, nil); err != nil {
					return err
				}
			}
			n = len(list)
			return nil
		})
		if err != nil || n < 100 {
			return err
		}
	}
}
