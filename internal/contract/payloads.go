package contract

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StepRequested asks a module to run a step attempt.
type StepRequested struct {
	Pipeline     string          `json:"pipeline"`
	Revision     int64           `json:"revision"`
	Stage        string          `json:"stage"`
	Step         string          `json:"step"`
	ItemIndex    *int            `json:"item_index,omitempty"`
	Input        json.RawMessage `json:"input"`
	Config       json.RawMessage `json:"config,omitempty"`
	Deadline     time.Time       `json:"deadline"`
	LeaseSeconds int             `json:"lease_seconds"`
	BlobPrefix   string          `json:"blob_prefix"`
}

// CancelReason says why an attempt is cancelled.
type CancelReason string

// Cancel reasons.
const (
	CancelTimeout    CancelReason = "timeout"
	CancelLost       CancelReason = "lost"
	CancelCancelled  CancelReason = "cancelled"
	CancelSuperseded CancelReason = "superseded"
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

// StepHeartbeat extends the lease of an attempt.
type StepHeartbeat struct {
	Progress  *float64 `json:"progress,omitempty"`
	Message   string   `json:"message,omitempty"`
	ReplicaID string   `json:"replica_id,omitempty"`
}

// StepSucceeded reports the output of an attempt.
type StepSucceeded struct {
	Output     json.RawMessage `json:"output"`
	ReplicaID  string          `json:"replica_id,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
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
	Class             ErrorClass `json:"class"`
	Code              string     `json:"code,omitempty"`
	Message           string     `json:"message"`
	RetryAfterSeconds int        `json:"retry_after_seconds,omitempty"`
	ReplicaID         string     `json:"replica_id,omitempty"`
	DurationMS        int64      `json:"duration_ms,omitempty"`
}

// RunRequested asks the core to start a run.
type RunRequested struct {
	Pipeline  string          `json:"pipeline"`
	DedupKey  string          `json:"dedup_key,omitempty"`
	TriggerID *uuid.UUID      `json:"trigger_id,omitempty"`
	Data      json.RawMessage `json:"data"`
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
