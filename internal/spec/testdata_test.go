package spec

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testdataDir = "../../testdata"

// want is an expected diagnostic declared in a testdata file as "# want: <path> | <substring>".
type want struct {
	path, substr string
}

func readWants(t *testing.T, file string) []want {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var wants []want
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "# want:")
		if !ok {
			continue
		}
		path, substr, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("%s: malformed want line %q", file, sc.Text())
		}
		wants = append(wants, want{strings.TrimSpace(path), strings.TrimSpace(substr)})
	}
	if len(wants) == 0 {
		t.Fatalf("%s: no '# want:' lines", file)
	}
	return wants
}

func specFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := expand(filepath.Join(testdataDir, dir))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func validateFile(t *testing.T, file string) Diagnostics {
	t.Helper()
	docs, diags := Load([]string{file}, nil)
	_, vd := Validate(docs, Options{})
	return append(diags, vd...)
}

func TestValidTestdata(t *testing.T) {
	for _, f := range specFiles(t, "valid") {
		t.Run(filepath.Base(f), func(t *testing.T) {
			for _, d := range validateFile(t, f) {
				if d.Severity == SeverityError {
					t.Errorf("unexpected error: %s", d)
				}
			}
		})
	}
}

// TestInvalidTestdata checks that every expected diagnostic is reported and that
// no unexpected error appears.
func TestInvalidTestdata(t *testing.T) {
	for _, group := range []string{"invalid/schema", "invalid/rules"} {
		for _, f := range specFiles(t, group) {
			t.Run(group+"/"+filepath.Base(f), func(t *testing.T) {
				wants := readWants(t, f)
				matched := make([]bool, len(wants))
				for _, d := range validateFile(t, f) {
					if d.Severity != SeverityError {
						continue
					}
					found := false
					for i, w := range wants {
						if d.Path == w.path && strings.Contains(d.Message, w.substr) {
							matched[i], found = true, true
						}
					}
					if !found {
						t.Errorf("unexpected error: %s", d)
					}
				}
				for i, w := range wants {
					if !matched[i] {
						t.Errorf("missing error at %q containing %q", w.path, w.substr)
					}
				}
			})
		}
	}
}

// TestSchemaLayerSplit keeps the testdata contract for SDKs honest: files under
// invalid/schema must fail the JSON Schema alone, files under invalid/rules must pass it.
func TestSchemaLayerSplit(t *testing.T) {
	for _, tc := range []struct {
		dir      string
		wantFail bool
	}{
		{"valid", false},
		{"invalid/schema", true},
		{"invalid/rules", false},
	} {
		for _, f := range specFiles(t, tc.dir) {
			docs, diags := Load([]string{f}, nil)
			if len(diags) > 0 {
				t.Fatalf("%s: %v", f, diags)
			}
			failed := false
			for _, d := range docs {
				if len(validateSchema(d)) > 0 {
					failed = true
				}
			}
			if failed != tc.wantFail {
				t.Errorf("%s: schema failure = %v, want %v", f, failed, tc.wantFail)
			}
		}
	}
}
