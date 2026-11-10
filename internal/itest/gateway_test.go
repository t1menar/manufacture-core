package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t1menar/manufacture-core/internal/gateway"
	"github.com/t1menar/manufacture-core/internal/mfctl"
	"github.com/t1menar/manufacture-core/internal/store"
)

const testToken = "test-admin-token-0123456789abcdef0123456789"

func newGateway(t *testing.T) *httptest.Server {
	t.Helper()
	s := newStore(t)
	gw := gateway.New(gateway.Options{Store: s, TokenHash: gateway.TokenHash(testToken),
		System: gateway.System{Version: "test", ContractVersion: "0.3.0", Driver: "docker", Roles: []string{"gateway"}}})
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	return srv
}

type resp struct {
	status int
	body   []byte
	header http.Header
}

func call(t *testing.T, srv *httptest.Server, method, path, ctype, body string, hdr map[string]string) resp {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, b, r.Header}
}

var bearer = map[string]string{"Authorization": "Bearer " + testToken}

func TestGatewayAuth(t *testing.T) {
	srv := newGateway(t)
	if r := call(t, srv, "GET", "/healthz", "", "", nil); r.status != 200 {
		t.Fatalf("healthz %d", r.status)
	}
	if r := call(t, srv, "GET", "/api/v1/openapi.yaml", "", "", nil); r.status != 200 || !bytes.Contains(r.body, []byte("openapi: 3.1.0")) {
		t.Fatalf("openapi %d", r.status)
	}
	r := call(t, srv, "GET", "/api/v1/modules", "", "", nil)
	if r.status != 401 || r.header.Get("Content-Type") != "application/problem+json" || !bytes.Contains(r.body, []byte(`"unauthorized"`)) {
		t.Fatalf("no token: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "GET", "/api/v1/modules", "", "", map[string]string{"Authorization": "Bearer wrong"}); r.status != 401 {
		t.Fatalf("wrong token: %d", r.status)
	}
	if r := call(t, srv, "GET", "/api/v1/system", "", "", bearer); r.status != 200 || !bytes.Contains(r.body, []byte(`"driver":"docker"`)) {
		t.Fatalf("system: %d %s", r.status, r.body)
	}
	// Session cookie.
	if r := call(t, srv, "POST", "/api/v1/session", "application/json", `{"token":"nope"}`, nil); r.status != 401 {
		t.Fatalf("bad session: %d", r.status)
	}
	r = call(t, srv, "POST", "/api/v1/session", "application/json", `{"token":"`+testToken+`"}`, nil)
	if r.status != 204 {
		t.Fatalf("session: %d %s", r.status, r.body)
	}
	cookie := strings.Split(r.header.Get("Set-Cookie"), ";")[0]
	if !strings.HasPrefix(cookie, gateway.SessionCookie+"=") || !strings.Contains(r.header.Get("Set-Cookie"), "HttpOnly") {
		t.Fatalf("cookie %q", r.header.Get("Set-Cookie"))
	}
	if r := call(t, srv, "GET", "/api/v1/modules", "", "", map[string]string{"Cookie": cookie}); r.status != 200 {
		t.Fatalf("cookie auth: %d", r.status)
	}
	if r := call(t, srv, "GET", "/api/v1/modules", "", "", map[string]string{"Cookie": gateway.SessionCookie + "=1.forged"}); r.status != 401 {
		t.Fatalf("forged cookie: %d", r.status)
	}
	cross := map[string]string{"Cookie": cookie, "Origin": "https://evil.example"}
	if r := call(t, srv, "POST", "/api/v1/apply", "application/yaml", helloModules, cross); r.status != 401 {
		t.Fatalf("cross-origin apply with cookie: %d", r.status)
	}
	if r := call(t, srv, "GET", "/api/v1/nothing", "", "", bearer); r.status != 404 {
		t.Fatalf("unknown endpoint: %d", r.status)
	}
	if r := call(t, srv, "GET", "/", "", "", nil); r.status != 200 || !strings.Contains(r.header.Get("Content-Type"), "text/html") {
		t.Fatalf("ui placeholder: %d", r.status)
	}
}

func TestGatewayApplyAndRead(t *testing.T) {
	srv := newGateway(t)
	r := call(t, srv, "POST", "/api/v1/apply?source=ui", "application/yaml", helloModules+"---\n"+helloPipeline, bearer)
	if r.status != 200 {
		t.Fatalf("apply: %d %s", r.status, r.body)
	}
	var ar struct {
		Applied bool                      `json:"applied"`
		Results []struct{ Action string } `json:"results"`
	}
	_ = json.Unmarshal(r.body, &ar)
	if !ar.Applied || len(ar.Results) != 5 {
		t.Fatalf("apply result %s", r.body)
	}
	bad := strings.Replace(helloPipeline, "timeout: 30s}\n    - name: world", "timeout: forever}\n    - name: world", 1)
	r = call(t, srv, "POST", "/api/v1/apply", "application/json",
		`{"files":[{"name":"p.yaml","content":`+jsonString(bad)+`}]}`, bearer)
	if r.status != 422 || r.header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("invalid apply: %d %s", r.status, r.body)
	}
	var pb gateway.Problem
	_ = json.Unmarshal(r.body, &pb)
	if pb.Code != gateway.CodeValidationFailed || len(pb.Errors) == 0 || pb.Errors[0].Source != "p.yaml" || pb.Errors[0].Line == 0 {
		t.Fatalf("problem: %s", r.body)
	}

	r = call(t, srv, "GET", "/api/v1/pipelines", "", "", bearer)
	var pl struct {
		Items []gateway.PipelineSummary `json:"items"`
	}
	_ = json.Unmarshal(r.body, &pl)
	if len(pl.Items) != 1 || len(pl.Items[0].Stages) != 3 || pl.Items[0].Stages[1].Lanes[0] != (gateway.Lane{StepID: "world", Module: "hello-world"}) {
		t.Fatalf("pipelines: %s", r.body)
	}
	r = call(t, srv, "GET", "/api/v1/pipelines/hello-world", "", "", bearer)
	var pd gateway.PipelineDetail
	_ = json.Unmarshal(r.body, &pd)
	if pd.Revision != 1 || pd.Source != "ui" || !strings.Contains(pd.YAML, "hello-exclaim") {
		t.Fatalf("pipeline detail: %s", r.body)
	}
	r = call(t, srv, "GET", "/api/v1/modules/hello-hello", "", "", bearer)
	var md gateway.ModuleDetail
	_ = json.Unmarshal(r.body, &md)
	if md.Replicas.Desired != 2 || md.Health != "unknown" || md.Driver != "docker" || md.ReplicaList == nil {
		t.Fatalf("module detail: %s", r.body)
	}
	r = call(t, srv, "POST", "/api/v1/modules/hello-hello/scale", "application/json", `{"replicas":3}`, bearer)
	if r.status != 200 || !bytes.Contains(r.body, []byte(`"desired":3`)) {
		t.Fatalf("scale: %d %s", r.status, r.body)
	}
	if r := call(t, srv, "GET", "/api/v1/modules/hello-hello/revisions", "", "", bearer); !bytes.Contains(r.body, []byte(`"revision":2`)) {
		t.Fatalf("revisions: %s", r.body)
	}
	if r := call(t, srv, "DELETE", "/api/v1/modules/hello-init", "", "", bearer); r.status != 409 {
		t.Fatalf("delete used module: %d", r.status)
	}
	if r := call(t, srv, "GET", "/api/v1/modules/nope", "", "", bearer); r.status != 404 {
		t.Fatalf("missing module: %d", r.status)
	}
	if r := call(t, srv, "GET", "/api/v1/overview", "", "", bearer); r.status != 200 || !bytes.Contains(r.body, []byte(`"last_event_id"`)) {
		t.Fatalf("overview: %s", r.body)
	}
	r = call(t, srv, "GET", "/api/v1/audit?limit=2", "", "", bearer)
	var al struct {
		Items      []store.AuditEntry `json:"items"`
		NextCursor string             `json:"next_cursor"`
	}
	_ = json.Unmarshal(r.body, &al)
	if len(al.Items) != 2 || al.Items[0].Action != "scale" || al.NextCursor == "" {
		t.Fatalf("audit: %s", r.body)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestMfctl(t *testing.T) {
	srv := newGateway(t)
	dir := t.TempDir()
	writeFile(t, dir+"/modules.yaml", helloModules)
	writeFile(t, dir+"/pipeline.yaml", helloPipeline)
	env := map[string]string{mfctl.EnvURL: srv.URL, mfctl.EnvToken: testToken}
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := mfctl.Run(context.Background(), mfctl.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
			Getenv: func(k string) string { return env[k] }}, args)
		return code, out.String(), errb.String()
	}
	if code, out, errs := run("apply", "-f", dir); code != 0 || !strings.Contains(out, "pipeline/hello-world created (revision 1)") {
		t.Fatalf("apply: %d %q %q", code, out, errs)
	}
	if code, out, _ := run("apply", "-f", dir); code != 0 || strings.Count(out, "unchanged") != 5 {
		t.Fatalf("re-apply: %d %q", code, out)
	}
	if code, out, _ := run("get", "pipelines"); code != 0 || !strings.Contains(out, "hello → world → exclaim") {
		t.Fatalf("get pipelines: %d %q", code, out)
	}
	if code, out, _ := run("get", "modules", "-o", "json"); code != 0 || !strings.Contains(out, `"hello-exclaim"`) {
		t.Fatalf("get modules json: %d %q", code, out)
	}
	if code, out, _ := run("describe", "pipeline/hello-world"); code != 0 || !strings.Contains(out, "--- spec (revision 1)") {
		t.Fatalf("describe: %d %q", code, out)
	}
	writeFile(t, dir+"/pipeline.yaml", strings.Replace(helloPipeline, "hello-exclaim", "hello-nope", 1))
	if code, _, errs := run("apply", "-f", dir); code != 1 || !strings.Contains(errs, "pipeline.yaml:") || !strings.Contains(errs, "hello-nope") {
		t.Fatalf("invalid apply: %d %q", code, errs)
	}
	env[mfctl.EnvToken] = ""
	if code, _, errs := run("get", "modules"); code != 1 || !strings.Contains(errs, "set MF_ADMIN_TOKEN") {
		t.Fatalf("no token: %d %q", code, errs)
	}
	if code, _, _ := run("frobnicate"); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
}
