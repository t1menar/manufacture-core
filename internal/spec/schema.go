package spec

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/t1menar/manufacture-core/schemas"
)

// schemaFiles maps a resource kind to its schema file in package schemas.
var schemaFiles = map[Kind]string{
	KindModule:   "v1/module.schema.json",
	KindPipeline: "v1/pipeline.schema.json",
}

// friendlyMessages replace generic pattern errors for well-known definitions,
// keyed by the suffix of the failing keyword location.
var friendlyMessages = map[string]string{
	"common.schema.json#/$defs/name/pattern":     "must be a lowercase DNS label: a-z, 0-9 and '-', starting and ending with a letter or digit",
	"common.schema.json#/$defs/duration/pattern": "must be a duration such as 500ms, 30s, 5m, 1h30m or 7d",
	"common.schema.json#/$defs/envName/pattern":  "must be an environment variable name: letters, digits and '_', not starting with a digit",
}

type compiledSchemas map[Kind]*jsonschema.Schema

var loadSchemas = sync.OnceValues(func() (compiledSchemas, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	fsys := schemas.FS()
	files, err := fs.Glob(fsys, "v1/*.json")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", f, err)
		}
		if err := c.AddResource(schemas.BaseURL+f, doc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", f, err)
		}
	}
	out := compiledSchemas{}
	for k, f := range schemaFiles {
		s, err := c.Compile(schemas.BaseURL + f)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", f, err)
		}
		out[k] = s
	}
	return out, nil
})

// validateSchema checks the document against the schema of its kind.
func validateSchema(d *Document) Diagnostics {
	apiVersion, k, _ := d.header()
	switch {
	case apiVersion != APIVersion:
		return Diagnostics{d.errorAt(Path{"apiVersion"}, fmt.Sprintf("apiVersion must be %q, got %q", APIVersion, apiVersion))}
	case schemaFiles[k] == "":
		return Diagnostics{d.errorAt(Path{"kind"}, fmt.Sprintf("unknown kind %q: expected one of %s", k, strings.Join(knownKinds(), ", ")))}
	}
	compiled, err := loadSchemas()
	if err != nil { // the schemas are embedded, so this is a build defect
		panic(err)
	}
	err = compiled[k].Validate(d.value)
	if err == nil {
		return nil
	}
	verr, ok := err.(*jsonschema.ValidationError) //nolint:errorlint // Validate returns the concrete type unwrapped.
	if !ok {
		return Diagnostics{d.errorAt(nil, err.Error())}
	}
	return schemaDiagnostics(d, verr)
}

func knownKinds() []string {
	ks := make([]string, 0, len(schemaFiles))
	for k := range schemaFiles {
		ks = append(ks, string(k))
	}
	slices.Sort(ks)
	return ks
}

var printer = message.NewPrinter(language.English)

// schemaDiagnostics turns the leaves of the error tree into diagnostics; inner
// nodes only say "validation failed" and would add noise.
func schemaDiagnostics(d *Document, root *jsonschema.ValidationError) Diagnostics {
	var out Diagnostics
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		p := Path(e.InstanceLocation)
		msg := e.ErrorKind.LocalizedString(printer)
		switch ek := e.ErrorKind.(type) {
		case *kind.AdditionalProperties:
			for _, prop := range ek.Properties {
				out = appendUnique(out, seen, d.errorAt(p.Key(prop), fmt.Sprintf("unknown field %q", prop)))
			}
			return
		case *kind.Pattern:
			if friendly := friendlyMessage(e.SchemaURL, ek.KeywordPath()); friendly != "" {
				msg = friendly
			}
		}
		out = appendUnique(out, seen, d.errorAt(p, msg))
	}
	walk(root)
	return out
}

// leafMessages flattens an error tree into "at '<pointer>': <message>" strings.
func leafMessages(root *jsonschema.ValidationError) []string {
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, fmt.Sprintf("at '/%s': %s", strings.Join(e.InstanceLocation, "/"), e.ErrorKind.LocalizedString(printer)))
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(root)
	return out
}

func friendlyMessage(schemaURL string, keywordPath []string) string {
	loc := schemaURL + "/" + strings.Join(keywordPath, "/")
	for suffix, msg := range friendlyMessages {
		if strings.HasSuffix(loc, suffix) {
			return msg
		}
	}
	return ""
}

func appendUnique(ds Diagnostics, seen map[string]bool, d Diagnostic) Diagnostics {
	key := d.Path + "\x00" + d.Message
	if seen[key] {
		return ds
	}
	seen[key] = true
	return append(ds, d)
}
