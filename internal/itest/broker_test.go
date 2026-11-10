package itest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/t1menar/manufacture-core/internal/bus"
	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/engine"
	"github.com/t1menar/manufacture-core/internal/store"
)

const envAMQPURL = "MF_TEST_AMQP_URL"

// fakeModule answers step.requested like the SDK would: heartbeat first, then
// the result; a source module answers source.trigger with run.requested.
func fakeModule(ctx context.Context, t *testing.T, url string, topo bus.Topology, module string, handle func(env contract.Envelope) (contract.Type, any)) {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if err := topo.DeclareModule(ch, module, false); err != nil {
		t.Fatalf("declare %s: %v", module, err)
	}
	if err := ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	deliveries, err := ch.Consume(topo.WorkQueue(module), "", false, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(typ contract.Type, att *contract.AttemptRef, runID *uuid.UUID, payload any) {
		env, err := contract.New(uuid.New(), typ, contract.ModuleProducer(module), time.Now(), payload)
		if err != nil {
			t.Error(err)
			return
		}
		env.Attempt, env.RunID = att, runID
		body, err := contract.Encode(env)
		if err != nil {
			t.Error(err)
			return
		}
		c, err := ch.PublishWithDeferredConfirmWithContext(ctx, topo.Events(), "module."+module+"."+string(typ), true, false,
			amqp.Publishing{ContentType: "application/json", MessageId: env.MessageID.String(), Type: string(typ), Body: body})
		if err == nil {
			_, err = c.WaitContext(ctx)
		}
		if err != nil && ctx.Err() == nil {
			t.Error(err)
		}
	}
	go func() {
		for d := range deliveries {
			env, err := contract.Decode(d.Body)
			if err != nil {
				t.Errorf("%s: invalid message: %v", module, err)
				_ = d.Nack(false, false)
				continue
			}
			if env.Type == contract.TypeStepRequested {
				publish(contract.TypeStepHeartbeat, env.Attempt, env.RunID, contract.StepHeartbeat{ReplicaID: "r1"})
			}
			typ, payload := handle(env)
			publish(typ, env.Attempt, env.RunID, payload)
			_ = d.Ack(false)
		}
	}()
}

func appendStep(suffix string) func(env contract.Envelope) (contract.Type, any) {
	return func(env contract.Envelope) (contract.Type, any) {
		var req contract.StepRequested
		_ = json.Unmarshal(env.Payload, &req)
		var in struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(req.Input, &in)
		out, _ := json.Marshal(map[string]string{"text": in.Text + suffix})
		return contract.TypeStepSucceeded, contract.StepSucceeded{Output: out, ReplicaID: "r1"}
	}
}

func TestBrokerHelloWorld(t *testing.T) {
	url := os.Getenv(envAMQPURL)
	if url == "" {
		t.Skipf("%s is not set", envAMQPURL)
	}
	pool := newDB(t)
	st := store.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := st.Apply(ctx, store.ApplyRequest{Files: []store.File{file("m.yaml", helloModules), file("p.yaml", helloPipeline)},
		Source: store.SourceCLI, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	topo := bus.Topology{Prefix: "t" + hex.EncodeToString(b) + "."}
	conn := &bus.Conn{URL: url, Topo: topo, Log: quietLog()}
	e := engine.New(engine.Options{Pool: pool, Store: st, Conn: conn, Topo: topo, Log: quietLog(), Tick: 100 * time.Millisecond})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{}, 2)
	go func() { conn.Run(runCtx); done <- struct{}{} }()
	if err := conn.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { e.Run(runCtx); done <- struct{}{} }()
	t.Cleanup(func() {
		stop()
		<-done
		<-done
		cleanupTopology(t, url, topo)
	})

	fakeModule(ctx, t, url, topo, "hello-hello", appendStep("hello"))
	fakeModule(ctx, t, url, topo, "hello-world", appendStep(" world"))
	fakeModule(ctx, t, url, topo, "hello-exclaim", appendStep("!"))
	fakeModule(ctx, t, url, topo, "hello-init", func(env contract.Envelope) (contract.Type, any) {
		var trig contract.SourceTrigger
		_ = json.Unmarshal(env.Payload, &trig)
		return contract.TypeRunRequested, contract.RunRequested{Pipeline: trig.Pipeline, TriggerID: &trig.TriggerID,
			DedupKey: trig.TriggerID.String() + ":0", Data: json.RawMessage(`{"text":""}`)}
	})

	res, err := e.StartRun(ctx, engine.StartRequest{Pipeline: "hello-world", Data: map[string]any{"text": ""}, Trigger: engine.Trigger{Kind: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(ctx, t, e, res.RunID)

	if _, err := e.TriggerSource(ctx, "hello-world", nil); err != nil {
		t.Fatal(err)
	}
	var sourced uuid.UUID
	for ctx.Err() == nil {
		if err := pool.QueryRow(ctx, `SELECT id FROM runs WHERE trigger->>'kind' = 'source'`).Scan(&sourced); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitRun(ctx, t, e, sourced)
}

func waitRun(ctx context.Context, t *testing.T, e *engine.Engine, id uuid.UUID) {
	t.Helper()
	for {
		d, err := e.GetRunDetail(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Run.Status == engine.RunSucceeded {
			if d.Context["text"] != "hello world!" || len(d.Steps) != 3 {
				t.Fatalf("run data %v, %d steps", d.Context, len(d.Steps))
			}
			return
		}
		if d.Run.Status == engine.RunFailed || d.Run.Status == engine.RunNeedsAttention {
			t.Fatalf("run %s: %+v", d.Run.Status, d.Run)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("run still %s", d.Run.Status)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func cleanupTopology(t *testing.T, url string, topo bus.Topology) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return
	}
	for _, m := range []string{"hello-init", "hello-hello", "hello-world", "hello-exclaim"} {
		_, _ = ch.QueueDelete(topo.WorkQueue(m), false, false, false)
	}
	_, _ = ch.QueueDelete(topo.CoreEvents(), false, false, false)
	_, _ = ch.QueueDelete(topo.DeadLetters(), false, false, false)
	for _, x := range []string{topo.Commands(), topo.Control(), topo.Events(), topo.DLX()} {
		_ = ch.ExchangeDelete(x, false, false)
	}
	t.Log("topology removed")
}
