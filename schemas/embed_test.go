package schemas

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"testing"
)

func TestVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version()) {
		t.Errorf("VERSION %q is not MAJOR.MINOR.PATCH", Version())
	}
}

func TestSchemaIDsMatchPaths(t *testing.T) {
	files, err := fs.Glob(FS(), "v1/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no schemas: %v", err)
	}
	for _, f := range files {
		b, err := fs.ReadFile(FS(), f)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Schema string `json:"$schema"`
			ID     string `json:"$id"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if doc.ID != BaseURL+f {
			t.Errorf("%s: $id %q, want %q", f, doc.ID, BaseURL+f)
		}
		if doc.Schema != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: $schema %q, want draft 2020-12", f, doc.Schema)
		}
	}
}
