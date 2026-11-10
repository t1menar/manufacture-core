package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Options tune validation.
type Options struct {
	// RequireReferences reports references to modules that are not part of the
	// validated input as errors. By default they are warnings, because the module
	// may already be applied to the core.
	RequireReferences bool
	// KnownModules are modules that exist outside of the input (already applied
	// to the core). References to them are never reported.
	KnownModules map[string]bool
}

// Set is a collection of resources that passed validation.
type Set struct {
	Modules   []*Module
	Pipelines []*Pipeline
	// Resources lists every valid resource in input order, with its document.
	Resources []Resource
}

// Resource is one valid resource and the document it came from.
type Resource struct {
	Kind     Kind
	Name     string
	Doc      *Document
	Module   *Module   // set when Kind is Module
	Pipeline *Pipeline // set when Kind is Pipeline
}

// Object returns the typed resource.
func (r Resource) Object() any {
	if r.Module != nil {
		return r.Module
	}
	return r.Pipeline
}

// Annotations returns the metadata annotations of the resource.
func (r Resource) Annotations() map[string]string {
	if r.Module != nil {
		return r.Module.Metadata.Annotations
	}
	return r.Pipeline.Metadata.Annotations
}

// Len returns the number of resources in the set.
func (s *Set) Len() int { return len(s.Modules) + len(s.Pipelines) }

// entry keeps a decoded resource together with the document it came from.
type entry struct {
	doc      *Document
	kind     Kind
	name     string
	module   *Module
	pipeline *Pipeline
}

// Validate checks the documents and returns the resources that have no errors.
// Defaults that other rules rely on are filled in place: a step without an id
// gets the stage name (single and foreach stages) or the module name (parallel stages).
func Validate(docs []*Document, opts Options) (*Set, Diagnostics) {
	var (
		diags   Diagnostics
		entries []entry
	)
	for _, d := range docs {
		e, ds := validateDocument(d)
		diags = append(diags, ds...)
		if e != nil {
			entries = append(entries, *e)
		}
	}
	diags = append(diags, checkSet(entries, opts)...)
	diags.Sort()

	set := &Set{}
	for _, e := range entries {
		if e.module != nil || e.pipeline != nil {
			set.Resources = append(set.Resources, Resource{Kind: e.kind, Name: e.name, Doc: e.doc, Module: e.module, Pipeline: e.pipeline})
		}
		switch {
		case e.module != nil:
			set.Modules = append(set.Modules, e.module)
		case e.pipeline != nil:
			set.Pipelines = append(set.Pipelines, e.pipeline)
		}
	}
	return set, diags
}

// validateDocument returns nil when the document has errors.
func validateDocument(d *Document) (*entry, Diagnostics) {
	if ds := validateSchema(d); len(ds) > 0 {
		return nil, ds
	}
	_, kind, name := d.header()
	r := &reporter{doc: d}
	e := &entry{doc: d, kind: kind, name: name}
	switch kind {
	case KindModule:
		var m Module
		if err := decodeStrict(d.json, &m); err != nil {
			return nil, Diagnostics{d.errorAt(nil, decodeMessage(err))}
		}
		checkModule(r, &m)
		e.module = &m
	case KindPipeline:
		var p Pipeline
		if err := decodeStrict(d.json, &p); err != nil {
			return nil, Diagnostics{d.errorAt(nil, decodeMessage(err))}
		}
		checkPipeline(r, &p)
		e.pipeline = &p
	}
	if r.diags.Errors() > 0 {
		return nil, r.diags
	}
	return e, r.diags
}

func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// decodeMessage explains a decoding failure after a successful schema check:
// it means the Go types and the JSON Schema disagree.
func decodeMessage(err error) string {
	return fmt.Sprintf("document matches the schema but cannot be decoded (schema and Go types disagree, please report): %v", err)
}

// checkSet applies rules that span resources: unique names and references.
func checkSet(entries []entry, opts Options) Diagnostics {
	var diags Diagnostics
	modules := map[string]bool{}
	seen := map[string]*Document{}
	for _, e := range entries {
		key := string(e.kind) + "/" + e.name
		if first, dup := seen[key]; dup {
			diags = append(diags, e.doc.errorAt(Path{"metadata", "name"},
				fmt.Sprintf("%s is defined twice (first in %s)", key, first.Source)))
			continue
		}
		seen[key] = e.doc
		if e.kind == KindModule {
			modules[e.name] = true
		}
	}
	sev := SeverityWarning
	if opts.RequireReferences {
		sev = SeverityError
	}
	missing := func(e entry, p Path, name string) {
		if !modules[name] && !opts.KnownModules[name] {
			diags = append(diags, e.doc.diagnostic(sev, p,
				fmt.Sprintf("module %q is not defined in the input; it must exist in the core when the pipeline is applied", name)))
		}
	}
	for _, e := range entries {
		if e.pipeline == nil {
			continue
		}
		spec := e.pipeline.Spec
		if spec.Trigger != nil && spec.Trigger.Source != "" {
			missing(e, Path{"spec", "trigger", "source"}, spec.Trigger.Source)
		}
		for i, st := range spec.Stages {
			sp := Path{"spec", "stages"}.Index(i)
			forEachStep(st, sp, func(s *Step, p Path) { missing(e, p.Key("module"), s.Module) })
		}
	}
	return diags
}

// forEachStep calls fn for every step of the stage with the step path.
func forEachStep(st Stage, stagePath Path, fn func(s *Step, p Path)) {
	if st.Step != nil {
		fn(st.Step, stagePath.Key("step"))
	}
	for i := range st.Parallel {
		fn(&st.Parallel[i], stagePath.Key("parallel").Index(i))
	}
}

// reporter collects diagnostics for one document.
type reporter struct {
	doc   *Document
	diags Diagnostics
}

func (r *reporter) errorf(p Path, format string, args ...any) {
	r.diags = append(r.diags, r.doc.diagnostic(SeverityError, p, fmt.Sprintf(format, args...)))
}

func (r *reporter) warnf(p Path, format string, args ...any) {
	r.diags = append(r.diags, r.doc.diagnostic(SeverityWarning, p, fmt.Sprintf(format, args...)))
}
