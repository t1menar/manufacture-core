package itest

import (
	"errors"
	"strings"
	"testing"

	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
)

func apply(t *testing.T, s *store.Store, src store.Source, files ...store.File) store.ApplyResult {
	t.Helper()
	res, err := s.Apply(ctxT(t), store.ApplyRequest{Files: files, Source: src, Actor: "test"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return res
}

func actions(res store.ApplyResult) string {
	var parts []string
	for _, r := range res.Results {
		parts = append(parts, string(r.Kind)+"/"+r.Name+"="+r.Action+"@"+string(rune('0'+r.Revision)))
	}
	return strings.Join(parts, " ")
}

func TestApplyIsIdempotent(t *testing.T) {
	s := newStore(t)
	in := []store.File{file("modules.yaml", helloModules), file("pipeline.yaml", helloPipeline)}
	first := apply(t, s, store.SourceCLI, in...)
	if !first.Applied || len(first.Results) != 5 {
		t.Fatalf("first apply: %+v", first)
	}
	for _, r := range first.Results {
		if r.Action != store.ActionCreated || r.Revision != 1 {
			t.Fatalf("first apply: %s", actions(first))
		}
	}
	second := apply(t, s, store.SourceCLI, in...)
	for _, r := range second.Results {
		if r.Action != store.ActionUnchanged || r.Revision != 1 {
			t.Fatalf("second apply: %s", actions(second))
		}
	}
	revs, err := s.Revisions(ctxT(t), spec.KindPipeline, "hello-world")
	if err != nil || len(revs) != 1 {
		t.Fatalf("revisions = %d, %v; want exactly 1", len(revs), err)
	}
	// Formatting and key order do not create a revision.
	reordered := strings.ReplaceAll(helloPipeline, "{module: hello-world, timeout: 30s}", "{timeout: 30s,   module: hello-world}")
	if r := apply(t, s, store.SourceCLI, file("p.yaml", reordered)); r.Results[0].Action != store.ActionUnchanged {
		t.Fatalf("reordered: %s", actions(r))
	}
}

func TestApplyUpdateCreatesRevision(t *testing.T) {
	s := newStore(t)
	apply(t, s, store.SourceCLI, file("m.yaml", helloModules), file("p.yaml", helloPipeline))
	changed := strings.Replace(helloPipeline, "timeout: 30s}\n    - name: world", "timeout: 45s}\n    - name: world", 1)
	res := apply(t, s, store.SourceUI, file("p.yaml", changed))
	if res.Results[0].Action != store.ActionUpdated || res.Results[0].Revision != 2 {
		t.Fatalf("update: %s", actions(res))
	}
	cur, err := s.Get(ctxT(t), spec.KindPipeline, "hello-world")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Revision != 2 || cur.Source != store.SourceUI || !strings.Contains(cur.YAML, "45s") {
		t.Fatalf("current = rev %d source %s yaml %q", cur.Revision, cur.Source, cur.YAML)
	}
	p, err := cur.Pipeline()
	if err != nil || p.Spec.Stages[0].Step.ID != "hello" {
		t.Fatalf("stored spec must have step ids filled: %+v %v", p, err)
	}
	old, err := s.RevisionAt(ctxT(t), spec.KindPipeline, "hello-world", 1)
	if err != nil || old.Current || strings.Contains(old.YAML, "45s") {
		t.Fatalf("revision 1: %+v %v", old, err)
	}
}

func TestApplyRejectsInvalidInputAtomically(t *testing.T) {
	s := newStore(t)
	bad := strings.Replace(helloPipeline, "module: hello-exclaim", "module: hello-missing", 1)
	_, err := s.Apply(ctxT(t), store.ApplyRequest{Files: []store.File{file("m.yaml", helloModules), file("p.yaml", bad)},
		Source: store.SourceCLI, Actor: "test"})
	var ve *store.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	d := ve.Diagnostics[0]
	if d.Source != "p.yaml" || d.Line == 0 || d.Path != "spec.stages[2].step.module" || d.Severity != spec.SeverityError {
		t.Fatalf("diagnostic = %+v", d)
	}
	if mods, _ := s.List(ctxT(t), spec.KindModule); len(mods) != 0 {
		t.Fatalf("modules were stored although the apply failed: %d", len(mods))
	}
	// A pipeline may reference modules that are already in the core.
	apply(t, s, store.SourceCLI, file("m.yaml", helloModules))
	if r := apply(t, s, store.SourceCLI, file("p.yaml", helloPipeline)); r.Results[0].Action != store.ActionCreated {
		t.Fatalf("pipeline: %s", actions(r))
	}
	_, err = s.Apply(ctxT(t), store.ApplyRequest{Files: []store.File{file("empty.yaml", "# nothing\n")}, Source: store.SourceCLI, Actor: "t"})
	if !errors.As(err, &ve) {
		t.Fatalf("empty input: %v", err)
	}
}

func TestDryRunStoresNothing(t *testing.T) {
	s := newStore(t)
	res, err := s.Apply(ctxT(t), store.ApplyRequest{Files: []store.File{file("m.yaml", helloModules)}, DryRun: true,
		Source: store.SourceCLI, Actor: "t"})
	if err != nil || res.Applied || len(res.Results) != 4 || res.Results[0].Action != store.ActionCreated {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if mods, _ := s.List(ctxT(t), spec.KindModule); len(mods) != 0 {
		t.Fatal("dry run stored modules")
	}
}

func TestDeleteKeepsReferencesConsistent(t *testing.T) {
	s := newStore(t)
	ctx := ctxT(t)
	apply(t, s, store.SourceCLI, file("m.yaml", helloModules), file("p.yaml", helloPipeline))
	err := s.Delete(ctx, spec.KindModule, "hello-init", "t", nil)
	if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "hello-world") {
		t.Fatalf("delete used module: %v", err)
	}
	if err := s.Delete(ctx, spec.KindPipeline, "hello-world", "t", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, spec.KindModule, "hello-init", "t", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, spec.KindModule, "hello-init"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err := s.Delete(ctx, spec.KindModule, "hello-init", "t", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	// Re-creating continues the numbering, so revision numbers are never reused.
	changed := strings.Replace(helloModules, "replicas: 1}", "replicas: 3}", 1)
	res := apply(t, s, store.SourceCLI, file("m.yaml", changed))
	if res.Results[0].Action != store.ActionCreated || res.Results[0].Revision != 2 {
		t.Fatalf("re-create: %s", actions(res))
	}
}

func TestScaleAndManagedBy(t *testing.T) {
	s := newStore(t)
	ctx := ctxT(t)
	managed := strings.Replace(helloModules, "metadata: {name: hello-hello}",
		"metadata: {name: hello-hello, annotations: {manufacture.dev/managed-by: git}}", 1)
	apply(t, s, store.SourceCLI, file("m.yaml", managed))
	rev, err := s.Scale(ctx, "hello-world", 4, store.SourceCLI, "t")
	if err != nil || rev.Revision != 2 {
		t.Fatalf("scale: rev %d %v", rev.Revision, err)
	}
	m, _ := rev.Module()
	if m.Spec.Replicas == nil || m.Spec.Replicas.Min != 4 || m.Spec.Replicas.Max != 4 {
		t.Fatalf("replicas = %+v", m.Spec.Replicas)
	}
	if again, _ := s.Scale(ctx, "hello-world", 4, store.SourceCLI, "t"); again.Revision != 2 {
		t.Fatalf("same scale created revision %d", again.Revision)
	}
	if _, err := s.Scale(ctx, "hello-hello", 1, store.SourceUI, "t"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("UI scale of a git-managed module: %v", err)
	}
	_, err = s.Apply(ctx, store.ApplyRequest{Files: []store.File{file("m.yaml", strings.Replace(managed, "replicas: 2", "replicas: 5", 1))},
		Source: store.SourceUI, Actor: "t"})
	var ve *store.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Diagnostics[0].Message, "managed by git") {
		t.Fatalf("UI apply of a git-managed module: %v", err)
	}
	if _, err := s.Scale(ctx, "nope", 1, store.SourceCLI, "t"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("scale missing: %v", err)
	}
}

func TestAdminTokenIsGeneratedOnce(t *testing.T) {
	s := newStore(t)
	h1, tok, created, err := s.EnsureAdminToken(ctxT(t))
	if err != nil || !created || !strings.HasPrefix(tok, "mf_") || len(tok) < 40 {
		t.Fatalf("first: %q %v %v", tok, created, err)
	}
	h2, tok2, created2, err := s.EnsureAdminToken(ctxT(t))
	if err != nil || created2 || tok2 != "" || h1 != h2 {
		t.Fatalf("second: %q %v %v", tok2, created2, err)
	}
}
