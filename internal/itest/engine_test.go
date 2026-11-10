package itest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/t1menar/manufacture-core/internal/bus"
	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/engine"
	"github.com/t1menar/manufacture-core/internal/store"
)

// engineEnv drives the engine without a broker: Tick runs admission, dispatch
// and sweeps; module messages go through Engine.Handle; outgoing messages are
// read from the outbox.
type engineEnv struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	e    *engine.Engine
}

func newEngine(t *testing.T, yaml ...string) *engineEnv {
	t.Helper()
	pool := newDB(t)
	st := store.New(pool)
	ctx := ctxT(t)
	files := []store.File{}
	for i, y := range yaml {
		files = append(files, file("f"+string(rune('a'+i))+".yaml", y))
	}
	if _, err := st.Apply(ctx, store.ApplyRequest{Files: files, Source: store.SourceCLI, Actor: "test"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return &engineEnv{t: t, ctx: ctx, pool: pool, e: engine.New(engine.Options{Pool: pool, Store: st, Log: quietLog()})}
}

func (x *engineEnv) start(data map[string]any) uuid.UUID {
	x.t.Helper()
	res, err := x.e.StartRun(x.ctx, engine.StartRequest{Pipeline: "hello-world", Data: data, Trigger: engine.Trigger{Kind: "manual", By: "test"}})
	if err != nil {
		x.t.Fatalf("start: %v", err)
	}
	x.e.Tick(x.ctx)
	return res.RunID
}

type attempt struct {
	id     uuid.UUID
	number int
	module string
	status string
	key    uuid.UUID
}

// current returns the current attempt of the newest step row of a run.
func (x *engineEnv) current(run uuid.UUID) attempt {
	x.t.Helper()
	var a attempt
	if err := x.pool.QueryRow(x.ctx, `SELECT a.id, a.number, a.module, a.status, a.step_key FROM steps s JOIN step_attempts a ON a.id = s.current_attempt_id
		WHERE s.run_id = $1 ORDER BY s.id DESC LIMIT 1`, run).Scan(&a.id, &a.number, &a.module, &a.status, &a.key); err != nil {
		x.t.Fatalf("current attempt: %v", err)
	}
	return a
}

func (x *engineEnv) send(module string, typ contract.Type, a *attempt, payload any) string {
	x.t.Helper()
	env, err := contract.New(uuid.New(), typ, contract.ModuleProducer(module), time.Now(), payload)
	if err != nil {
		x.t.Fatal(err)
	}
	if a != nil {
		env.Attempt = &contract.AttemptRef{ID: a.id, Number: a.number}
	}
	out, err := x.e.Handle(x.ctx, bus.Message{Envelope: env})
	if err != nil {
		x.t.Fatalf("handle %s: %v", typ, err)
	}
	return out
}

func (x *engineEnv) succeed(a attempt, output map[string]any) string {
	x.t.Helper()
	b, _ := json.Marshal(output)
	x.send(a.module, contract.TypeStepHeartbeat, &a, contract.StepHeartbeat{})
	return x.send(a.module, contract.TypeStepSucceeded, &a, contract.StepSucceeded{Output: b})
}

func (x *engineEnv) run(id uuid.UUID) (status string, data, attention map[string]any) {
	x.t.Helper()
	if err := x.pool.QueryRow(x.ctx, `SELECT status, data, attention FROM runs WHERE id = $1`, id).Scan(&status, &data, &attention); err != nil {
		x.t.Fatal(err)
	}
	return
}

func (x *engineEnv) count(sql string, args ...any) int {
	x.t.Helper()
	var n int
	if err := x.pool.QueryRow(x.ctx, sql, args...).Scan(&n); err != nil {
		x.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (x *engineEnv) exec(sql string, args ...any) {
	x.t.Helper()
	if _, err := x.pool.Exec(x.ctx, sql, args...); err != nil {
		x.t.Fatalf("%s: %v", sql, err)
	}
}

func (x *engineEnv) wantStatus(id uuid.UUID, want string) {
	x.t.Helper()
	if got, _, att := x.run(id); got != want {
		x.t.Fatalf("run status %s (attention %v), want %s", got, att, want)
	}
}

func appendText(data map[string]any, s string) map[string]any {
	t, _ := data["text"].(string)
	return map[string]any{"text": t + s}
}

func TestEngineLinearRun(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	id := x.start(map[string]any{"text": ""})
	data := map[string]any{"text": ""}
	for i, s := range []string{"hello", " world", "!"} {
		a := x.current(id)
		if a.status != "dispatched" {
			t.Fatalf("step %d: attempt status %s", i, a.status)
		}
		if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'step.requested' AND routing_key = $1`, "module."+a.module); n != 1 {
			t.Fatalf("step.requested for %s in outbox: %d", a.module, n)
		}
		data = appendText(data, s)
		if out := x.succeed(a, data); out != bus.Applied {
			t.Fatalf("succeeded: %s", out)
		}
		// A duplicate result of the same attempt changes nothing.
		if out := x.succeed(a, map[string]any{"text": "dup"}); out != bus.Stale {
			t.Fatalf("duplicate result: %s, want stale", out)
		}
		x.e.Tick(x.ctx)
	}
	st, got, _ := x.run(id)
	if st != engine.RunSucceeded || got["text"] != "hello world!" {
		t.Fatalf("run %s %v", st, got)
	}
	if n := x.count(`SELECT count(*) FROM steps WHERE run_id = $1 AND status = 'succeeded'`, id); n != 3 {
		t.Fatalf("succeeded steps: %d", n)
	}
	if n := x.count(`SELECT count(*) FROM events WHERE run_id = $1 AND type = 'run.finished'`, id); n != 1 {
		t.Fatalf("run.finished events: %d", n)
	}
	if n := x.count(`SELECT count(*) FROM context_snapshots WHERE run_id = $1`, id); n != 3 {
		t.Fatalf("snapshots: %d", n)
	}
}

func TestEngineForeignAndStaleResults(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	id := x.start(nil)
	a := x.current(id)
	if out := x.send("hello-world", contract.TypeStepSucceeded, &a, contract.StepSucceeded{Output: json.RawMessage(`{}`)}); out != bus.Invalid {
		t.Fatalf("result from another module: %s", out)
	}
	unknown := attempt{id: uuid.New(), number: 1, module: a.module}
	if out := x.send(a.module, contract.TypeStepSucceeded, &unknown, contract.StepSucceeded{Output: json.RawMessage(`{}`)}); out != bus.Stale {
		t.Fatalf("unknown attempt: %s", out)
	}
	x.wantStatus(id, engine.RunRunning)
}

func TestEngineTransientRetry(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	id := x.start(nil)
	a := x.current(id)
	x.send(a.module, contract.TypeStepHeartbeat, &a, contract.StepHeartbeat{})
	x.send(a.module, contract.TypeStepFailed, &a, contract.StepFailed{Class: contract.ErrorTransient, Message: "flaky"})
	b := x.current(id)
	if b.number != 2 || b.status != engine.AttemptScheduled || b.key != a.key {
		t.Fatalf("retry attempt %+v (first %+v)", b, a)
	}
	x.exec(`UPDATE step_attempts SET not_before = now() WHERE id = $1`, b.id)
	x.e.Tick(x.ctx)
	if b = x.current(id); b.status != "dispatched" {
		t.Fatalf("retry not dispatched: %s", b.status)
	}
	// A late result of the first attempt is stale.
	if out := x.send(a.module, contract.TypeStepSucceeded, &a, contract.StepSucceeded{Output: json.RawMessage(`{}`)}); out != bus.Stale {
		t.Fatalf("late result of the old attempt: %s", out)
	}
	// Exhaust the attempts: hello-hello has retry.max_attempts unset in this pipeline (default 3).
	x.send(b.module, contract.TypeStepFailed, &b, contract.StepFailed{Class: contract.ErrorTransient, Message: "flaky"})
	c := x.current(id)
	x.exec(`UPDATE step_attempts SET not_before = now() WHERE id = $1`, c.id)
	x.e.Tick(x.ctx)
	x.send(c.module, contract.TypeStepFailed, &c, contract.StepFailed{Class: contract.ErrorTransient, Message: "flaky"})
	x.wantStatus(id, engine.RunNeedsAttention)
	if _, _, att := x.run(id); att["code"] != engine.CodeRetriesExhausted {
		t.Fatalf("attention %v", att)
	}
}

func TestEngineFatalFailsRun(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	id := x.start(nil)
	a := x.current(id)
	x.send(a.module, contract.TypeStepFailed, &a, contract.StepFailed{Class: contract.ErrorFatal, Message: "bad input"})
	x.wantStatus(id, engine.RunFailed)
}

func TestEngineTimeoutAndLease(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	id := x.start(nil)
	a := x.current(id)
	x.send(a.module, contract.TypeStepHeartbeat, &a, contract.StepHeartbeat{})
	x.exec(`UPDATE step_attempts SET deadline = now() - interval '1s' WHERE id = $1`, a.id)
	x.e.Tick(x.ctx)
	if n := x.count(`SELECT count(*) FROM step_attempts WHERE id = $1 AND status = 'timed_out'`, a.id); n != 1 {
		t.Fatal("attempt not timed out")
	}
	if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'step.cancel' AND routing_key = $1`, "control."+a.module); n != 1 {
		t.Fatalf("step.cancel sent %d times", n)
	}
	b := x.current(id)
	if b.number != 2 {
		t.Fatalf("on_timeout retry: attempt %d", b.number)
	}
	x.e.Tick(x.ctx)
	b = x.current(id)
	x.send(b.module, contract.TypeStepHeartbeat, &b, contract.StepHeartbeat{})
	x.exec(`UPDATE step_attempts SET lease_until = now() - interval '1s' WHERE id = $1`, b.id)
	x.e.Tick(x.ctx)
	if n := x.count(`SELECT count(*) FROM step_attempts WHERE id = $1 AND status = 'lost'`, b.id); n != 1 {
		t.Fatal("attempt not lost")
	}
	if c := x.current(id); c.number != 3 {
		t.Fatalf("lost → retry: attempt %d", c.number)
	}
	// The timeout clock does not start before the first heartbeat.
	if n := x.count(`SELECT count(*) FROM step_attempts WHERE run_id = $1 AND status = 'scheduled' AND deadline IS NULL`, id); n != 1 {
		t.Fatal("scheduled attempt has a deadline")
	}
}

const reviewPipeline = `
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: reviewer}
spec: {image: localhost:5001/reviewer:1}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: watcher}
spec: {image: localhost:5001/watcher:1, subscribe: [run.started, {event: run.finished, pipelines: [hello-world]}]}
---
apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: hello-world}
spec:
  trigger: {source: hello-init}
  stages:
    - name: review
      step: {module: reviewer, wait: {timeout: 1h}, on_timeout: attention}
`

func TestEngineWaitingAndNotifications(t *testing.T) {
	x := newEngine(t, helloModules, reviewPipeline)
	id := x.start(nil)
	if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'run.started' AND routing_key = 'notify.watcher'`); n != 1 {
		t.Fatalf("run.started to watcher: %d", n)
	}
	a := x.current(id)
	x.send(a.module, contract.TypeStepHeartbeat, &a, contract.StepHeartbeat{})
	if out := x.send(a.module, contract.TypeStepWaiting, &a, contract.StepWaiting{Reason: contract.WaitHuman, Seq: 1}); out != bus.Applied {
		t.Fatalf("waiting: %s", out)
	}
	if out := x.send(a.module, contract.TypeStepWaiting, &a, contract.StepWaiting{Reason: contract.WaitHuman, Seq: 1}); out != bus.Stale {
		t.Fatalf("repeated waiting seq: %s", out)
	}
	x.wantStatus(id, engine.RunWaitingHuman)
	// Leases and timeouts do not tick while waiting.
	x.exec(`UPDATE step_attempts SET deadline = now() - interval '1s' WHERE id = $1`, a.id)
	x.e.Tick(x.ctx)
	x.wantStatus(id, engine.RunWaitingHuman)
	// The wait timeout applies on_timeout: attention.
	x.exec(`UPDATE steps SET wait_deadline = now() - interval '1s' WHERE current_attempt_id = $1`, a.id)
	x.e.Tick(x.ctx)
	x.wantStatus(id, engine.RunNeedsAttention)
	if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'run.finished'`); n != 0 {
		t.Fatal("run.finished sent for needs_attention")
	}

	// A second run that succeeds notifies run.finished exactly once.
	id2 := x.start(nil)
	b := x.current(id2)
	x.succeed(b, map[string]any{"approved": true})
	x.succeed(b, map[string]any{"approved": true})
	x.wantStatus(id2, engine.RunSucceeded)
	if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'run.finished' AND routing_key = 'notify.watcher'`); n != 1 {
		t.Fatalf("run.finished to watcher: %d", n)
	}
}

func TestEngineRunRequested(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	req := contract.RunRequested{Pipeline: "hello-world", DedupKey: "t1:0", Data: json.RawMessage(`{"text":""}`)}
	if out := x.send("hello-world", contract.TypeRunRequested, nil, req); out != bus.Ignored {
		t.Fatalf("run.requested from a non-source module: %s", out)
	}
	if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'run.rejected' AND routing_key = 'control.hello-world' AND body->'payload'->>'code' = 'forbidden'`); n != 1 {
		t.Fatalf("run.rejected forbidden: %d", n)
	}
	for range 2 {
		if out := x.send("hello-init", contract.TypeRunRequested, nil, req); out != bus.Applied {
			t.Fatalf("run.requested: %s", out)
		}
	}
	if n := x.count(`SELECT count(*) FROM runs WHERE dedup_key = 't1:0' AND trigger->>'kind' = 'source'`); n != 1 {
		t.Fatalf("runs for one dedup key: %d", n)
	}
	unknown := contract.RunRequested{Pipeline: "nope", Data: json.RawMessage(`{}`)}
	x.send("hello-init", contract.TypeRunRequested, nil, unknown)
	if n := x.count(`SELECT count(*) FROM outbox WHERE type = 'run.rejected' AND body->'payload'->>'code' = 'unknown_pipeline'`); n != 1 {
		t.Fatalf("run.rejected unknown_pipeline: %d", n)
	}
}

func TestEngineReplicas(t *testing.T) {
	x := newEngine(t, helloModules, helloPipeline)
	online := func(replica, version string) {
		x.send("hello-hello", contract.TypeModuleOnline, nil, contract.ModuleOnline{Module: "hello-hello", ReplicaID: replica,
			ContractVersion: version, SDK: contract.SDKInfo{Name: "manufacture-sdk-python", Version: "0.3.0"}})
	}
	online("r1", "0.3.0")
	online("r2", "0.2.1")
	if n := x.count(`SELECT count(*) FROM module_replicas WHERE module = 'hello-hello' AND replica_id = 'r1' AND status = 'up'`); n != 1 {
		t.Fatal("r1 not up")
	}
	if n := x.count(`SELECT count(*) FROM module_replicas WHERE replica_id = 'r2' AND status = 'unhealthy'`); n != 1 {
		t.Fatal("incompatible r2 not unhealthy")
	}
	x.exec(`UPDATE module_replicas SET last_heartbeat_at = now() - interval '1m' WHERE replica_id = 'r1'`)
	x.e.Tick(x.ctx)
	if n := x.count(`SELECT count(*) FROM module_replicas WHERE replica_id = 'r1' AND status = 'offline'`); n != 1 {
		t.Fatal("silent r1 not offline")
	}
	x.send("hello-hello", contract.TypeModuleHeartbeat, nil, contract.ModuleHeartbeat{Module: "hello-hello", ReplicaID: "r1", InFlight: 2})
	if n := x.count(`SELECT count(*) FROM module_replicas WHERE replica_id = 'r1' AND status = 'up' AND in_flight = 2`); n != 1 {
		t.Fatal("heartbeat did not revive r1")
	}
}
