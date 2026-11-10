package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/internal/bus"
	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/schemas"
)

// Replica liveness.
const (
	ReplicaStaleAfter  = 30 * time.Second // no module.heartbeat for this long → offline (heartbeat every 10 s)
	replicaForgetAfter = time.Hour
)

func (e *Engine) handleRunRequested(ctx context.Context, tx pgx.Tx, module string, m bus.Message) (string, error) {
	var p contract.RunRequested
	if err := m.DecodePayload(&p); err != nil {
		return e.invalid(m, err)
	}
	reject := func(code, msg string) (string, error) {
		e.log.Info("run.requested rejected", "module", module, "pipeline", p.Pipeline, "code", code, "reason", msg)
		env, err := envelope(uuid.NewSHA1(contract.Namespace, []byte(m.MessageID.String()+":run.rejected")), contract.TypeRunRejected, nil, nil,
			contract.RunRejected{RequestMessageID: m.MessageID, Pipeline: p.Pipeline, DedupKey: p.DedupKey, Code: code, Message: msg})
		if err != nil {
			return "", err
		}
		return bus.Ignored, e.send(ctx, tx, e.o.Topo.Control(), contract.ControlKey(module), 0, env)
	}
	pl, _, err := e.currentPipeline(ctx, p.Pipeline)
	if errors.Is(err, ErrNotFound) {
		return reject("unknown_pipeline", fmt.Sprintf("pipeline %q does not exist", p.Pipeline))
	}
	if err != nil {
		return "", err
	}
	if pl.Spec.Trigger == nil || pl.Spec.Trigger.Source != module {
		return reject("forbidden", fmt.Sprintf("module %s is not the trigger source of pipeline %s", module, p.Pipeline))
	}
	var data map[string]any
	if len(p.Data) > 0 {
		if err := json.Unmarshal(p.Data, &data); err != nil {
			return reject("invalid_data", "data must be a JSON object")
		}
	}
	_, err = e.createRun(ctx, tx, StartRequest{Pipeline: p.Pipeline, Data: data, DedupKey: p.DedupKey, Priority: p.Priority, Labels: p.Labels,
		Trigger: Trigger{Kind: "source", By: module}})
	var ce *ConflictError
	if errors.As(err, &ce) {
		return reject("invalid_data", ce.Message)
	}
	if err != nil {
		return "", err
	}
	return bus.Applied, nil
}

// compatible reports whether a module contract version can talk to this core:
// the same major, and for 0.x the same minor.
func compatible(module string) bool {
	core := strings.Split(schemas.Version(), ".")
	mod := strings.Split(module, ".")
	if len(core) < 2 || len(mod) < 2 {
		return false
	}
	if core[0] != mod[0] {
		return false
	}
	return core[0] != "0" || core[1] == mod[1]
}

func (e *Engine) handleReplica(ctx context.Context, tx pgx.Tx, module string, m bus.Message) (string, error) {
	switch m.Type {
	case contract.TypeModuleOnline:
		var p contract.ModuleOnline
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if p.Module != module {
			return bus.Invalid, nil
		}
		status, reason := "up", ""
		if !compatible(p.ContractVersion) {
			status = "unhealthy"
			reason = fmt.Sprintf("contract %s is not compatible with the core contract %s", p.ContractVersion, schemas.Version())
			e.log.Warn("incompatible module replica", "module", module, "replica", p.ReplicaID, "reason", reason)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO module_replicas (module, replica_id, status, reason, contract_version, sdk, version, concurrency, kinds, min_budget_usd)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (module, replica_id) DO UPDATE SET status = EXCLUDED.status, reason = EXCLUDED.reason, contract_version = EXCLUDED.contract_version,
				sdk = EXCLUDED.sdk, version = EXCLUDED.version, concurrency = EXCLUDED.concurrency, kinds = EXCLUDED.kinds,
				min_budget_usd = EXCLUDED.min_budget_usd, online_at = now(), last_heartbeat_at = now()`,
			module, p.ReplicaID, status, nullStr(reason), p.ContractVersion, p.SDK, nullStr(p.Version), p.Concurrency, p.Kinds, p.MinBudgetUSD); err != nil {
			return "", err
		}
	case contract.TypeModuleHeartbeat:
		var p contract.ModuleHeartbeat
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if p.Module != module {
			return bus.Invalid, nil
		}
		tag, err := tx.Exec(ctx, `UPDATE module_replicas SET last_heartbeat_at = now(), in_flight = $3,
				status = CASE WHEN status = 'offline' THEN 'up' ELSE status END
			WHERE module = $1 AND replica_id = $2`, module, p.ReplicaID, p.InFlight)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() == 0 { // the core lost the online (e.g. it was down); the next reconnect re-sends it
			if _, err := tx.Exec(ctx, `INSERT INTO module_replicas (module, replica_id, status, in_flight) VALUES ($1, $2, 'up', $3)
				ON CONFLICT DO NOTHING`, module, p.ReplicaID, p.InFlight); err != nil {
				return "", err
			}
		}
		return bus.Applied, nil // no timeline event per heartbeat
	default:
		var p contract.ModuleOffline
		if err := m.DecodePayload(&p); err != nil {
			return e.invalid(m, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE module_replicas SET status = 'offline', reason = 'module.offline', in_flight = 0
			WHERE module = $1 AND replica_id = $2`, module, p.ReplicaID); err != nil {
			return "", err
		}
	}
	return bus.Applied, emit(ctx, tx, "module.status", nil, map[string]any{"name": module})
}

// sweepReplicas marks silent replicas offline and forgets old offline ones.
func (e *Engine) sweepReplicas(ctx context.Context) error {
	return e.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH u AS (UPDATE module_replicas SET status = 'offline', reason = 'no heartbeat', in_flight = 0
			WHERE status <> 'offline' AND last_heartbeat_at < $1 RETURNING module) SELECT DISTINCT module FROM u`, time.Now().Add(-ReplicaStaleAfter))
		if err != nil {
			return err
		}
		mods, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, m := range mods {
			if err := emit(ctx, tx, "module.status", nil, map[string]any{"name": m}); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM module_replicas WHERE status = 'offline' AND last_heartbeat_at < $1`, time.Now().Add(-replicaForgetAfter))
		return err
	})
}

// TriggerSource asks the trigger source of a pipeline to emit a run (manual
// start without data, or a schedule tick).
func (e *Engine) TriggerSource(ctx context.Context, pipeline string, data json.RawMessage) (uuid.UUID, error) {
	p, _, err := e.currentPipeline(ctx, pipeline)
	if err != nil {
		return uuid.Nil, err
	}
	if p.Spec.Trigger == nil || p.Spec.Trigger.Source == "" {
		return uuid.Nil, &ConflictError{Code: "no_source", Message: fmt.Sprintf("pipeline %s has no trigger.source; pass data to start a run", pipeline)}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	env, err := envelope(id, contract.TypeSourceTrigger, nil, nil, contract.SourceTrigger{Pipeline: pipeline, TriggerID: id, Data: data})
	if err != nil {
		return uuid.Nil, err
	}
	return id, e.tx(ctx, func(tx pgx.Tx) error {
		return e.send(ctx, tx, e.o.Topo.Commands(), contract.WorkKey(p.Spec.Trigger.Source), 0, env)
	})
}

// HasSource reports whether a pipeline has a trigger source module.
func (e *Engine) HasSource(ctx context.Context, pipeline string) (bool, error) {
	p, _, err := e.currentPipeline(ctx, pipeline)
	if err != nil {
		return false, err
	}
	return p.Spec.Trigger != nil && p.Spec.Trigger.Source != "", nil
}

// ReplicaInfo is a registered module replica.
type ReplicaInfo struct {
	Module          string
	ReplicaID       string
	Status          string // up, unhealthy, offline
	Reason          string
	ContractVersion string
	SDK             contract.SDKInfo
	Version         string
	InFlight        int
	OnlineAt        time.Time
	LastHeartbeatAt time.Time
}

// Replicas lists the replicas of a module ("" for all), newest first.
func (e *Engine) Replicas(ctx context.Context, module string) ([]ReplicaInfo, error) {
	rows, err := e.o.Pool.Query(ctx, `SELECT module, replica_id, status, coalesce(reason, ''), coalesce(contract_version, ''), sdk,
			coalesce(version, ''), in_flight, online_at, last_heartbeat_at
		FROM module_replicas WHERE $1 = '' OR module = $1 ORDER BY module, online_at DESC`, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplicaInfo
	for rows.Next() {
		var r ReplicaInfo
		var sdk *contract.SDKInfo
		if err := rows.Scan(&r.Module, &r.ReplicaID, &r.Status, &r.Reason, &r.ContractVersion, &sdk, &r.Version, &r.InFlight,
			&r.OnlineAt, &r.LastHeartbeatAt); err != nil {
			return nil, err
		}
		if sdk != nil {
			r.SDK = *sdk
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
