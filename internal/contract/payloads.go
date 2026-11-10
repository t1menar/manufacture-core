package contract

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StepRequested asks a module to run a step attempt.
type StepRequested struct {
	Pipeline       string          `json:"pipeline"`
	Revision       int64           `json:"revision"`
	Stage          string          `json:"stage"`
	Step           string          `json:"step"`
	StepKey        uuid.UUID       `json:"step_key"`
	Visit          int             `json:"visit"`
	ItemIndex      *int            `json:"item_index,omitempty"`
	Input          json.RawMessage `json:"input"`
	Config         json.RawMessage `json:"config,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	Deadline       *time.Time      `json:"deadline,omitempty"`
	LeaseSeconds   int             `json:"lease_seconds"`
	BlobPrefix     string          `json:"blob_prefix"`
	Budget         *StepBudget     `json:"budget,omitempty"`
	Priority       int             `json:"priority,omitempty"`
	Labels         Labels          `json:"labels,omitempty"`
}

// StepBudget is the run budget as seen when the attempt was dispatched.
type StepBudget struct {
	RunUSD       float64 `json:"run_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
}

// Labels are free-form string labels of a run.
type Labels map[string]string

// CancelReason says why an attempt is cancelled.
type CancelReason string

// Cancel reasons.
const (
	CancelTimeout    CancelReason = "timeout"
	CancelLost       CancelReason = "lost"
	CancelCancelled  CancelReason = "cancelled"
	CancelSuperseded CancelReason = "superseded"
	CancelBudget     CancelReason = "budget"
	CancelOperator   CancelReason = "operator"
)

// StepCancel stops an attempt.
type StepCancel struct {
	Reason CancelReason `json:"reason"`
}

// SourceTrigger asks a source module to emit runs.
type SourceTrigger struct {
	Pipeline  string          `json:"pipeline"`
	TriggerID uuid.UUID       `json:"trigger_id"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// StepHeartbeat extends the lease of an attempt and ends a wait.
type StepHeartbeat struct {
	Progress  *float64 `json:"progress,omitempty"`
	Message   string   `json:"message,omitempty"`
	ReplicaID string   `json:"replica_id,omitempty"`
	Usage     *Usage   `json:"usage,omitempty"`
}

// WaitReason says what a waiting attempt waits for.
type WaitReason string

// Wait reasons.
const (
	WaitHuman    WaitReason = "human"
	WaitExternal WaitReason = "external"
)

// StepWaiting pauses the lease and timeout of an attempt until the next heartbeat or result.
type StepWaiting struct {
	Reason     WaitReason `json:"reason"`
	Message    string     `json:"message,omitempty"`
	Seq        int        `json:"seq"`
	ExpectedBy *time.Time `json:"expected_by,omitempty"`
	ReplicaID  string     `json:"replica_id,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`
}

// Tokens counts the tokens of one model.
type Tokens struct {
	Input      int64 `json:"input,omitempty"`
	Output     int64 `json:"output,omitempty"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
}

// Usage is the cumulative usage of an attempt so far, priced by the module.
type Usage struct {
	CostUSD float64           `json:"cost_usd,omitempty"`
	Tokens  map[string]Tokens `json:"tokens,omitempty"`
}

// Max returns the component-wise maximum of two cumulative reports, so a
// repeated or reordered report never lowers or double-counts usage.
func (u Usage) Max(o Usage) Usage {
	r := Usage{CostUSD: max(u.CostUSD, o.CostUSD)}
	if len(u.Tokens)+len(o.Tokens) == 0 {
		return r
	}
	r.Tokens = make(map[string]Tokens, len(u.Tokens)+len(o.Tokens))
	for _, m := range []map[string]Tokens{u.Tokens, o.Tokens} {
		for k, t := range m {
			c := r.Tokens[k]
			r.Tokens[k] = Tokens{
				Input: max(c.Input, t.Input), Output: max(c.Output, t.Output),
				CacheRead: max(c.CacheRead, t.CacheRead), CacheWrite: max(c.CacheWrite, t.CacheWrite),
			}
		}
	}
	return r
}

// Add sums two usages (attempt totals into a step or run total).
func (u Usage) Add(o Usage) Usage {
	r := Usage{CostUSD: u.CostUSD + o.CostUSD}
	if len(u.Tokens)+len(o.Tokens) == 0 {
		return r
	}
	r.Tokens = make(map[string]Tokens, len(u.Tokens)+len(o.Tokens))
	for _, m := range []map[string]Tokens{u.Tokens, o.Tokens} {
		for k, t := range m {
			c := r.Tokens[k]
			r.Tokens[k] = Tokens{
				Input: c.Input + t.Input, Output: c.Output + t.Output,
				CacheRead: c.CacheRead + t.CacheRead, CacheWrite: c.CacheWrite + t.CacheWrite,
			}
		}
	}
	return r
}

// StepSucceeded reports the output of an attempt.
type StepSucceeded struct {
	Output     json.RawMessage `json:"output"`
	ReplicaID  string          `json:"replica_id,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
}

// ErrorClass classifies a failure.
type ErrorClass string

// Failure classes.
const (
	ErrorTransient ErrorClass = "transient"
	ErrorFatal     ErrorClass = "fatal"
	ErrorAttention ErrorClass = "attention"
)

// StepFailed reports a failed attempt.
type StepFailed struct {
	Class             ErrorClass      `json:"class"`
	Code              string          `json:"code,omitempty"`
	Message           string          `json:"message"`
	RetryAfterSeconds int             `json:"retry_after_seconds,omitempty"`
	ReplicaID         string          `json:"replica_id,omitempty"`
	DurationMS        int64           `json:"duration_ms,omitempty"`
	Usage             *Usage          `json:"usage,omitempty"`
	Recommendation    string          `json:"recommendation,omitempty"`
	Details           json.RawMessage `json:"details,omitempty"`
}

// Reserved step.failed codes.
const (
	CodeModuleShutdown = "module_shutdown"
	CodeBudget         = "budget"
	CodeInvalidBlobRef = "invalid_blob_ref"
)

// RunRequested asks the core to start a run.
type RunRequested struct {
	Pipeline  string          `json:"pipeline"`
	DedupKey  string          `json:"dedup_key,omitempty"`
	TriggerID *uuid.UUID      `json:"trigger_id,omitempty"`
	Data      json.RawMessage `json:"data"`
	Priority  *int            `json:"priority,omitempty"`
	Labels    Labels          `json:"labels,omitempty"`
}

// RunRejected tells a source module (all replicas, control exchange) that a
// run.requested or a source.trigger reply could not start a run.
type RunRejected struct {
	RequestMessageID uuid.UUID `json:"request_message_id"`
	Pipeline         string    `json:"pipeline"`
	DedupKey         string    `json:"dedup_key,omitempty"`
	Code             string    `json:"code"`
	Message          string    `json:"message"`
}

// Trigger says what started a run.
type Trigger struct {
	Kind string `json:"kind"` // manual, source, schedule, api, retry
	By   string `json:"by,omitempty"`
}

// RunStarted notifies subscribed modules.
type RunStarted struct {
	Pipeline  string    `json:"pipeline"`
	Revision  int64     `json:"revision"`
	Labels    Labels    `json:"labels,omitempty"`
	DedupKey  string    `json:"dedup_key,omitempty"`
	Trigger   Trigger   `json:"trigger"`
	Priority  int       `json:"priority,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// RunError is the error that ended a failed run.
type RunError struct {
	Class   string `json:"class,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// RunFinished notifies subscribed modules once per run, on succeeded, failed or cancelled.
type RunFinished struct {
	Pipeline   string          `json:"pipeline"`
	Revision   int64           `json:"revision"`
	Status     string          `json:"status"`
	Labels     Labels          `json:"labels,omitempty"`
	DedupKey   string          `json:"dedup_key,omitempty"`
	Trigger    *Trigger        `json:"trigger,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt time.Time       `json:"finished_at"`
	Error      *RunError       `json:"error,omitempty"`
	Usage      Usage           `json:"usage"`
	Data       json.RawMessage `json:"data,omitempty"`
	DataRef    *BlobRef        `json:"data_ref,omitempty"`
}

// BlobRef is a value stored in object storage instead of inline.
type BlobRef struct {
	URI         string `json:"$blob"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type,omitempty"`
	Name        string `json:"name,omitempty"`
	Keep        bool   `json:"keep,omitempty"`
}

// SDKInfo names the SDK of a module.
type SDKInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HandlerKind is what a module implements.
type HandlerKind string

// Handler kinds.
const (
	HandlerStep   HandlerKind = "step"
	HandlerSource HandlerKind = "source"
)

// ModuleOnline announces a replica.
type ModuleOnline struct {
	Module          string          `json:"module"`
	ReplicaID       string          `json:"replica_id"`
	ContractVersion string          `json:"contract_version"`
	SDK             SDKInfo         `json:"sdk"`
	Version         string          `json:"version,omitempty"`
	Concurrency     int             `json:"concurrency,omitempty"`
	Kinds           []HandlerKind   `json:"kinds,omitempty"`
	InputSchema     json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema    json.RawMessage `json:"output_schema,omitempty"`
	MinBudgetUSD    *float64        `json:"min_budget_usd,omitempty"`
}

// ModuleHeartbeat keeps a replica marked alive.
type ModuleHeartbeat struct {
	Module    string `json:"module"`
	ReplicaID string `json:"replica_id"`
	InFlight  int    `json:"in_flight,omitempty"`
}

// ModuleOffline says a replica is shutting down.
type ModuleOffline struct {
	Module    string `json:"module"`
	ReplicaID string `json:"replica_id"`
}
