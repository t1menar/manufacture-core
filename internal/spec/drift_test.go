package spec

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/t1menar/manufacture-core/schemas"
)

// TestGoTypesMatchSchemas fails when a field exists in a Go type but not in the
// JSON Schema (or the other way round). Strict decoding would otherwise only
// catch the drift for fields that appear in testdata.
func TestGoTypesMatchSchemas(t *testing.T) {
	for _, tc := range []struct {
		typ     any
		file    string
		pointer string
	}{
		{Module{}, "module", ""},
		{ObjectMeta{}, "common", "/$defs/metadata"},
		{ModuleSpec{}, "module", "/$defs/moduleSpec"},
		{replicasRange{}, "module", "/$defs/moduleSpec/properties/replicas"},
		{Resources{}, "module", "/$defs/moduleSpec/properties/resources"},
		{SecretEnv{}, "module", "/$defs/moduleSpec/properties/secrets/items"},
		{SecretRef{}, "module", "/$defs/moduleSpec/properties/secrets/items/properties/secretRef"},
		{Pipeline{}, "pipeline", ""},
		{PipelineSpec{}, "pipeline", "/$defs/pipelineSpec"},
		{Trigger{}, "pipeline", "/$defs/pipelineSpec/properties/trigger"},
		{Concurrency{}, "pipeline", "/$defs/pipelineSpec/properties/concurrency"},
		{ContextSpec{}, "pipeline", "/$defs/pipelineSpec/properties/context"},
		{BlobsSpec{}, "pipeline", "/$defs/pipelineSpec/properties/blobs"},
		{StepPolicy{}, "pipeline", "/$defs/pipelineSpec/properties/defaults"},
		{Stage{}, "pipeline", "/$defs/stage"},
		{Step{}, "pipeline", "/$defs/step"},
		{Retry{}, "pipeline", "/$defs/retry"},
		{Join{}, "pipeline", "/$defs/join"},
		{Route{}, "pipeline", "/$defs/route"},
	} {
		name := reflect.TypeOf(tc.typ).Name()
		t.Run(name, func(t *testing.T) {
			schemaProps := propertiesAt(t, tc.file, tc.pointer)
			goProps := jsonFields(reflect.TypeOf(tc.typ))
			if !slices.Equal(schemaProps, goProps) {
				t.Errorf("%s fields %v, schema %s.schema.json#%s properties %v", name, goProps, tc.file, tc.pointer, schemaProps)
			}
		})
	}
}

func propertiesAt(t *testing.T, file, pointer string) []string {
	t.Helper()
	b, err := fs.ReadFile(schemas.FS(), "v1/"+file+".schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var node any
	if err := json.Unmarshal(b, &node); err != nil {
		t.Fatal(err)
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		if seg == "" {
			continue
		}
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("pointer %s: %q is not an object", pointer, seg)
		}
		node = m[seg]
	}
	m, _ := node.(map[string]any)
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties at %s#%s", file, pointer)
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// jsonFields lists the JSON names of a struct, flattening embedded structs as encoding/json does.
func jsonFields(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		if f.Anonymous {
			out = append(out, jsonFields(f.Type)...)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.IsExported() && name != "-" && name != "" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
