package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/internal/contract"
)

// Operator is who changes a run and why (audit).
type Operator struct {
	Actor   string
	Comment string
}

func (e *Engine) audit(ctx context.Context, op Operator, action string, id uuid.UUID, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	details["comment"] = op.Comment
	if err := e.o.Store.Audit(ctx, op.Actor, action, "run/"+id.String(), details); err != nil {
		e.log.Warn("audit", "action", action, "err", err)
	}
}

func lockRunFor(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*runRow, error) {
	r, err := lockRun(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// CancelRun cancels a queued, running or parked run. Attempts that confirmed an
// external action are not cancelled: the run goes to needs_attention
// (cancelled_after_confirm) instead.
func (e *Engine) CancelRun(ctx context.Context, id uuid.UUID, op Operator) (RunView, error) {
	err := e.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockRunFor(ctx, tx, id)
		if err != nil {
			return err
		}
		if isTerminalRun(r.Status) {
			return &ConflictError{Code: "run_finished", Message: fmt.Sprintf("the run is already %s", r.Status)}
		}
		var confirmed int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM step_attempts WHERE run_id = $1 AND confirmed_at IS NOT NULL
			AND status IN ('running','waiting')`, id).Scan(&confirmed); err != nil {
			return err
		}
		if confirmed > 0 {
			if err := e.cancelLive(ctx, tx, r, -1, contract.CancelCancelled, true); err != nil {
				return err
			}
			return e.attention(ctx, tx, r, CodeCancelledAfterConfirm,
				fmt.Sprintf("cancel requested, but %d attempt(s) confirmed an external action; check their outcome", confirmed))
		}
		return e.finishRun(ctx, tx, r, RunCancelled, &ErrorInfo{Class: "cancelled", Code: "cancelled", Message: "cancelled by " + op.Actor})
	})
	if err != nil {
		return RunView{}, err
	}
	e.audit(ctx, op, "run.cancel", id, nil)
	return e.GetRun(ctx, id)
}

// ResumeRun retries the failed (or never started) steps of the current stage
// visit of a run in needs_attention: a new attempt of the same visit (same step_key).
func (e *Engine) ResumeRun(ctx context.Context, id uuid.UUID, op Operator, ack bool) (RunView, error) {
	err := e.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockRunFor(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Status != RunNeedsAttention {
			return &ConflictError{Code: "not_needs_attention", Message: fmt.Sprintf("the run is %s, not needs_attention", r.Status)}
		}
		code := ""
		if r.Attention != nil {
			code = r.Attention.Code
		}
		switch code {
		case CodeMaxVisitsExceeded, CodeNotImplemented, CodeInvalidPipeline:
			return &ConflictError{Code: "not_resumable", Message: fmt.Sprintf("a run stopped by %s cannot be resumed; cancel it or retry it as a new run", code)}
		case CodeBudgetExceeded, CodeBudgetInsufficient:
			if b := r.Budget; b != nil && b.RunUSD > 0 && r.SpentUSD >= b.RunUSD {
				return &ConflictError{Code: "budget_exceeded", Message: "raise the run budget first (PATCH /runs/{id}/budget)"}
			}
		case CodeOutcomeUnknown, CodeCancelledAfterConfirm, CodeStepFailed:
			if !ack {
				return &ConflictError{Code: "attention_ack_required", Message: "set acknowledge_attention: true after checking " + code}
			}
		}
		p, err := e.pipeline(ctx, r.Pipeline, r.Revision)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT s.id, s.step_id, s.step_key, s.module, s.visit, a.id, a.number, a.request
			FROM steps s LEFT JOIN step_attempts a ON a.id = s.current_attempt_id
			WHERE s.run_id = $1 AND s.stage_index = $2 AND s.visit = (SELECT max(visit) FROM steps WHERE run_id = $1 AND stage_index = $2)
				AND (s.status IN ('failed','cancelled') OR (s.status = 'pending' AND s.current_attempt_id IS NULL))
			ORDER BY s.id FOR UPDATE OF s`, r.ID, r.StageIndex)
		if err != nil {
			return err
		}
		type redo struct {
			row            int64
			stepID, module string
			key            uuid.UUID
			visit, number  int
			attempt        *uuid.UUID
			request        []byte
		}
		var list []redo
		for rows.Next() {
			var d redo
			var number *int
			if err := rows.Scan(&d.row, &d.stepID, &d.key, &d.module, &d.visit, &d.attempt, &number, &d.request); err != nil {
				rows.Close()
				return err
			}
			if number != nil {
				d.number = *number
			}
			list = append(list, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET attention = NULL WHERE id = $1`, r.ID); err != nil {
			return err
		}
		r.Attention = nil
		if err := e.setRunStatus(ctx, tx, r, RunRunning, map[string]any{"resumed_by": op.Actor}); err != nil {
			return err
		}
		if len(list) == 0 { // nothing failed in this visit: re-evaluate the stage
			var visit int
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(visit), 0) FROM steps WHERE run_id = $1 AND stage_index = $2`, r.ID, r.StageIndex).Scan(&visit); err != nil {
				return err
			}
			if visit == 0 {
				return e.startStage(ctx, tx, r, r.StageIndex)
			}
			return e.checkStage(ctx, tx, r, r.StageIndex, visit)
		}
		stage := p.Spec.Stages[r.StageIndex]
		for _, d := range list {
			var req contract.StepRequested
			if d.request != nil {
				if err := json.Unmarshal(d.request, &req); err != nil {
					return err
				}
			} else { // the step never got an attempt (budget_insufficient): build its request now
				vars, err := e.scope(ctx, tx, r, d.visit)
				if err != nil {
					return err
				}
				for _, st := range stage.Steps() {
					if st.ID == d.stepID {
						if req, err = e.buildRequest(ctx, r, stage, st, d.visit, d.key, resolvePolicy(st, p.Spec.Defaults), vars); err != nil {
							return err
						}
					}
				}
			}
			if err := e.createAttempt(ctx, tx, r.ID, d.row, d.key, d.module, d.number+1, req, time.Now()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE steps SET finished_at = NULL WHERE id = $1`, d.row); err != nil {
				return err
			}
			if err := emitStep(ctx, tx, d.row, StepPending, map[string]any{"message": "resumed by " + op.Actor}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return RunView{}, err
	}
	e.audit(ctx, op, "run.resume", id, map[string]any{"acknowledge_attention": ack})
	return e.GetRun(ctx, id)
}

// RetryRun starts a new run of the same pipeline revision with the initial data.
func (e *Engine) RetryRun(ctx context.Context, id uuid.UUID, op Operator) (RunView, error) {
	var newID uuid.UUID
	err := e.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockRunFor(ctx, tx, id)
		if err != nil {
			return err
		}
		if !isTerminalRun(r.Status) {
			return &ConflictError{Code: "run_active", Message: fmt.Sprintf("the run is %s; cancel it first", r.Status)}
		}
		var data map[string]any
		if err := json.Unmarshal(r.InitialData, &data); err != nil {
			return err
		}
		res, err := e.createRun(ctx, tx, StartRequest{Pipeline: r.Pipeline, Revision: r.Revision, Data: data, Priority: &r.Priority,
			Labels: r.Labels, Trigger: Trigger{Kind: "retry", By: op.Actor}})
		newID = res.RunID
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return RunView{}, err
	}
	if err != nil {
		return RunView{}, err
	}
	e.audit(ctx, op, "run.retry", id, map[string]any{"new_run": newID})
	return e.GetRun(ctx, newID)
}

// SetPriority changes the priority of a queued run.
func (e *Engine) SetPriority(ctx context.Context, id uuid.UUID, priority int, op Operator) (RunView, error) {
	err := e.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockRunFor(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Status != RunQueued {
			return &ConflictError{Code: "not_queued", Message: "only a queued run can change priority"}
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET priority = $2 WHERE id = $1`, id, priority); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE run_queue SET priority = $2 WHERE run_id = $1`, id, priority)
		return err
	})
	if err != nil {
		return RunView{}, err
	}
	e.audit(ctx, op, "run.priority", id, map[string]any{"priority": priority})
	return e.GetRun(ctx, id)
}

// SetBudget changes the budget of an unfinished run.
func (e *Engine) SetBudget(ctx context.Context, id uuid.UUID, runUSD, warnUSD float64, op Operator) (RunView, error) {
	if warnUSD > 0 && warnUSD >= runUSD {
		return RunView{}, &ConflictError{Code: "validation_failed", Message: "warn_usd must be below run_usd"}
	}
	err := e.tx(ctx, func(tx pgx.Tx) error {
		r, err := lockRunFor(ctx, tx, id)
		if err != nil {
			return err
		}
		if isTerminalRun(r.Status) {
			return &ConflictError{Code: "run_finished", Message: fmt.Sprintf("the run is already %s", r.Status)}
		}
		b := &RunBudget{RunUSD: runUSD, WarnUSD: warnUSD}
		_, err = tx.Exec(ctx, `UPDATE runs SET budget = $2, budget_warned_at = CASE WHEN $3::float8 > spent_usd THEN NULL ELSE budget_warned_at END
			WHERE id = $1`, id, b, warnUSD)
		return err
	})
	if err != nil {
		return RunView{}, err
	}
	e.audit(ctx, op, "run.budget", id, map[string]any{"run_usd": runUSD, "warn_usd": warnUSD})
	return e.GetRun(ctx, id)
}

// PipelineBusy refuses deleting a pipeline with unfinished runs.
func PipelineBusy(ctx context.Context, tx pgx.Tx, pipeline string) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE pipeline = $1 AND status NOT IN ('succeeded','failed','cancelled')`, pipeline).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return &ConflictError{Code: "pipeline_busy", Message: fmt.Sprintf("pipeline %s has %d unfinished run(s); cancel them first", pipeline, n)}
	}
	return nil
}
