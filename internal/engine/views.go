package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/internal/contract"
)

// RunView is the API Run (api/openapi.yaml).
type RunView struct {
	ID            uuid.UUID         `json:"id"`
	Pipeline      string            `json:"pipeline"`
	Revision      int               `json:"revision"`
	Status        string            `json:"status"`
	StageIndex    int               `json:"stage_index"`
	Stage         string            `json:"stage,omitempty"`
	Attention     string            `json:"attention,omitempty"`
	AttentionInfo *Attention        `json:"attention_info,omitempty"`
	Trigger       Trigger           `json:"trigger"`
	Priority      int               `json:"priority"`
	Labels        map[string]string `json:"labels,omitempty"`
	DedupKey      string            `json:"dedup_key,omitempty"`
	QueueReason   string            `json:"queue_reason,omitempty"`
	Budget        *BudgetView       `json:"budget,omitempty"`
	SpentUSD      float64           `json:"spent_usd"`
	Usage         *contract.Usage   `json:"usage,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	StartedAt     *time.Time        `json:"started_at,omitempty"`
	FinishedAt    *time.Time        `json:"finished_at,omitempty"`
	Error         *ErrorView        `json:"error,omitempty"`
}

// BudgetView is the run budget with its state.
type BudgetView struct {
	RunUSD  float64 `json:"run_usd,omitempty"`
	WarnUSD float64 `json:"warn_usd,omitempty"`
	State   string  `json:"state"`
}

// ErrorView is the API ErrorInfo (code always present).
type ErrorView struct {
	Class   string `json:"class"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func errorView(e *ErrorInfo) *ErrorView {
	if e == nil {
		return nil
	}
	return &ErrorView{Class: e.Class, Code: e.Code, Message: e.Message}
}

// AttemptView is the API Attempt.
type AttemptView struct {
	AttemptID      uuid.UUID       `json:"attempt_id"`
	Number         int             `json:"number"`
	Status         string          `json:"status"`
	ReplicaID      string          `json:"replica_id,omitempty"`
	NotBefore      *time.Time      `json:"not_before,omitempty"`
	DispatchedAt   *time.Time      `json:"dispatched_at,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	LeaseUntil     *time.Time      `json:"lease_until,omitempty"`
	Deadline       *time.Time      `json:"deadline,omitempty"`
	ConfirmedAt    *time.Time      `json:"confirmed_at,omitempty"`
	ConfirmedUntil *time.Time      `json:"confirmed_until,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
	Progress       *float64        `json:"progress,omitempty"`
	Usage          *contract.Usage `json:"usage,omitempty"`
	Error          *ErrorView      `json:"error,omitempty"`
}

// StepView is the API Step (one visit).
type StepView struct {
	StepKey          uuid.UUID       `json:"step_key"`
	Stage            string          `json:"stage"`
	StageIndex       int             `json:"stage_index"`
	StepID           string          `json:"step_id"`
	Module           string          `json:"module"`
	ItemIndex        *int            `json:"item_index,omitempty"`
	Status           string          `json:"status"`
	Visit            int             `json:"visit"`
	Visits           int             `json:"visits"`
	Waiting          map[string]any  `json:"waiting,omitempty"`
	WaitTotalSeconds float64         `json:"wait_total_seconds"`
	Usage            *contract.Usage `json:"usage,omitempty"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	Output           json.RawMessage `json:"output,omitempty"`
	Attempts         []AttemptView   `json:"attempts"`
}

// SnapshotInfo lists one context snapshot.
type SnapshotInfo struct {
	AfterStage int       `json:"after_stage"`
	Visit      int       `json:"visit"`
	SHA256     string    `json:"sha256"`
	Size       int       `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
}

// RunDetail is the API RunDetail. Context is the current run data only (the
// conceptual context's run and steps parts are Run and Steps).
type RunDetail struct {
	Run       RunView        `json:"run"`
	Steps     []StepView     `json:"steps"`
	Context   map[string]any `json:"context"`
	Snapshots []SnapshotInfo `json:"snapshots"`
}

// StoredEvent is one timeline entry.
type StoredEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

const runColumns = `r.id, r.pipeline, r.revision, r.status, r.stage_index, r.attention, r.trigger, r.priority, r.labels, r.dedup_key,
	r.queue_reason, r.budget, r.spent_usd::float8, r.budget_warned_at, r.created_at, r.started_at, r.finished_at, r.error, r.data`

func (e *Engine) scanRun(ctx context.Context, row pgx.Row) (RunView, map[string]any, error) {
	var v RunView
	var att *Attention
	var dedup, qr *string
	var budget *RunBudget
	var warned *time.Time
	var errInfo *ErrorInfo
	var data map[string]any
	if err := row.Scan(&v.ID, &v.Pipeline, &v.Revision, &v.Status, &v.StageIndex, &att, &v.Trigger, &v.Priority, &v.Labels, &dedup,
		&qr, &budget, &v.SpentUSD, &warned, &v.CreatedAt, &v.StartedAt, &v.FinishedAt, &errInfo, &data); err != nil {
		return v, nil, err
	}
	if att != nil && v.Status == RunNeedsAttention {
		v.AttentionInfo, v.Attention = att, att.Message
	}
	if dedup != nil {
		v.DedupKey = *dedup
	}
	if qr != nil && v.Status == RunQueued {
		v.QueueReason = *qr
	}
	if budget != nil {
		b := &BudgetView{RunUSD: budget.RunUSD, WarnUSD: budget.WarnUSD, State: "ok"}
		if warned != nil {
			b.State = "warn"
		}
		if budget.RunUSD > 0 && v.SpentUSD >= budget.RunUSD {
			b.State = "exceeded"
		}
		v.Budget = b
	}
	v.Error = errorView(errInfo)
	if p, err := e.pipeline(ctx, v.Pipeline, v.Revision); err == nil && v.StageIndex < len(p.Spec.Stages) {
		v.Stage = p.Spec.Stages[v.StageIndex].Name
	}
	return v, data, nil
}

// GetRun returns a run.
func (e *Engine) GetRun(ctx context.Context, id uuid.UUID) (RunView, error) {
	v, _, err := e.scanRun(ctx, e.o.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs r WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// RunFilter selects runs to list.
type RunFilter struct {
	Pipeline, Status, Q, Cursor string
	Limit                       int
}

// ListRuns lists runs newest first; the cursor is opaque.
func (e *Engine) ListRuns(ctx context.Context, f RunFilter) ([]RunView, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Pipeline != "" {
		add("r.pipeline = $%d", f.Pipeline)
	}
	if f.Status != "" {
		add("r.status = $%d", f.Status)
	}
	if f.Q != "" {
		add("(r.id::text LIKE $%[1]d || '%%' OR r.attention->>'message' ILIKE '%%' || $%[1]d || '%%')", f.Q)
	}
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		parts := strings.SplitN(string(raw), "|", 2)
		if err != nil || len(parts) != 2 {
			return nil, "", &ConflictError{Code: "invalid_cursor", Message: "invalid cursor"}
		}
		at, err1 := time.Parse(time.RFC3339Nano, parts[0])
		id, err2 := uuid.Parse(parts[1])
		if err1 != nil || err2 != nil {
			return nil, "", &ConflictError{Code: "invalid_cursor", Message: "invalid cursor"}
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(r.created_at, r.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)
	rows, err := e.o.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs r WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` ORDER BY r.created_at DESC, r.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []RunView{}
	for rows.Next() {
		v, _, err := e.scanRun(ctx, rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID.String()))
	}
	return out, next, nil
}

// GetRunDetail returns a run with its steps, attempts, data and snapshots.
func (e *Engine) GetRunDetail(ctx context.Context, id uuid.UUID) (RunDetail, error) {
	var d RunDetail
	v, data, err := e.scanRun(ctx, e.o.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs r WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Run, d.Context = v, data
	if d.Context == nil {
		d.Context = map[string]any{}
	}
	usage, err := e.usageByAttempt(ctx, id)
	if err != nil {
		return d, err
	}
	var total contract.Usage
	for _, u := range usage {
		total = total.Add(u)
	}
	d.Run.Usage = &total

	attempts := map[int64][]AttemptView{}
	rows, err := e.o.Pool.Query(ctx, `SELECT step_row, id, number, status, replica_id, not_before, dispatched_at, started_at, lease_until, deadline,
			confirmed_at, confirmed_until, finished_at, progress, error
		FROM step_attempts WHERE run_id = $1 ORDER BY step_row, number`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var row int64
		var a AttemptView
		var replica *string
		var errInfo *ErrorInfo
		if err := rows.Scan(&row, &a.AttemptID, &a.Number, &a.Status, &replica, &a.NotBefore, &a.DispatchedAt, &a.StartedAt, &a.LeaseUntil,
			&a.Deadline, &a.ConfirmedAt, &a.ConfirmedUntil, &a.FinishedAt, &a.Progress, &errInfo); err != nil {
			rows.Close()
			return d, err
		}
		if replica != nil {
			a.ReplicaID = *replica
		}
		a.Error = errorView(errInfo)
		if u, ok := usage[a.AttemptID]; ok {
			a.Usage = &u
		}
		attempts[row] = append(attempts[row], a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	d.Steps = []StepView{}
	rows, err = e.o.Pool.Query(ctx, `SELECT id, step_key, stage, stage_index, step_id, module, item_index, status, visit, waiting, wait_deadline,
			extract(epoch FROM wait_total)::float8, started_at, finished_at, output
		FROM steps WHERE run_id = $1 ORDER BY stage_index, visit, id`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var row int64
		var s StepView
		var waitDeadline *time.Time
		var output []byte
		if err := rows.Scan(&row, &s.StepKey, &s.Stage, &s.StageIndex, &s.StepID, &s.Module, &s.ItemIndex, &s.Status, &s.Visit, &s.Waiting,
			&waitDeadline, &s.WaitTotalSeconds, &s.StartedAt, &s.FinishedAt, &output); err != nil {
			rows.Close()
			return d, err
		}
		s.Visits = s.Visit
		if s.Waiting != nil && waitDeadline != nil {
			s.Waiting["deadline"] = waitDeadline.UTC()
		}
		if len(output) > 0 && output[0] == '{' {
			s.Output = output
		}
		s.Attempts = attempts[row]
		if s.Attempts == nil {
			s.Attempts = []AttemptView{}
		}
		var su contract.Usage
		has := false
		for _, a := range s.Attempts {
			if a.Usage != nil {
				su, has = su.Add(*a.Usage), true
			}
		}
		if has {
			s.Usage = &su
		}
		d.Steps = append(d.Steps, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	d.Snapshots = []SnapshotInfo{}
	rows, err = e.o.Pool.Query(ctx, `SELECT after_stage, visit, sha256, size, created_at FROM context_snapshots WHERE run_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SnapshotInfo
		if err := rows.Scan(&s.AfterStage, &s.Visit, &s.SHA256, &s.Size, &s.CreatedAt); err != nil {
			return d, err
		}
		d.Snapshots = append(d.Snapshots, s)
	}
	return d, rows.Err()
}

func (e *Engine) usageByAttempt(ctx context.Context, runID uuid.UUID) (map[uuid.UUID]contract.Usage, error) {
	rows, err := e.o.Pool.Query(ctx, `SELECT attempt_id, cost_usd::float8, tokens FROM usage WHERE run_id = $1`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]contract.Usage{}
	for rows.Next() {
		var id uuid.UUID
		var u contract.Usage
		var tokens []byte
		if err := rows.Scan(&id, &u.CostUSD, &tokens); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tokens, &u.Tokens); err != nil {
			return nil, err
		}
		if len(u.Tokens) == 0 {
			u.Tokens = nil
		}
		out[id] = u
	}
	return out, rows.Err()
}

// Snapshot returns the run data saved after a stage (visit 0: the last visit).
func (e *Engine) Snapshot(ctx context.Context, id uuid.UUID, afterStage, visit int) (json.RawMessage, error) {
	var body []byte
	err := e.o.Pool.QueryRow(ctx, `SELECT body FROM context_snapshots WHERE run_id = $1 AND after_stage = $2 AND ($3 = 0 OR visit = $3)
		ORDER BY visit DESC LIMIT 1`, id, afterStage, visit).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return body, err
}

// RunEvents returns the run timeline, oldest first.
func (e *Engine) RunEvents(ctx context.Context, id uuid.UUID, cursor string, limit int) ([]StoredEvent, string, error) {
	if _, err := e.GetRun(ctx, id); err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	after := int64(0)
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return nil, "", &ConflictError{Code: "invalid_cursor", Message: "invalid cursor"}
		}
		after = n
	}
	rows, err := e.o.Pool.Query(ctx, `SELECT id, type, at, data FROM events WHERE run_id = $1 AND id > $2 ORDER BY id LIMIT $3`, id, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []StoredEvent{}
	for rows.Next() {
		var ev StoredEvent
		var n int64
		if err := rows.Scan(&n, &ev.Type, &ev.At, &ev.Data); err != nil {
			return nil, "", err
		}
		ev.ID = strconv.FormatInt(n, 10)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, nil
}

// LastEventID is the newest stream event id.
func (e *Engine) LastEventID(ctx context.Context) string {
	var n int64
	_ = e.o.Pool.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM events`).Scan(&n)
	return strconv.FormatInt(n, 10)
}

// RunCounters counts runs of a pipeline ("" for all).
type RunCounters struct {
	Active, Queued, NeedsAttention, Succeeded24h, Failed24h int
}

// Counters counts the runs of a pipeline ("" for all pipelines).
func (e *Engine) Counters(ctx context.Context, pipeline string) RunCounters {
	var c RunCounters
	_ = e.o.Pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE status IN ('running','waiting_human')),
			count(*) FILTER (WHERE status = 'queued'),
			count(*) FILTER (WHERE status = 'needs_attention'),
			count(*) FILTER (WHERE status = 'succeeded' AND finished_at > now() - interval '24 hours'),
			count(*) FILTER (WHERE status = 'failed' AND finished_at > now() - interval '24 hours')
		FROM runs WHERE $1 = '' OR pipeline = $1`, pipeline).Scan(&c.Active, &c.Queued, &c.NeedsAttention, &c.Succeeded24h, &c.Failed24h)
	return c
}
