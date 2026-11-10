package app

import (
	"context"

	"github.com/t1menar/manufacture-core/internal/engine"
	"github.com/t1menar/manufacture-core/internal/gateway"
)

// live answers the gateway's live questions from the engine tables. Queue
// depths and rolling metrics arrive with C8 (-1 = unknown).
type live struct {
	e      *engine.Engine
	driver string
}

func (l live) Module(ctx context.Context, name string) gateway.ModuleLive {
	ml := gateway.ModuleLive{Driver: l.driver, Health: "unknown", QueueDepth: -1, Metrics: gateway.LiveMetrics{QueueDepth: -1}}
	reps, err := l.e.Replicas(ctx, name)
	if err != nil || len(reps) == 0 {
		if err == nil {
			ml.Health = "down"
		}
		return ml
	}
	unhealthy := 0
	for _, r := range reps {
		hb := r.LastHeartbeatAt
		state := map[string]string{"up": "ready", "unhealthy": "unhealthy", "offline": "offline"}[r.Status]
		ml.Replicas = append(ml.Replicas, gateway.Replica{ID: r.ReplicaID, State: state, SDKVersion: r.SDK.Version, Version: r.Version,
			LastSeenAt: &hb, InFlight: r.InFlight})
		if ml.LastHeartbeatAt == nil || hb.After(*ml.LastHeartbeatAt) {
			ml.LastHeartbeatAt = &hb
		}
		switch r.Status {
		case "up":
			ml.ReadyReplicas++
			ml.InFlight += r.InFlight
			if ml.SDKVersion == "" {
				ml.SDKVersion, ml.ContractVersion = r.SDK.Version, r.ContractVersion
			}
		case "unhealthy":
			unhealthy++
			since := r.OnlineAt
			ml.Conditions = append(ml.Conditions, gateway.Condition{Type: "Compatible", Status: "False", Reason: "ContractMismatch",
				Message: r.ReplicaID + ": " + r.Reason, Since: &since})
		}
	}
	ml.Metrics.InFlight = ml.InFlight
	switch {
	case ml.ReadyReplicas > 0 && unhealthy == 0:
		ml.Health = "up"
	case ml.ReadyReplicas > 0:
		ml.Health = "degraded"
	default:
		ml.Health = "down"
	}
	return ml
}

func (l live) Runs(ctx context.Context, pipeline string) gateway.RunCounters {
	c := l.e.Counters(ctx, pipeline)
	return gateway.RunCounters{Active: c.Active, Queued: c.Queued, NeedsAttention: c.NeedsAttention, Succeeded24h: c.Succeeded24h, Failed24h: c.Failed24h}
}

func (l live) LastEventID(ctx context.Context) string { return l.e.LastEventID(ctx) }
