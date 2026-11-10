package itest

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/t1menar/manufacture-core/internal/engine"
	"github.com/t1menar/manufacture-core/internal/gateway"
	"github.com/t1menar/manufacture-core/internal/metrics"
	"github.com/t1menar/manufacture-core/internal/store"
)

func TestRunsAPI(t *testing.T) {
	pool := newDB(t)
	st := store.New(pool)
	ctx := ctxT(t)
	if _, err := st.Apply(ctx, store.ApplyRequest{Files: []store.File{file("m.yaml", helloModules), file("p.yaml", helloPipeline)},
		Source: store.SourceCLI, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	e := engine.New(engine.Options{Pool: pool, Store: st, Log: quietLog()})
	gw := gateway.New(gateway.Options{Store: st, TokenHash: gateway.TokenHash(testToken), Runs: e, Metrics: &metrics.Metrics{Pool: pool},
		PipelineBusy: engine.PipelineBusy, Logger: quietLog()})
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	js := "application/json"

	// No data and a trigger source: source.trigger, 202.
	if r := call(t, srv, "POST", "/api/v1/pipelines/hello-world/runs", js, `{}`, bearer); r.status != 202 || !bytes.Contains(r.body, []byte("trigger_id")) {
		t.Fatalf("trigger: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "POST", "/api/v1/pipelines/nope/runs", js, `{"data":{}}`, bearer); r.status != 404 {
		t.Fatalf("unknown pipeline: %d %s", r.status, r.body)
	}
	hdr := map[string]string{"Authorization": bearer["Authorization"], "Idempotency-Key": "k1"}
	r := call(t, srv, "POST", "/api/v1/pipelines/hello-world/runs", js, `{"data":{"text":""},"labels":{"a":"b"}}`, hdr)
	if r.status != 201 {
		t.Fatalf("start: %d %s", r.status, r.body)
	}
	var run engine.RunView
	_ = json.Unmarshal(r.body, &run)
	if r2 := call(t, srv, "POST", "/api/v1/pipelines/hello-world/runs", js, `{"data":{"text":""}}`, hdr); !bytes.Contains(r2.body, []byte(run.ID.String())) {
		t.Fatalf("same Idempotency-Key gave another run: %s", r2.body)
	}
	e.Tick(ctx)
	r = call(t, srv, "GET", "/api/v1/runs/"+run.ID.String(), "", "", bearer)
	var d engine.RunDetail
	if err := json.Unmarshal(r.body, &d); err != nil || r.status != 200 || d.Run.Status != "running" || len(d.Steps) != 1 || len(d.Steps[0].Attempts) != 1 {
		t.Fatalf("detail: %d %s", r.status, r.body)
	}
	if _, ok := d.Context["text"]; !ok {
		t.Fatalf("context is run data: %v", d.Context)
	}
	call(t, srv, "POST", "/api/v1/pipelines/hello-world/runs", js, `{"data":{"text":""}}`, bearer)
	if r := call(t, srv, "GET", "/api/v1/runs?pipeline=hello-world&limit=1", "", "", bearer); r.status != 200 || !bytes.Contains(r.body, []byte("next_cursor")) {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "DELETE", "/api/v1/pipelines/hello-world", "", "", bearer); r.status != 409 {
		t.Fatalf("delete busy pipeline: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "POST", "/api/v1/runs/"+run.ID.String()+"/cancel", js, `{}`, bearer); r.status != 422 {
		t.Fatalf("cancel without comment: %d", r.status)
	}
	if r := call(t, srv, "POST", "/api/v1/runs/"+run.ID.String()+"/resume", js, `{"comment":"x"}`, bearer); r.status != 409 {
		t.Fatalf("resume a running run: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "POST", "/api/v1/runs/"+run.ID.String()+"/cancel", js, `{"comment":"test"}`, bearer); r.status != 200 ||
		!bytes.Contains(r.body, []byte(`"status":"cancelled"`)) {
		t.Fatalf("cancel: %d %s", r.status, r.body)
	}
	r = call(t, srv, "POST", "/api/v1/runs/"+run.ID.String()+"/retry", js, `{"comment":"again"}`, bearer)
	if r.status != 201 || !bytes.Contains(r.body, []byte(`"kind":"retry"`)) {
		t.Fatalf("retry: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "GET", "/api/v1/runs/"+run.ID.String()+"/events", "", "", bearer); r.status != 200 || !bytes.Contains(r.body, []byte("run.finished")) {
		t.Fatalf("events: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "GET", "/metrics", "", "", nil); r.status != 200 ||
		!bytes.Contains(r.body, []byte(`mf_runs_finished_total{pipeline="hello-world",status="cancelled"} 1`)) {
		t.Fatalf("metrics: %d %s", r.status, r.body)
	}
}
