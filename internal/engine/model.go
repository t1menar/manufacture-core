package engine

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/t1menar/manufacture-core/internal/contract"
)

// Run statuses.
const (
	RunQueued         = "queued"
	RunRunning        = "running"
	RunWaitingHuman   = "waiting_human"
	RunSucceeded      = "succeeded"
	RunFailed         = "failed"
	RunNeedsAttention = "needs_attention"
	RunCancelled      = "cancelled"
)

// Step and attempt statuses.
const (
	StepPending    = "pending"
	StepDispatched = "dispatched"
	StepRunning    = "running"
	StepWaiting    = "waiting"
	StepSucceeded  = "succeeded"
	StepFailed     = "failed"
	StepSkipped    = "skipped"
	StepCancelled  = "cancelled"

	AttemptScheduled = "scheduled"
	AttemptTimedOut  = "timed_out"
	AttemptLost      = "lost"
)

// Attention codes.
const (
	CodeStepFailed            = "step_failed"
	CodeRetriesExhausted      = "retries_exhausted"
	CodeTimeout               = "timeout"
	CodeBudgetExceeded        = "budget_exceeded"
	CodeBudgetInsufficient    = "budget_insufficient"
	CodeOutcomeUnknown        = "outcome_unknown"
	CodeMaxVisitsExceeded     = "max_visits_exceeded"
	CodeCancelledAfterConfirm = "cancelled_after_confirm"
	CodeNotImplemented        = "not_implemented"
	CodeCancelledStep         = "step_cancelled"
	CodeInvalidPipeline       = "invalid_pipeline"
)

func isTerminalRun(s string) bool {
	return s == RunSucceeded || s == RunFailed || s == RunCancelled
}

func isTerminalStep(s string) bool {
	return s == StepSucceeded || s == StepFailed || s == StepSkipped || s == StepCancelled
}

func isLiveAttempt(s string) bool {
	return s == AttemptScheduled || s == StepDispatched || s == StepRunning || s == StepWaiting
}

// Trigger says what started a run.
type Trigger = contract.Trigger

// Attention says why a run waits for an operator.
type Attention struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorInfo is the error of a step, attempt or run.
type ErrorInfo struct {
	Class   string `json:"class"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// RunBudget is the budget snapshot of a run.
type RunBudget struct {
	RunUSD  float64 `json:"run_usd,omitempty"`
	WarnUSD float64 `json:"warn_usd,omitempty"`
}

// runRow is a run locked for update.
type runRow struct {
	ID          uuid.UUID
	Pipeline    string
	Revision    int
	Status      string
	Priority    int
	Labels      map[string]string
	DedupKey    *string
	Trigger     Trigger
	Data        map[string]any
	StageIndex  int
	Budget      *RunBudget
	SpentUSD    float64
	WarnedAt    *time.Time
	Attention   *Attention
	Finished    int
	CreatedAt   time.Time
	StartedAt   *time.Time
	InitialData json.RawMessage
}

// stepRow is one visit of a step.
type stepRow struct {
	ID         int64
	RunID      uuid.UUID
	Stage      string
	StageIndex int
	StepID     string
	ItemIndex  *int
	Visit      int
	StepKey    uuid.UUID
	Module     string
	Status     string
	Current    *uuid.UUID
	Output     json.RawMessage
	Error      *ErrorInfo
	FinishedAt *time.Time
}

// attemptRow is one attempt.
type attemptRow struct {
	ID             uuid.UUID
	StepRow        int64
	RunID          uuid.UUID
	StepKey        uuid.UUID
	Module         string
	Number         int
	Status         string
	Request        json.RawMessage
	TimeoutSeconds int
	LeaseSeconds   int
	StartedAt      *time.Time
	Deadline       *time.Time
	NotAfter       *time.Time
	WaitingSince   *time.Time
	WaitingSeq     int
	ConfirmedAt    *time.Time
	Error          *ErrorInfo
}
