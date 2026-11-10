package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"

	"github.com/t1menar/manufacture-core/internal/db"
	"github.com/t1menar/manufacture-core/internal/spec"
)

// AnnotationManagedBy marks a resource whose source of truth is elsewhere (for
// example "git"); the UI may not change it.
const AnnotationManagedBy = "manufacture.dev/managed-by"

// Actions of an apply.
const (
	ActionCreated   = "created"
	ActionUpdated   = "updated"
	ActionUnchanged = "unchanged"
)

// File is one input of an apply.
type File struct {
	Name    string
	Content []byte
}

// ApplyRequest is a set of files with Module and Pipeline documents.
type ApplyRequest struct {
	Files  []File
	DryRun bool
	Source Source
	Actor  string
}

// ApplyItem is the outcome for one resource.
type ApplyItem struct {
	Kind     spec.Kind `json:"kind"`
	Name     string    `json:"name"`
	Action   string    `json:"action"`
	Revision int       `json:"revision"`
}

// ApplyResult is the outcome of an apply without errors.
type ApplyResult struct {
	Applied  bool             `json:"applied"`
	Results  []ApplyItem      `json:"results"`
	Warnings spec.Diagnostics `json:"warnings,omitempty"`
}

// Revision is one stored revision of a resource.
type Revision struct {
	Kind      spec.Kind       `json:"kind"`
	Name      string          `json:"name"`
	Revision  int             `json:"revision"`
	Spec      json.RawMessage `json:"spec"`
	YAML      string          `json:"yaml"`
	SpecHash  string          `json:"spec_hash"`
	AppliedAt time.Time       `json:"applied_at"`
	AppliedBy string          `json:"applied_by"`
	Source    Source          `json:"source"`
	Current   bool            `json:"current"`
}

// ManagedBy returns the managed-by annotation of the revision, if any.
func (r Revision) ManagedBy() string {
	var v struct {
		Metadata spec.ObjectMeta `json:"metadata"`
	}
	_ = json.Unmarshal(r.Spec, &v)
	return v.Metadata.Annotations[AnnotationManagedBy]
}

// Module decodes the revision of a Module.
func (r Revision) Module() (*spec.Module, error) {
	var m spec.Module
	if err := json.Unmarshal(r.Spec, &m); err != nil {
		return nil, fmt.Errorf("decode module %s revision %d: %w", r.Name, r.Revision, err)
	}
	return &m, nil
}

// Pipeline decodes the revision of a Pipeline.
func (r Revision) Pipeline() (*spec.Pipeline, error) {
	var p spec.Pipeline
	if err := json.Unmarshal(r.Spec, &p); err != nil {
		return nil, fmt.Errorf("decode pipeline %s revision %d: %w", r.Name, r.Revision, err)
	}
	return &p, nil
}

// Apply validates all documents and stores a new revision for every resource that
// changed. Nothing is stored when any document has an error (*ValidationError) or
// when DryRun is set. A resource identical to its current revision is "unchanged".
func (s *Store) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	if !req.Source.Valid() {
		return ApplyResult{}, fmt.Errorf("unknown source %q", req.Source)
	}
	var (
		docs  []*spec.Document
		diags spec.Diagnostics
	)
	for _, f := range req.Files {
		d, ds := spec.Parse(f.Name, f.Content)
		docs = append(docs, d...)
		diags = append(diags, ds...)
	}
	var res ApplyResult
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockResources(ctx, tx); err != nil {
			return err
		}
		known, err := names(ctx, tx, spec.KindModule)
		if err != nil {
			return err
		}
		set, vds := spec.Validate(docs, spec.Options{RequireReferences: true, KnownModules: known})
		diags = append(diags, vds...)
		if diags.Errors() == 0 && len(set.Resources) == 0 {
			diags = append(diags, spec.Diagnostic{Severity: spec.SeverityError, Source: "request",
				Message: "the input contains no Module or Pipeline documents"})
		}
		if diags.Errors() == 0 {
			diags = append(diags, s.checkManaged(ctx, tx, set.Resources, req.Source)...)
		}
		if diags.Errors() > 0 {
			diags.Sort()
			return &ValidationError{Diagnostics: diags}
		}
		res.Warnings = diags
		for _, r := range set.Resources {
			y, err := r.Doc.YAML()
			if err != nil {
				return fmt.Errorf("render %s/%s: %w", r.Kind, r.Name, err)
			}
			item, err := writeRevision(ctx, tx, r.Kind, r.Name, r.Object(), y, req.Source, req.Actor)
			if err != nil {
				return err
			}
			res.Results = append(res.Results, item)
		}
		if req.DryRun {
			return errDryRun
		}
		res.Applied = true
		for _, it := range res.Results {
			if it.Action == ActionUnchanged {
				continue
			}
			if err := audit(ctx, tx, req.Actor, "apply", string(it.Kind)+"/"+it.Name,
				map[string]any{"action": it.Action, "revision": it.Revision, "source": req.Source}); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return ApplyResult{}, err
	}
	return res, nil
}

var errDryRun = errors.New("dry run")

// checkManaged refuses UI changes to resources whose source of truth is elsewhere.
func (s *Store) checkManaged(ctx context.Context, tx pgx.Tx, rs []spec.Resource, src Source) spec.Diagnostics {
	if src != SourceUI {
		return nil
	}
	var diags spec.Diagnostics
	for _, r := range rs {
		cur, err := current(ctx, tx, r.Kind, r.Name)
		if err != nil {
			continue // new resource or read error; the write reports real errors
		}
		if m := cur.ManagedBy(); m != "" {
			diags = append(diags, r.Doc.ErrorAt(spec.Path{"metadata", "annotations"},
				fmt.Sprintf("%s/%s is managed by %s; change it there", r.Kind, r.Name, m)))
		}
	}
	return diags
}

// writeRevision stores obj as the next revision unless it equals the current one.
func writeRevision(ctx context.Context, tx pgx.Tx, kind spec.Kind, name string, obj any, yamlText []byte,
	src Source, actor string,
) (ApplyItem, error) {
	js, err := json.Marshal(obj)
	if err != nil {
		return ApplyItem{}, fmt.Errorf("encode %s/%s: %w", kind, name, err)
	}
	sum := sha256.Sum256(js)
	hash := hex.EncodeToString(sum[:])
	item := ApplyItem{Kind: kind, Name: name}

	var curRev int
	var curHash string
	err = tx.QueryRow(ctx, `SELECT r.current_revision, rr.spec_hash FROM resources r
		JOIN resource_revisions rr ON rr.kind = r.kind AND rr.name = r.name AND rr.revision = r.current_revision
		WHERE r.kind = $1 AND r.name = $2`, kind, name).Scan(&curRev, &curHash)
	switch {
	case err == nil && curHash == hash:
		item.Action, item.Revision = ActionUnchanged, curRev
		return item, nil
	case err == nil:
		item.Action = ActionUpdated
	case errors.Is(err, pgx.ErrNoRows):
		item.Action = ActionCreated
	default:
		return ApplyItem{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision), 0) + 1 FROM resource_revisions
		WHERE kind = $1 AND name = $2`, kind, name).Scan(&item.Revision); err != nil {
		return ApplyItem{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO resource_revisions (kind, name, revision, spec, yaml, spec_hash, applied_by, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, kind, name, item.Revision, js, string(yamlText), hash, actor, src); err != nil {
		return ApplyItem{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO resources (kind, name, current_revision) VALUES ($1, $2, $3)
		ON CONFLICT (kind, name) DO UPDATE SET current_revision = EXCLUDED.current_revision, updated_at = now()`,
		kind, name, item.Revision); err != nil {
		return ApplyItem{}, err
	}
	return item, nil
}

func names(ctx context.Context, tx pgx.Tx, kind spec.Kind) (map[string]bool, error) {
	rows, err := tx.Query(ctx, "SELECT name FROM resources WHERE kind = $1", kind)
	if err != nil {
		return nil, err
	}
	ns, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(ns))
	for _, n := range ns {
		m[n] = true
	}
	return m, nil
}

const revisionColumns = `rr.kind, rr.name, rr.revision, rr.spec, rr.yaml, rr.spec_hash, rr.applied_at, rr.applied_by, rr.source`

func scanRevision(row pgx.CollectableRow) (Revision, error) {
	var r Revision
	var doc []byte
	err := row.Scan(&r.Kind, &r.Name, &r.Revision, &doc, &r.YAML, &r.SpecHash, &r.AppliedAt, &r.AppliedBy, &r.Source, &r.Current)
	r.Spec = doc
	return r, err
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func current(ctx context.Context, q querier, kind spec.Kind, name string) (Revision, error) {
	rows, err := q.Query(ctx, `SELECT `+revisionColumns+`, true FROM resources r
		JOIN resource_revisions rr ON rr.kind = r.kind AND rr.name = r.name AND rr.revision = r.current_revision
		WHERE r.kind = $1 AND r.name = $2`, kind, name)
	if err != nil {
		return Revision{}, err
	}
	r, err := pgx.CollectExactlyOneRow(rows, scanRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, fmt.Errorf("%s %q: %w", kind, name, ErrNotFound)
	}
	return r, err
}

// Get returns the current revision of a resource.
func (s *Store) Get(ctx context.Context, kind spec.Kind, name string) (Revision, error) {
	return current(ctx, s.pool, kind, name)
}

// List returns the current revisions of all resources of a kind, by name.
func (s *Store) List(ctx context.Context, kind spec.Kind) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+revisionColumns+`, true FROM resources r
		JOIN resource_revisions rr ON rr.kind = r.kind AND rr.name = r.name AND rr.revision = r.current_revision
		WHERE r.kind = $1 ORDER BY r.name`, kind)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanRevision)
}

// Revisions returns the history of an existing resource, newest first.
func (s *Store) Revisions(ctx context.Context, kind spec.Kind, name string) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+revisionColumns+`, rr.revision = r.current_revision FROM resources r
		JOIN resource_revisions rr ON rr.kind = r.kind AND rr.name = r.name
		WHERE r.kind = $1 AND r.name = $2 ORDER BY rr.revision DESC`, kind, name)
	if err != nil {
		return nil, err
	}
	revs, err := pgx.CollectRows(rows, scanRevision)
	if err == nil && len(revs) == 0 {
		err = fmt.Errorf("%s %q: %w", kind, name, ErrNotFound)
	}
	return revs, err
}

// RevisionAt returns one revision of an existing resource.
func (s *Store) RevisionAt(ctx context.Context, kind spec.Kind, name string, revision int) (Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+revisionColumns+`, rr.revision = r.current_revision FROM resources r
		JOIN resource_revisions rr ON rr.kind = r.kind AND rr.name = r.name
		WHERE r.kind = $1 AND r.name = $2 AND rr.revision = $3`, kind, name, revision)
	if err != nil {
		return Revision{}, err
	}
	r, err := pgx.CollectExactlyOneRow(rows, scanRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, fmt.Errorf("%s %q revision %d: %w", kind, name, revision, ErrNotFound)
	}
	return r, err
}

// Delete removes a resource; its revisions are kept. A module that a current
// pipeline revision uses cannot be deleted. busy, when not nil, may refuse the
// deletion of a pipeline (for example while it has active runs).
func (s *Store) Delete(ctx context.Context, kind spec.Kind, name, actor string, busy func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockResources(ctx, tx); err != nil {
			return err
		}
		cur, err := current(ctx, tx, kind, name)
		if err != nil {
			return err
		}
		if kind == spec.KindModule {
			users, err := pipelinesUsing(ctx, tx, name)
			if err != nil {
				return err
			}
			if len(users) > 0 {
				return &ConflictError{Reason: fmt.Sprintf("module %q is used by pipeline(s) %v", name, users)}
			}
		}
		if busy != nil {
			if err := busy(ctx, tx); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM resources WHERE kind = $1 AND name = $2", kind, name); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "delete", string(kind)+"/"+name, map[string]any{"revision": cur.Revision})
	})
}

// pipelinesUsing lists pipelines whose current revision references the module.
func pipelinesUsing(ctx context.Context, tx pgx.Tx, module string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT `+revisionColumns+`, true FROM resources r
		JOIN resource_revisions rr ON rr.kind = r.kind AND rr.name = r.name AND rr.revision = r.current_revision
		WHERE r.kind = 'Pipeline' ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	revs, err := pgx.CollectRows(rows, scanRevision)
	if err != nil {
		return nil, err
	}
	var users []string
	for _, rev := range revs {
		p, err := rev.Pipeline()
		if err != nil {
			return nil, err
		}
		if slices.Contains(ModulesOf(p), module) {
			users = append(users, rev.Name)
		}
	}
	return users, nil
}

// ModulesOf returns the modules a pipeline references (trigger source and steps), without duplicates.
func ModulesOf(p *spec.Pipeline) []string {
	var ms []string
	add := func(m string) {
		if m != "" && !slices.Contains(ms, m) {
			ms = append(ms, m)
		}
	}
	if p.Spec.Trigger != nil {
		add(p.Spec.Trigger.Source)
	}
	for _, st := range p.Spec.Stages {
		for _, step := range st.Steps() {
			add(step.Module)
		}
	}
	return ms
}

// Scale stores a new module revision with a fixed replica count.
func (s *Store) Scale(ctx context.Context, name string, replicas int, src Source, actor string) (Revision, error) {
	var out Revision
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockResources(ctx, tx); err != nil {
			return err
		}
		cur, err := current(ctx, tx, spec.KindModule, name)
		if err != nil {
			return err
		}
		if m := cur.ManagedBy(); m != "" && src == SourceUI {
			return &ConflictError{Reason: fmt.Sprintf("module %q is managed by %s; change it there", name, m)}
		}
		m, err := cur.Module()
		if err != nil {
			return err
		}
		if m.Spec.EffectiveRuntime() == spec.RuntimeExternal {
			return &ConflictError{Reason: fmt.Sprintf("module %q runs outside of the core (runtime: external) and cannot be scaled here", name)}
		}
		r := spec.Fixed(replicas)
		m.Spec.Replicas = &r
		y, err := toYAML(m)
		if err != nil {
			return err
		}
		item, err := writeRevision(ctx, tx, spec.KindModule, name, m, y, src, actor)
		if err != nil {
			return err
		}
		if item.Action != ActionUnchanged {
			if err := audit(ctx, tx, actor, "scale", "Module/"+name,
				map[string]any{"replicas": replicas, "revision": item.Revision, "source": src}); err != nil {
				return err
			}
		}
		out, err = current(ctx, tx, spec.KindModule, name)
		return err
	})
	return out, err
}

// toYAML renders a typed resource as YAML (keys sorted, no comments).
func toYAML(obj any) ([]byte, error) {
	js, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(js, &v); err != nil {
		return nil, err
	}
	return yaml.Marshal(v)
}
