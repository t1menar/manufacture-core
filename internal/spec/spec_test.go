package spec

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustValidate(t *testing.T, src string) (*Set, Diagnostics) {
	t.Helper()
	docs, diags := Parse("test.yaml", []byte(src))
	if len(diags) > 0 {
		t.Fatalf("parse: %v", diags)
	}
	return Validate(docs, Options{})
}

func TestPositionsPointAtTheField(t *testing.T) {
	src := `apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: p}
spec:
  stages:
    - name: a
      step: {module: m}
    - name: b
      step:
        module: m2
        timeout: soon
`
	_, diags := mustValidate(t, src)
	if len(diags) != 1 {
		t.Fatalf("got %v, want one diagnostic", diags)
	}
	d := diags[0]
	if d.Line != 11 || d.Column != 9 || d.Path != "spec.stages[1].step.timeout" || d.Resource != "Pipeline/p" {
		t.Errorf("got %s (line %d col %d)", d, d.Line, d.Column)
	}
}

func TestParseRejectsWhatJSONCannotHold(t *testing.T) {
	for name, src := range map[string]string{
		"non-string key": "apiVersion: manufacture.dev/v1\n1: x\n",
		"infinity":       "a: .inf\n",
		"merge key":      "base: &b {x: 1}\nother:\n  <<: *b\n",
		"syntax":         "a: [1, 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, diags := Parse("x.yaml", []byte(src)); diags.Errors() == 0 {
				t.Error("expected an error")
			}
		})
	}
}

func TestParseSkipsEmptyDocuments(t *testing.T) {
	docs, diags := Parse("x.yaml", []byte("---\n# only a comment\n---\nkind: Module\n---\n"))
	if len(diags) > 0 || len(docs) != 1 {
		t.Fatalf("got %d docs, diags %v", len(docs), diags)
	}
	if _, kind, _ := docs[0].header(); kind != KindModule {
		t.Errorf("kind = %q", kind)
	}
}

func TestValidateFillsStepIDs(t *testing.T) {
	set, diags := mustValidate(t, `apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: p}
spec:
  stages:
    - name: single
      step: {module: m}
    - name: fan
      parallel: [{module: x}, {id: custom, module: y}]
`)
	if diags.Errors() > 0 {
		t.Fatal(diags)
	}
	st := set.Pipelines[0].Spec.Stages
	got := []string{st[0].Step.ID, st[1].Parallel[0].ID, st[1].Parallel[1].ID}
	if strings.Join(got, ",") != "single,x,custom" {
		t.Errorf("step ids = %v", got)
	}
	if st[0].Kind() != StageSingle || st[1].Kind() != StageParallel {
		t.Errorf("kinds = %s, %s", st[0].Kind(), st[1].Kind())
	}
}

func TestReferencesAreWarningsUnlessRequired(t *testing.T) {
	src := `apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: p}
spec:
  trigger: {source: src}
  stages: [{name: a, step: {module: m}}]
`
	docs, _ := Parse("p.yaml", []byte(src))
	if _, diags := Validate(docs, Options{}); diags.Errors() != 0 || diags.Warnings() != 2 {
		t.Errorf("default: %v", diags)
	}
	if _, diags := Validate(docs, Options{RequireReferences: true}); diags.Errors() != 2 {
		t.Errorf("required: %v", diags)
	}
}

func TestStepPolicyDefaults(t *testing.T) {
	retry := &Retry{MaxAttempts: 2}
	defaults := &StepPolicy{Timeout: Duration(time.Minute), Lease: Duration(10 * time.Second), Retry: retry}
	got := Step{Timeout: Duration(time.Hour)}.Policy(defaults)
	if got.Timeout.Std() != time.Hour || got.Lease.Std() != 10*time.Second || got.Retry != retry {
		t.Errorf("policy = %+v", got)
	}
	if p := (Step{}).Policy(nil); p.Timeout != 0 || p.Retry != nil {
		t.Errorf("policy without defaults = %+v", p)
	}
}

func TestImageReferences(t *testing.T) {
	for image, want := range map[string]Severity{
		"localhost:5001/mf-hello:0.1.0":                              "",
		"ghcr.io/t1menar/mf-video@sha256:" + strings.Repeat("a", 64): "",
		"registry.example.com:443/team/app:v1.2.3-rc.1":              "",
		"busybox":          SeverityWarning,
		"busybox:latest":   SeverityWarning,
		"UPPER/app:1":      SeverityError,
		"app:bad tag":      SeverityError,
		"app@sha256:short": SeverityError,
	} {
		r := &reporter{doc: mustDocument(t)}
		checkImage(r, Path{"spec", "image"}, image)
		var got Severity
		if len(r.diags) > 0 {
			got = r.diags[0].Severity
		}
		if got != want {
			t.Errorf("%q: severity %q, want %q (%v)", image, got, want, r.diags)
		}
	}
}

func mustDocument(t *testing.T) *Document {
	t.Helper()
	docs, diags := Parse("x", []byte("a: 1\n"))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	return docs[0]
}

func TestCheckCron(t *testing.T) {
	for _, ok := range []string{"* * * * *", "*/5 0-6 1,15 JAN-MAR mon", "@daily"} {
		if err := checkCron(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"* * * *", "@sometimes", "* * * * ?!", "1-2-3 * * * *"} {
		if err := checkCron(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestDiagnosticFormatting(t *testing.T) {
	d := Diagnostic{Severity: SeverityError, Source: "a.yaml", Line: 3, Column: 5, Resource: "Module/m", Path: "spec.image", Message: "bad"}
	if got := d.String(); got != "a.yaml:3:5: error: Module/m: spec.image: bad" {
		t.Errorf("got %q", got)
	}
	if got := (Path{"spec", "stages", "1", "routes", "0"}).String(); got != "spec.stages[1].routes[0]" {
		t.Errorf("path = %q", got)
	}
	base := Path{"a"}
	x, y := base.Key("x"), base.Key("y")
	if x.String() != "a.x" || y.String() != "a.y" {
		t.Errorf("Key must not alias: %s %s", x, y)
	}
}

func TestReplicasJSON(t *testing.T) {
	for in, want := range map[string]Replicas{
		`3`:                 Fixed(3),
		`{"min":0,"max":4}`: {Min: 0, Max: 4, Autoscale: true, Metric: ScaleQueueDepth},
		`{"min":1,"max":2,"metric":"cpu","target":70}`: {Min: 1, Max: 2, Autoscale: true, Metric: ScaleCPU, Target: 70},
	} {
		var got Replicas
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", in, got, want)
		}
		out, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var back Replicas
		if err := json.Unmarshal(out, &back); err != nil || back != got {
			t.Errorf("%s: round trip gave %s (%v)", in, out, err)
		}
	}
	var r Replicas
	if err := json.Unmarshal([]byte(`"two"`), &r); err == nil {
		t.Error("string replicas must fail")
	}
}
