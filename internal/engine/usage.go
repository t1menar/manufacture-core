package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/internal/contract"
)

// recordUsage stores the cumulative usage of an attempt (component-wise max,
// so repeated or reordered reports never double-count) and refreshes the run total.
func (e *Engine) recordUsage(ctx context.Context, tx pgx.Tx, c *attemptCtx, u contract.Usage) error {
	var prev contract.Usage
	var tokens []byte
	err := tx.QueryRow(ctx, `SELECT cost_usd::float8, tokens FROM usage WHERE attempt_id = $1 FOR UPDATE`, c.att.ID).Scan(&prev.CostUSD, &tokens)
	switch {
	case err == nil:
		if err := json.Unmarshal(tokens, &prev.Tokens); err != nil {
			return err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	cur := prev.Max(u)
	if cur.Tokens == nil {
		cur.Tokens = map[string]contract.Tokens{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO usage (attempt_id, run_id, pipeline, stage, step_id, module, cost_usd, tokens, day)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, (now() AT TIME ZONE 'UTC')::date)
		ON CONFLICT (attempt_id) DO UPDATE SET cost_usd = EXCLUDED.cost_usd, tokens = EXCLUDED.tokens, updated_at = now()`,
		c.att.ID, c.run.ID, c.run.Pipeline, c.step.Stage, c.step.StepID, c.att.Module, cur.CostUSD, cur.Tokens); err != nil {
		return err
	}
	if cur.CostUSD == prev.CostUSD {
		return nil
	}
	return tx.QueryRow(ctx, `UPDATE runs SET spent_usd = (SELECT coalesce(sum(cost_usd), 0) FROM usage WHERE run_id = $1)
		WHERE id = $1 RETURNING spent_usd::float8`, c.run.ID).Scan(&c.run.SpentUSD)
}

// runUsage sums the usage of all attempts of a run.
func (e *Engine) runUsage(ctx context.Context, tx pgx.Tx, runID any, out *contract.Usage) error {
	rows, err := tx.Query(ctx, `SELECT cost_usd::float8, tokens FROM usage WHERE run_id = $1`, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u contract.Usage
		var tokens []byte
		if err := rows.Scan(&u.CostUSD, &tokens); err != nil {
			return err
		}
		if err := json.Unmarshal(tokens, &u.Tokens); err != nil {
			return err
		}
		*out = out.Add(u)
	}
	return rows.Err()
}

// checkBudget applies warn_usd and run_usd after usage changed.
func (e *Engine) checkBudget(ctx context.Context, tx pgx.Tx, r *runRow) error {
	b := r.Budget
	if b == nil || isTerminalRun(r.Status) {
		return nil
	}
	if b.WarnUSD > 0 && r.WarnedAt == nil && r.SpentUSD >= b.WarnUSD {
		now := time.Now()
		r.WarnedAt = &now
		if _, err := tx.Exec(ctx, `UPDATE runs SET budget_warned_at = now() WHERE id = $1`, r.ID); err != nil {
			return err
		}
		// run.budget_warning joins StreamEventType in API 0.5; until then a run.updated carries spent_usd.
		if err := emit(ctx, tx, "run.updated", &r.ID, runEvent(r, r.Status, map[string]any{"spent_usd": r.SpentUSD, "budget_warning": true})); err != nil {
			return err
		}
	}
	if b.RunUSD > 0 && r.SpentUSD >= b.RunUSD && r.Status != RunNeedsAttention {
		if err := e.cancelLive(ctx, tx, r, -1, contract.CancelBudget, false); err != nil {
			return err
		}
		return e.attention(ctx, tx, r, CodeBudgetExceeded, fmt.Sprintf("the run spent $%.4f of its $%.2f budget", r.SpentUSD, b.RunUSD))
	}
	return nil
}
