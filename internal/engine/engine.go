// Package engine runs pipelines: admission, stage scheduling, attempts, results,
// timeouts, retries, budgets and run notifications (docs/architecture.md §6,
// design-amendments R1–R10). All state lives in PostgreSQL; every transition
// locks the run row first, then step and attempt rows, and writes its broker
// messages to the outbox in the same transaction.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/t1menar/manufacture-core/internal/bus"
	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/db"
	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
)

// Options configure the engine.
type Options struct {
	Pool  *pgxpool.Pool
	Store *store.Store
	Conn  *bus.Conn
	Topo  bus.Topology
	Log   *slog.Logger
	// Tick is the period of admission, dispatch and sweeps (default 500 ms).
	Tick time.Duration
	// RedispatchAfter re-publishes a step.requested that stayed dispatched this long
	// while the module has a live replica (default 15 min).
	RedispatchAfter time.Duration
	// BlobBucket and BlobPrefix build payload.blob_prefix: s3://<bucket>/runs/<run>/<step>/<attempt>/.
	BlobBucket string
	// RequireUserID refuses module messages whose AMQP user_id is not
	// BrokerUserPrefix+<module> (set once every module has its own broker user).
	RequireUserID bool
	// Metrics receives counters; nil disables them.
	Metrics Metrics
}

// BrokerUserPrefix prefixes the RabbitMQ user of a module.
const BrokerUserPrefix = "mf-module-"

// Metrics is what the engine reports (implemented by the metrics package).
type Metrics interface {
	RunFinished(pipeline, status string, d time.Duration)
	StaleResult(module, reason string)
}

type nopMetrics struct{}

func (nopMetrics) RunFinished(string, string, time.Duration) {}
func (nopMetrics) StaleResult(string, string)                {}

// Engine is the pipeline engine.
type Engine struct {
	o     Options
	log   *slog.Logger
	wake  chan struct{}
	relay chan struct{}

	mu        sync.Mutex
	pipelines map[string]*spec.Pipeline // "<name>@<revision>"
	declared  map[string]bool           // "<module>@<revision>" with queues declared
}

// New builds an engine.
func New(o Options) *Engine {
	if o.Tick == 0 {
		o.Tick = 500 * time.Millisecond
	}
	if o.RedispatchAfter == 0 {
		o.RedispatchAfter = 15 * time.Minute
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Metrics == nil {
		o.Metrics = nopMetrics{}
	}
	if o.BlobBucket == "" {
		o.BlobBucket = "mf-blobs"
	}
	return &Engine{o: o, log: o.Log, wake: make(chan struct{}, 1), relay: make(chan struct{}, 1), pipelines: map[string]*spec.Pipeline{}, declared: map[string]bool{}}
}

// Run starts the relay, the event consumer and the scheduling loop until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	relay := &bus.Relay{Pool: e.o.Pool, Log: e.log, Wake: e.relay}
	cons := &bus.Consumer{Pool: e.o.Pool, Name: "core.events", Queue: e.o.Topo.CoreEvents(), Handler: e.handle, Log: e.log,
		OnInvalid: e.onInvalid}
	wg.Add(3)
	go func() { defer wg.Done(); relay.Run(ctx, e.o.Conn.Current, time.Second) }()
	go func() { defer wg.Done(); cons.Run(ctx, e.o.Conn.Current) }()
	go func() { defer wg.Done(); e.loop(ctx) }()
	wg.Wait()
}

func (e *Engine) loop(ctx context.Context) {
	t := time.NewTicker(e.o.Tick)
	defer t.Stop()
	for {
		e.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.wake:
		}
	}
}

// Tick runs one round of admission, dispatch and sweeps (exported for tests).
func (e *Engine) Tick(ctx context.Context) {
	for _, step := range []struct {
		name string
		fn   func(context.Context) error
	}{
		{"declare", e.declareModules}, {"admission", e.admit}, {"dispatch", e.dispatch}, {"sweep", e.sweep}, {"replicas", e.sweepReplicas},
	} {
		if err := step.fn(ctx); err != nil && ctx.Err() == nil {
			e.log.Warn("engine "+step.name, "err", err)
		}
	}
}

func (e *Engine) poke() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
	select {
	case e.relay <- struct{}{}:
	default:
	}
}

// Handle applies one module message in a transaction (the consumer path; exported for tests).
func (e *Engine) Handle(ctx context.Context, m bus.Message) (string, error) {
	var out string
	err := e.tx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = e.handle(ctx, tx, m)
		return err
	})
	return out, err
}

// tx runs fn in a transaction and wakes the loops after a commit.
func (e *Engine) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	if err := db.WithTx(ctx, e.o.Pool, fn); err != nil {
		return err
	}
	e.poke()
	return nil
}

// pipeline returns a pipeline revision (immutable, cached).
func (e *Engine) pipeline(ctx context.Context, name string, revision int) (*spec.Pipeline, error) {
	key := fmt.Sprintf("%s@%d", name, revision)
	e.mu.Lock()
	p, ok := e.pipelines[key]
	e.mu.Unlock()
	if ok {
		return p, nil
	}
	rev, err := e.o.Store.RevisionAt(ctx, spec.KindPipeline, name, revision)
	if err != nil {
		return nil, err
	}
	p, err = rev.Pipeline()
	if err != nil {
		return nil, err
	}
	for i := range p.Spec.Stages { // revisions are stored validated, ids assigned; keep defaults safe
		st := &p.Spec.Stages[i]
		if st.Step != nil && st.Step.ID == "" {
			st.Step.ID = st.Name
		}
		for j := range st.Parallel {
			if st.Parallel[j].ID == "" {
				st.Parallel[j].ID = st.Parallel[j].Module
			}
		}
	}
	e.mu.Lock()
	e.pipelines[key] = p
	e.mu.Unlock()
	return p, nil
}

// module returns the current Module spec, or nil when it does not exist.
func (e *Engine) module(ctx context.Context, name string) (*spec.Module, error) {
	rev, err := e.o.Store.Get(ctx, spec.KindModule, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rev.Module()
}

// stepEventType maps a step status to its stream event (api/openapi.yaml StreamEventType).
func stepEventType(status string) string {
	switch status {
	case StepDispatched:
		return "step.dispatched"
	case StepRunning:
		return "step.started"
	case StepSucceeded:
		return "step.succeeded"
	case StepFailed, StepCancelled:
		return "step.failed"
	case StepSkipped:
		return "step.skipped"
	default: // pending (retry scheduled), waiting
		return "step.progress"
	}
}

// emitStep records a StepEvent for a step row.
func emitStep(ctx context.Context, tx pgx.Tx, stepRow int64, status string, extra map[string]any) error {
	var (
		runID                    uuid.UUID
		pipeline, stage, id, mod string
		stageIndex, visit        int
		key                      uuid.UUID
		item                     *int
		attempt                  *uuid.UUID
		number                   *int
	)
	if err := tx.QueryRow(ctx, `SELECT s.run_id, r.pipeline, s.stage, s.stage_index, s.step_id, s.module, s.visit, s.step_key, s.item_index,
			s.current_attempt_id, a.number
		FROM steps s JOIN runs r ON r.id = s.run_id LEFT JOIN step_attempts a ON a.id = s.current_attempt_id WHERE s.id = $1`, stepRow).Scan(
		&runID, &pipeline, &stage, &stageIndex, &id, &mod, &visit, &key, &item, &attempt, &number); err != nil {
		return err
	}
	ev := map[string]any{"run_id": runID, "pipeline": pipeline, "stage": stage, "stage_index": stageIndex, "step_id": id, "module": mod,
		"step_key": key, "visit": visit, "status": status, "at": time.Now().UTC()}
	if item != nil {
		ev["item_index"] = *item
	}
	if attempt != nil {
		ev["attempt_id"], ev["attempt"] = *attempt, *number
	}
	for k, v := range extra {
		ev[k] = v
	}
	return emit(ctx, tx, stepEventType(status), &runID, ev)
}

// emit records a timeline event (and the source of the SSE stream).
func emit(ctx context.Context, tx pgx.Tx, typ string, runID *uuid.UUID, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO events (type, run_id, data) VALUES ($1, $2, $3)`, typ, runID, b)
	return err
}

func (e *Engine) send(ctx context.Context, tx pgx.Tx, exchange, key string, priority uint8, env contract.Envelope) error {
	return bus.Enqueue(ctx, tx, exchange, key, priority, env)
}

func envelope(id uuid.UUID, typ contract.Type, runID *uuid.UUID, att *contract.AttemptRef, payload any) (contract.Envelope, error) {
	env, err := contract.New(id, typ, contract.ProducerCore, time.Now(), payload)
	if err != nil {
		return env, err
	}
	env.RunID, env.Attempt = runID, att
	return env, nil
}

// declareModules declares the queues of every applied module (idempotent; cheap
// once declared, so it runs on each tick only for modules not seen yet).
func (e *Engine) declareModules(ctx context.Context) error {
	if e.o.Conn == nil {
		return nil
	}
	conn := e.o.Conn.Current()
	if conn == nil {
		return nil
	}
	revs, err := e.o.Store.List(ctx, spec.KindModule)
	if err != nil {
		return err
	}
	var todo []*spec.Module
	e.mu.Lock()
	for _, r := range revs {
		key := fmt.Sprintf("%s@%d", r.Name, r.Revision)
		if e.declared[key] {
			continue
		}
		m, err := r.Module()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		todo = append(todo, m)
		e.declared[key] = true
	}
	e.mu.Unlock()
	if len(todo) == 0 {
		return nil
	}
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()
	for _, m := range todo {
		if err := e.o.Topo.DeclareModule(ch, m.Metadata.Name, len(m.Spec.Subscribe) > 0); err != nil {
			return fmt.Errorf("declare queues of module %s: %w", m.Metadata.Name, err)
		}
	}
	return nil
}
