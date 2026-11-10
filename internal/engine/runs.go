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
	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
)

// ErrNotFound is returned for an unknown run, step or pipeline (the store's sentinel).
var ErrNotFound = store.ErrNotFound

// ConflictError is an operator action that does not fit the current state.
type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// ProblemCode is the API problem code.
func (e *ConflictError) ProblemCode() string { return e.Code }

// HTTPStatus is the API status of the error.
func (e *ConflictError) HTTPStatus() int {
	switch e.Code {
	case "validation_failed", "data_too_large":
		return 422
	case "invalid_cursor":
		return 400
	default:
		return 409
	}
}

// StartRequest starts a run.
type StartRequest struct {
	Pipeline string
	Data     map[string]any
	DedupKey string
	Priority *int
	Labels   map[string]string
	Trigger  Trigger
	// Revision pins a pipeline revision (retry); 0 is the current one.
	Revision int
}

// Started is the result of StartRun.
type Started struct {
	RunID    uuid.UUID
	Existing bool // the dedup key matched an existing run
}

// StartRun creates a queued run of the current pipeline revision.
func (e *Engine) StartRun(ctx context.Context, req StartRequest) (Started, error) {
	var out Started
	err := e.tx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = e.createRun(ctx, tx, req)
		return err
	})
	return out, err
}

func (e *Engine) currentPipeline(ctx context.Context, name string) (*spec.Pipeline, int, error) {
	rev, err := e.o.Store.Get(ctx, spec.KindPipeline, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	p, err := e.pipeline(ctx, name, rev.Revision)
	return p, rev.Revision, err
}

func (e *Engine) createRun(ctx context.Context, tx pgx.Tx, req StartRequest) (Started, error) {
	p, revision, err := e.currentPipeline(ctx, req.Pipeline)
	if err != nil {
		return Started{}, err
	}
	if req.Revision > 0 && req.Revision != revision {
		if p, err = e.pipeline(ctx, req.Pipeline, req.Revision); err != nil {
			return Started{}, err
		}
		revision = req.Revision
	}
	if req.Data == nil {
		req.Data = map[string]any{}
	}
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	prio := p.Spec.Priority
	if req.Priority != nil {
		prio = *req.Priority
	}
	var budget *RunBudget
	if b := p.Spec.Budget; b != nil && (b.RunUSD > 0 || b.WarnUSD > 0) {
		budget = &RunBudget{RunUSD: b.RunUSD, WarnUSD: b.WarnUSD}
	}
	var dedup *string
	if req.DedupKey != "" {
		dedup = &req.DedupKey
	}
	data, err := json.Marshal(req.Data)
	if err != nil {
		return Started{}, err
	}
	if limit := maxBytes(p); len(data) > limit {
		return Started{}, &ConflictError{Code: "data_too_large", Message: fmt.Sprintf("run data is %d bytes, the limit is %d", len(data), limit)}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Started{}, err
	}
	var got uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO runs (id, pipeline, revision, status, priority, labels, dedup_key, trigger, data, initial_data, budget)
		VALUES ($1, $2, $3, 'queued', $4, $5, $6, $7, $8, $8, $9)
		ON CONFLICT (pipeline, dedup_key) DO NOTHING RETURNING id`,
		id, req.Pipeline, revision, prio, req.Labels, dedup, req.Trigger, data, budget).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM runs WHERE pipeline = $1 AND dedup_key = $2`, req.Pipeline, req.DedupKey).Scan(&got)
		return Started{RunID: got, Existing: true}, err
	}
	if err != nil {
		return Started{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO run_queue (run_id, pipeline, priority) VALUES ($1, $2, $3)`, id, req.Pipeline, prio); err != nil {
		return Started{}, err
	}
	return Started{RunID: id}, emit(ctx, tx, "run.created", &id, runEvent(&runRow{ID: id, Pipeline: req.Pipeline, Revision: revision, Priority: prio}, RunQueued, nil))
}

func maxBytes(p *spec.Pipeline) int {
	if c := p.Spec.Context; c != nil && c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return spec.DefaultContextMaxBytes
}

func runEvent(r *runRow, status string, extra map[string]any) map[string]any {
	ev := map[string]any{"run_id": r.ID, "pipeline": r.Pipeline, "revision": r.Revision, "status": status, "stage_index": r.StageIndex,
		"priority": r.Priority, "at": time.Now().UTC()}
	for k, v := range extra {
		ev[k] = v
	}
	return ev
}

// lockRun locks and loads a run.
func lockRun(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*runRow, error) {
	r := &runRow{ID: id}
	err := tx.QueryRow(ctx, `SELECT pipeline, revision, status, priority, labels, dedup_key, trigger, data, stage_index,
			budget, spent_usd::float8, budget_warned_at, attention, finished_count, created_at, started_at, initial_data
		FROM runs WHERE id = $1 FOR UPDATE`, id).Scan(&r.Pipeline, &r.Revision, &r.Status, &r.Priority, &r.Labels, &r.DedupKey,
		&r.Trigger, &r.Data, &r.StageIndex, &r.Budget, &r.SpentUSD, &r.WarnedAt, &r.Attention, &r.Finished, &r.CreatedAt,
		&r.StartedAt, &r.InitialData)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (e *Engine) setRunStatus(ctx context.Context, tx pgx.Tx, r *runRow, status string, extra map[string]any) error {
	if r.Status == status {
		return nil
	}
	r.Status = status
	if _, err := tx.Exec(ctx, `UPDATE runs SET status = $2, updated_at = now() WHERE id = $1`, r.ID, status); err != nil {
		return err
	}
	if isTerminalRun(status) {
		return nil // finishRun emits run.finished
	}
	return emit(ctx, tx, "run.updated", &r.ID, runEvent(r, status, extra))
}

// admit moves queued runs to running per pipeline: priority order, max_runs,
// min_interval between starts, and the daily budget.
func (e *Engine) admit(ctx context.Context) error {
	rows, err := e.o.Pool.Query(ctx, `SELECT DISTINCT pipeline FROM run_queue`)
	if err != nil {
		return err
	}
	pipelines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, name := range pipelines {
		if err := e.tx(ctx, func(tx pgx.Tx) error { return e.admitPipeline(ctx, tx, name) }); err != nil {
			return fmt.Errorf("pipeline %s: %w", name, err)
		}
	}
	return nil
}

func (e *Engine) admitPipeline(ctx context.Context, tx pgx.Tx, name string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO pipeline_admission (pipeline) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
		return err
	}
	var last *time.Time
	if err := tx.QueryRow(ctx, `SELECT last_started_at FROM pipeline_admission WHERE pipeline = $1 FOR UPDATE`, name).Scan(&last); err != nil {
		return err
	}
	p, _, err := e.currentPipeline(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return e.rejectQueued(ctx, tx, name)
	}
	if err != nil {
		return err
	}
	conc := p.Spec.Concurrency
	slots := 1 << 30
	countWaiting := false
	var minInterval time.Duration
	if conc != nil {
		if conc.MaxRuns > 0 {
			slots = conc.MaxRuns
		}
		countWaiting = conc.CountWaiting
		minInterval = time.Duration(conc.MinInterval)
	}
	statuses := []string{RunRunning}
	if countWaiting {
		statuses = append(statuses, RunWaitingHuman)
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE pipeline = $1 AND status = ANY($2)`, name, statuses).Scan(&active); err != nil {
		return err
	}
	reason := ""
	free := slots - active
	switch {
	case free <= 0:
		reason = "max_runs"
	case minInterval > 0 && last != nil && time.Since(*last) < minInterval:
		reason = "min_interval"
	}
	if reason == "" {
		if b := p.Spec.Budget; b != nil && b.DayUSD > 0 {
			var day float64
			if err := tx.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0)::float8 FROM usage WHERE pipeline = $1 AND day = (now() AT TIME ZONE 'UTC')::date`,
				name).Scan(&day); err != nil {
				return err
			}
			if day >= b.DayUSD {
				reason = "day_budget"
			}
		}
	}
	if reason != "" {
		_, err := tx.Exec(ctx, `UPDATE runs SET queue_reason = $2 WHERE id IN (SELECT run_id FROM run_queue WHERE pipeline = $1)
			AND queue_reason IS DISTINCT FROM $2`, name, reason)
		return err
	}
	if minInterval > 0 {
		free = 1 // one start per interval
	}
	ids, err := queryIDs(ctx, tx, `SELECT run_id FROM run_queue WHERE pipeline = $1
		ORDER BY priority DESC, enqueued_at LIMIT $2`, name, free)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `DELETE FROM run_queue WHERE run_id = $1`, id); err != nil {
			return err
		}
		r, err := lockRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Status != RunQueued {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET started_at = now(), queue_reason = NULL WHERE id = $1`, id); err != nil {
			return err
		}
		now := time.Now()
		r.StartedAt = &now
		if err := e.setRunStatus(ctx, tx, r, RunRunning, nil); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE pipeline_admission SET last_started_at = now() WHERE pipeline = $1`, name); err != nil {
			return err
		}
		if err := e.notifyStarted(ctx, tx, r); err != nil {
			return err
		}
		if err := e.startStage(ctx, tx, r, 0); err != nil {
			return err
		}
	}
	return nil
}

func queryIDs(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// rejectQueued fails queued runs of a deleted pipeline.
func (e *Engine) rejectQueued(ctx context.Context, tx pgx.Tx, name string) error {
	ids, err := queryIDs(ctx, tx, `DELETE FROM run_queue WHERE pipeline = $1 RETURNING run_id`, name)
	if err != nil {
		return err
	}
	for _, id := range ids {
		r, err := lockRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := e.finishRun(ctx, tx, r, RunFailed, &ErrorInfo{Class: "fatal", Code: "pipeline_deleted", Message: "the pipeline was deleted"}); err != nil {
			return err
		}
	}
	return nil
}

// attention parks the run in needs_attention.
func (e *Engine) attention(ctx context.Context, tx pgx.Tx, r *runRow, code, msg string) error {
	if isTerminalRun(r.Status) {
		return nil
	}
	r.Attention = &Attention{Code: code, Message: msg}
	if _, err := tx.Exec(ctx, `UPDATE runs SET attention = $2 WHERE id = $1`, r.ID, r.Attention); err != nil {
		return err
	}
	if r.Status == RunNeedsAttention {
		return nil
	}
	return e.setRunStatus(ctx, tx, r, RunNeedsAttention, map[string]any{"attention": r.Attention})
}

// finishRun ends a run, cancels its live attempts and notifies subscribers.
func (e *Engine) finishRun(ctx context.Context, tx pgx.Tx, r *runRow, status string, errInfo *ErrorInfo) error {
	if isTerminalRun(r.Status) {
		return nil
	}
	if err := e.cancelLive(ctx, tx, r, -1, contract.CancelCancelled, false); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET finished_at = now(), error = $2, queue_reason = NULL WHERE id = $1`, r.ID, errInfo); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM run_queue WHERE run_id = $1`, r.ID); err != nil {
		return err
	}
	extra := map[string]any{}
	if errInfo != nil {
		extra["error"] = errInfo
	}
	if err := e.setRunStatus(ctx, tx, r, status, extra); err != nil {
		return err
	}
	if err := emit(ctx, tx, "run.finished", &r.ID, runEvent(r, status, extra)); err != nil {
		return err
	}
	var d time.Duration
	if r.StartedAt != nil {
		d = time.Since(*r.StartedAt)
	}
	e.o.Metrics.RunFinished(r.Pipeline, status, d)
	return e.notifyFinished(ctx, tx, r, status, errInfo)
}

// subscribers lists modules subscribed to an event of a pipeline.
func (e *Engine) subscribers(ctx context.Context, ev spec.RunEvent, pipeline string) ([]string, error) {
	revs, err := e.o.Store.List(ctx, spec.KindModule)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rev := range revs {
		m, err := rev.Module()
		if err != nil {
			return nil, err
		}
		for _, s := range m.Spec.Subscribe {
			if s.Wants(ev, pipeline) {
				out = append(out, m.Metadata.Name)
				break
			}
		}
	}
	return out, nil
}

func (e *Engine) notifyStarted(ctx context.Context, tx pgx.Tx, r *runRow) error {
	mods, err := e.subscribers(ctx, spec.RunStarted, r.Pipeline)
	if err != nil || len(mods) == 0 {
		return err
	}
	p := contract.RunStarted{Pipeline: r.Pipeline, Revision: int64(r.Revision), Labels: r.Labels, Trigger: r.Trigger,
		Priority: r.Priority, StartedAt: time.Now().UTC()}
	if r.DedupKey != nil {
		p.DedupKey = *r.DedupKey
	}
	env, err := envelope(contract.RunStartedID(r.ID), contract.TypeRunStarted, &r.ID, nil, p)
	if err != nil {
		return err
	}
	for _, m := range mods {
		if err := e.send(ctx, tx, e.o.Topo.Commands(), contract.NotifyKey(m), 0, env); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) notifyFinished(ctx context.Context, tx pgx.Tx, r *runRow, status string, errInfo *ErrorInfo) error {
	mods, err := e.subscribers(ctx, spec.RunFinished, r.Pipeline)
	if err != nil || len(mods) == 0 {
		return err
	}
	var usage contract.Usage
	if err := e.runUsage(ctx, tx, r.ID, &usage); err != nil {
		return err
	}
	p := contract.RunFinished{Pipeline: r.Pipeline, Revision: int64(r.Revision), Status: status, Labels: r.Labels,
		Trigger: &r.Trigger, StartedAt: r.StartedAt, FinishedAt: time.Now().UTC(), Usage: usage}
	if r.DedupKey != nil {
		p.DedupKey = *r.DedupKey
	}
	if errInfo != nil {
		p.Error = &contract.RunError{Class: errInfo.Class, Code: errInfo.Code, Message: errInfo.Message}
	}
	data, err := json.Marshal(r.Data)
	if err != nil {
		return err
	}
	if len(data) <= 192*1024 {
		p.Data = data
	} else {
		p.Data = json.RawMessage(`{}`) // data_ref (snapshot blob) arrives with C7; the snapshot API has the full data
	}
	env, err := envelope(contract.RunFinishedID(r.ID), contract.TypeRunFinished, &r.ID, nil, p)
	if err != nil {
		return err
	}
	for _, m := range mods {
		if err := e.send(ctx, tx, e.o.Topo.Commands(), contract.NotifyKey(m), 0, env); err != nil {
			return err
		}
	}
	return nil
}
