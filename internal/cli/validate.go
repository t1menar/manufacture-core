package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/t1menar/manufacture-core/internal/spec"
)

type validateReport struct {
	Valid       bool             `json:"valid"`
	Resources   []resourceRef    `json:"resources"`
	Diagnostics spec.Diagnostics `json:"diagnostics"`
}

type resourceRef struct {
	Kind spec.Kind `json:"kind"`
	Name string    `json:"name"`
}

func runValidate(_ context.Context, env Env, args []string) int {
	fs := newFlagSet("validate", "-f <file|dir|-> [-f ...] [flags]", env.Stderr)
	var files stringList
	fs.Var(&files, "f", "spec file, directory (recursive) or - for stdin; repeatable")
	strict := fs.Bool("strict", false, "treat warnings, including references to modules missing from the input, as errors")
	output := fs.String("o", "text", "output format: text or json")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	switch {
	case len(files) == 0:
		fmt.Fprintln(env.Stderr, "manufacture validate: at least one -f is required")
		fs.Usage()
		return ExitUsage
	case fs.NArg() > 0:
		fmt.Fprintf(env.Stderr, "manufacture validate: unexpected arguments %q; pass files with -f\n", fs.Args())
		return ExitUsage
	case *output != "text" && *output != "json":
		fmt.Fprintf(env.Stderr, "manufacture validate: -o must be text or json, got %q\n", *output)
		return ExitUsage
	}

	docs, diags := spec.Load(files, env.Stdin)
	set, vdiags := spec.Validate(docs, spec.Options{RequireReferences: *strict})
	diags = append(diags, vdiags...)
	diags.Sort()

	valid := diags.Errors() == 0 && (!*strict || diags.Warnings() == 0)
	report := validateReport{Valid: valid, Resources: refs(set), Diagnostics: diags}
	if report.Diagnostics == nil {
		report.Diagnostics = spec.Diagnostics{}
	}
	var err error
	if *output == "json" {
		err = writeJSON(env.Stdout, report)
	} else {
		err = writeText(env.Stdout, report, len(docs))
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "manufacture validate: %v\n", err)
		return ExitFailure
	}
	if !valid {
		return ExitFailure
	}
	return ExitOK
}

func refs(set *spec.Set) []resourceRef {
	out := make([]resourceRef, 0, set.Len())
	for _, m := range set.Modules {
		out = append(out, resourceRef{Kind: spec.KindModule, Name: m.Metadata.Name})
	}
	for _, p := range set.Pipelines {
		out = append(out, resourceRef{Kind: spec.KindPipeline, Name: p.Metadata.Name})
	}
	return out
}

func writeText(w io.Writer, r validateReport, documents int) error {
	for _, d := range r.Diagnostics {
		if _, err := fmt.Fprintln(w, d.String()); err != nil {
			return err
		}
	}
	verdict := "valid"
	if !r.Valid {
		verdict = "invalid"
	}
	_, err := fmt.Fprintf(w, "%s: %d document(s), %d resource(s) passed, %d error(s), %d warning(s)\n",
		verdict, documents, len(r.Resources), r.Diagnostics.Errors(), r.Diagnostics.Warnings())
	return err
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
