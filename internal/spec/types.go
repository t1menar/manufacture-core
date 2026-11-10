package spec

import (
	"encoding/json"
	"slices"
	"time"
)

// APIVersion is the only resource API version this core understands.
const APIVersion = "manufacture.dev/v1"

// Kind names a resource type.
type Kind string

// Resource kinds.
const (
	KindModule   Kind = "Module"
	KindPipeline Kind = "Pipeline"
)

// TypeMeta is the header shared by all resources.
type TypeMeta struct {
	APIVersion string `json:"apiVersion"`
	Kind       Kind   `json:"kind"`
}

// ObjectMeta identifies a resource.
type ObjectMeta struct {
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Module is a container image that handles pipeline steps.
type Module struct {
	TypeMeta
	Metadata ObjectMeta `json:"metadata"`
	Spec     ModuleSpec `json:"spec"`
}

// RuntimeMode tells whether the core starts the module or the module runs elsewhere.
type RuntimeMode string

// Runtime modes.
const (
	RuntimeAuto     RuntimeMode = "auto"
	RuntimeExternal RuntimeMode = "external"
)

// ModuleSpec is the desired state of a module.
type ModuleSpec struct {
	Image        string            `json:"image,omitempty"`
	Runtime      RuntimeMode       `json:"runtime,omitempty"`
	Replicas     *Replicas         `json:"replicas,omitempty"`
	Concurrency  int               `json:"concurrency,omitempty"`
	Resources    *Resources        `json:"resources,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Secrets      []SecretEnv       `json:"secrets,omitempty"`
	Config       json.RawMessage   `json:"config,omitempty"`
	MinBudgetUSD *float64          `json:"min_budget_usd,omitempty"`
	Subscribe    []Subscription    `json:"subscribe,omitempty"`
}

// RunEvent names a run lifecycle notification a module may subscribe to.
type RunEvent string

// Run lifecycle notifications.
const (
	RunStarted  RunEvent = "run.started"
	RunFinished RunEvent = "run.finished"
)

// Subscription delivers a run notification to the module queue mf.notify.<name>.
// In YAML it is an event name (all pipelines) or {event, pipelines}.
type Subscription struct {
	Event     RunEvent `json:"event"`
	Pipelines []string `json:"pipelines,omitempty"` // empty: every pipeline
}

// UnmarshalJSON accepts the short form "run.finished" and the object form.
func (s *Subscription) UnmarshalJSON(b []byte) error {
	var ev string
	if json.Unmarshal(b, &ev) == nil {
		*s = Subscription{Event: RunEvent(ev)}
		return nil
	}
	type plain Subscription
	return json.Unmarshal(b, (*plain)(s))
}

// Wants reports whether the subscription covers the event of the pipeline.
func (s Subscription) Wants(ev RunEvent, pipeline string) bool {
	return s.Event == ev && (len(s.Pipelines) == 0 || slices.Contains(s.Pipelines, pipeline))
}

// EffectiveRuntime returns the runtime mode with the default applied.
func (s ModuleSpec) EffectiveRuntime() RuntimeMode {
	if s.Runtime == "" {
		return RuntimeAuto
	}
	return s.Runtime
}

// Resources are the compute requests of one replica, in Kubernetes notation.
type Resources struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// SecretEnv fills an environment variable from a secret.
type SecretEnv struct {
	Env       string    `json:"env"`
	SecretRef SecretRef `json:"secretRef"`
}

// SecretRef points at one key of a named secret.
type SecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Pipeline is an ordered list of stages.
type Pipeline struct {
	TypeMeta
	Metadata ObjectMeta   `json:"metadata"`
	Spec     PipelineSpec `json:"spec"`
}

// PipelineSpec is the definition of a pipeline.
type PipelineSpec struct {
	Trigger     *Trigger     `json:"trigger,omitempty"`
	Concurrency *Concurrency `json:"concurrency,omitempty"`
	Priority    int          `json:"priority,omitempty"`
	Budget      *Budget      `json:"budget,omitempty"`
	Context     *ContextSpec `json:"context,omitempty"`
	Blobs       *BlobsSpec   `json:"blobs,omitempty"`
	Defaults    *StepPolicy  `json:"defaults,omitempty"`
	Stages      []Stage      `json:"stages"`
}

// Trigger says how runs start besides manual starts.
type Trigger struct {
	Source   string `json:"source,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

// Concurrency limits the active runs of a pipeline.
type Concurrency struct {
	MaxRuns      int      `json:"max_runs,omitempty"`
	CountWaiting bool     `json:"count_waiting,omitempty"`
	MinInterval  Duration `json:"min_interval,omitempty"`
}

// Budget limits the cost of runs (usage.cost_usd reported by modules).
type Budget struct {
	RunUSD  float64 `json:"run_usd,omitempty"`
	WarnUSD float64 `json:"warn_usd,omitempty"`
	DayUSD  float64 `json:"day_usd,omitempty"`
}

// ContextSpec describes run data.
type ContextSpec struct {
	Schema   json.RawMessage `json:"schema,omitempty"`
	MaxBytes int             `json:"max_bytes,omitempty"`
}

// BlobsSpec controls large payloads stored by reference.
type BlobsSpec struct {
	TTL Duration `json:"ttl,omitempty"`
}

// StepPolicy holds the reliability settings that pipeline defaults and steps share.
type StepPolicy struct {
	Timeout   Duration  `json:"timeout,omitempty"`
	Lease     Duration  `json:"lease,omitempty"`
	Retry     *Retry    `json:"retry,omitempty"`
	OnTimeout OnTimeout `json:"on_timeout,omitempty"`
}

// OnTimeout is what happens when an attempt exceeds its timeout or wait timeout.
type OnTimeout string

// Timeout actions.
const (
	OnTimeoutRetry     OnTimeout = "retry"
	OnTimeoutFail      OnTimeout = "fail"
	OnTimeoutAttention OnTimeout = "attention"
)

// Step policy defaults (docs/messaging.md §8, Q&A 12–13). Without a retry block a step
// still gets DefaultMaxAttempts attempts for DefaultRetryOn classes.
const (
	DefaultTimeout      = Duration(30 * time.Minute)
	DefaultLeaseMax     = Duration(60 * time.Second) // default lease = min(DefaultLeaseMax, timeout/2), at least MinLease
	MinLease            = Duration(3 * time.Second)
	DefaultMaxAttempts  = 3
	DefaultRetryInitial = Duration(2 * time.Second)
	DefaultRetryMax     = Duration(5 * time.Minute)
)

// DefaultLease is the lease of a step that does not set one.
func DefaultLease(timeout Duration) Duration {
	return max(MinLease, min(DefaultLeaseMax, timeout/2))
}

// Defaults of the 0.3.0 fields.
const (
	DefaultWaitTimeout     = Duration(7 * 24 * time.Hour)
	DefaultContextMaxBytes = 196608
)

// OnCancel is what an operator cancel of a step does to the run.
type OnCancel string

// Cancel actions.
const (
	OnCancelFail      OnCancel = "fail"
	OnCancelAttention OnCancel = "attention"
)

// Wait allows a step to wait for a human or an external system.
type Wait struct {
	Timeout Duration `json:"timeout,omitempty"`
}

// Stage is one position on the conveyor. Exactly one of Step and Parallel is set;
// Foreach requires Step.
type Stage struct {
	Name        string  `json:"name"`
	Step        *Step   `json:"step,omitempty"`
	Parallel    []Step  `json:"parallel,omitempty"`
	Foreach     string  `json:"foreach,omitempty"`
	MaxParallel int     `json:"max_parallel,omitempty"`
	Join        *Join   `json:"join,omitempty"`
	When        string  `json:"when,omitempty"`
	Routes      []Route `json:"routes,omitempty"`
}

// StageKind is the shape of a stage.
type StageKind string

// Stage shapes.
const (
	StageSingle   StageKind = "single"
	StageParallel StageKind = "parallel"
	StageForeach  StageKind = "foreach"
)

// Kind reports the stage shape. It is meaningful only for a validated stage.
func (s Stage) Kind() StageKind {
	switch {
	case len(s.Parallel) > 0:
		return StageParallel
	case s.Foreach != "":
		return StageForeach
	default:
		return StageSingle
	}
}

// Steps returns the steps of the stage in declaration order.
func (s Stage) Steps() []Step {
	if s.Step != nil {
		return []Step{*s.Step}
	}
	return s.Parallel
}

// MergeMode says whether a step output is merged into run data.
type MergeMode string

// Merge modes.
const (
	MergeData MergeMode = "data"
	MergeNone MergeMode = "none"
)

// Step is one module call inside a stage.
type Step struct {
	ID           string            `json:"id,omitempty"`
	Module       string            `json:"module"`
	Input        map[string]string `json:"input,omitempty"`
	Config       json.RawMessage   `json:"config,omitempty"`
	Timeout      Duration          `json:"timeout,omitempty"`
	Lease        Duration          `json:"lease,omitempty"`
	Retry        *Retry            `json:"retry,omitempty"`
	OnTimeout    OnTimeout         `json:"on_timeout,omitempty"`
	OnCancel     OnCancel          `json:"on_cancel,omitempty"`
	Wait         *Wait             `json:"wait,omitempty"`
	MinBudgetUSD *float64          `json:"min_budget_usd,omitempty"`
	When         string            `json:"when,omitempty"`
	Merge        MergeMode         `json:"merge,omitempty"`
	Optional     bool              `json:"optional,omitempty"`
}

// Policy returns the step reliability settings with pipeline defaults applied.
func (s Step) Policy(defaults *StepPolicy) StepPolicy {
	p := StepPolicy{Timeout: s.Timeout, Lease: s.Lease, Retry: s.Retry, OnTimeout: s.OnTimeout}
	if defaults != nil {
		if p.Timeout == 0 {
			p.Timeout = defaults.Timeout
		}
		if p.Lease == 0 {
			p.Lease = defaults.Lease
		}
		if p.Retry == nil {
			p.Retry = defaults.Retry
		}
		if p.OnTimeout == "" {
			p.OnTimeout = defaults.OnTimeout
		}
	}
	p.OnTimeout = defaultOnTimeout(p)
	return p
}

// defaultOnTimeout resolves an unset on_timeout to retry (r2).
func defaultOnTimeout(p StepPolicy) OnTimeout {
	if p.OnTimeout != "" {
		return p.OnTimeout
	}
	return OnTimeoutRetry
}

// Backoff is the delay growth between retries.
type Backoff string

// Backoff kinds.
const (
	BackoffFixed       Backoff = "fixed"
	BackoffExponential Backoff = "exponential"
)

// ErrorClass classifies a step failure for retries.
type ErrorClass string

// Retryable error classes.
const (
	ErrorTransient ErrorClass = "transient"
	ErrorTimeout   ErrorClass = "timeout"
	ErrorLost      ErrorClass = "lost"
)

// Retry configures repeated attempts of a step.
type Retry struct {
	MaxAttempts int          `json:"max_attempts,omitempty"`
	Backoff     Backoff      `json:"backoff,omitempty"`
	Initial     Duration     `json:"initial,omitempty"`
	Max         Duration     `json:"max,omitempty"`
	Jitter      *bool        `json:"jitter,omitempty"`
	OnErrors    []ErrorClass `json:"on_errors,omitempty"`
}

// DefaultRetryOn are the error classes retried when on_errors is not set.
var DefaultRetryOn = []ErrorClass{ErrorTransient, ErrorTimeout, ErrorLost}

// Retries reports whether the policy retries the error class.
func (r *Retry) Retries(c ErrorClass) bool {
	if r == nil {
		return false
	}
	if len(r.OnErrors) == 0 {
		return slices.Contains(DefaultRetryOn, c)
	}
	return slices.Contains(r.OnErrors, c)
}

// JoinMode says when a parallel or foreach stage is complete.
type JoinMode string

// Join modes.
const (
	JoinAll    JoinMode = "all"
	JoinAny    JoinMode = "any"
	JoinQuorum JoinMode = "quorum"
)

// MergeStrategy resolves writes of the same run data key by parallel steps.
type MergeStrategy string

// Merge strategies.
const (
	MergeError       MergeStrategy = "error"
	MergeLastWins    MergeStrategy = "last_wins"
	MergeArrayAppend MergeStrategy = "array_append"
)

// Join configures fan-in.
type Join struct {
	Mode   JoinMode      `json:"mode,omitempty"`
	Quorum int           `json:"quorum,omitempty"`
	Merge  MergeStrategy `json:"merge,omitempty"`
}

// Route moves a run to another stage when its condition holds after the stage completes.
type Route struct {
	When      string `json:"when"`
	Goto      string `json:"goto"`
	MaxVisits int    `json:"max_visits,omitempty"`
}
