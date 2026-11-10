package gateway

import (
	"context"
	"time"
)

// LiveMetrics is the rolling 1-minute window of a module.
type LiveMetrics struct {
	Rate       float64 `json:"rate"`
	P50ms      float64 `json:"p50_ms"`
	P95ms      float64 `json:"p95_ms"`
	InFlight   int     `json:"in_flight"`
	QueueDepth int     `json:"queue_depth"`
	ErrorRate  float64 `json:"error_rate"`
}

// Condition is a runtime or registration problem (Kubernetes style).
type Condition struct {
	Type    string     `json:"type"`
	Status  string     `json:"status"`
	Reason  string     `json:"reason,omitempty"`
	Message string     `json:"message,omitempty"`
	Since   *time.Time `json:"since,omitempty"`
}

// Replica is one running instance of a module.
type Replica struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	SDKVersion string     `json:"sdk_version,omitempty"`
	Version    string     `json:"version,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	InFlight   int        `json:"in_flight"`
}

// ModuleLive is what the runtime and the registry know about a module right now.
type ModuleLive struct {
	Driver          string
	ReadyReplicas   int
	Health          string // up, degraded, down, unknown
	Conditions      []Condition
	LastHeartbeatAt *time.Time
	QueueDepth      int
	DLQDepth        int
	InFlight        int
	SDKVersion      string
	ContractVersion string
	Metrics         LiveMetrics
	Replicas        []Replica
	Manifest        map[string]any
}

// RunCounters count runs of a pipeline (or all pipelines).
type RunCounters struct {
	Active         int `json:"active"`
	Queued         int `json:"queued"`
	NeedsAttention int `json:"needs_attention"`
	Succeeded24h   int `json:"succeeded_24h"`
	Failed24h      int `json:"failed_24h"`
}

// Live is implemented by the controller/registry and the engine.
type Live interface {
	Module(ctx context.Context, name string) ModuleLive
	Runs(ctx context.Context, pipeline string) RunCounters // "" for all pipelines
	LastEventID(ctx context.Context) string
}

type noLive struct{}

func (noLive) Module(context.Context, string) ModuleLive {
	return ModuleLive{Health: "unknown", QueueDepth: -1, Metrics: LiveMetrics{QueueDepth: -1}}
}
func (noLive) Runs(context.Context, string) RunCounters { return RunCounters{} }
func (noLive) LastEventID(context.Context) string       { return "0" }
