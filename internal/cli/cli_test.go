package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const testdata = "../../testdata"

func run(t *testing.T, stdin string, getenv func(string) string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	var out, errOut bytes.Buffer
	code = Run(context.Background(), Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut, Getenv: getenv}, args)
	return code, out.String(), errOut.String()
}

func TestValidateValid(t *testing.T) {
	code, out, _ := run(t, "", nil, "validate", "-f", testdata+"/valid/hello-world.yaml")
	if code != ExitOK || out != "valid: 5 document(s), 5 resource(s) passed, 0 error(s), 0 warning(s)\n" {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestValidateInvalidDirectory(t *testing.T) {
	code, out, _ := run(t, "", nil, "validate", "-f", testdata+"/invalid")
	if code != ExitFailure || !strings.Contains(out, "routes.yaml:14:26: error: Pipeline/routes: spec.stages[1].routes[0].goto:") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestValidateStrictTurnsWarningsIntoFailure(t *testing.T) {
	args := []string{"validate", "-f", testdata + "/valid/geo.json"}
	if code, out, _ := run(t, "", nil, args...); code != ExitOK || !strings.Contains(out, "warning:") {
		t.Errorf("default: code %d\n%s", code, out)
	}
	if code, out, _ := run(t, "", nil, append(args, "--strict")...); code != ExitFailure || !strings.HasPrefix(lastLine(out), "invalid:") {
		t.Errorf("strict: code %d\n%s", code, out)
	}
}

func TestValidateStdinJSON(t *testing.T) {
	spec := "apiVersion: manufacture.dev/v1\nkind: Module\nmetadata: {name: m}\nspec: {image: 'x/m:1'}\n"
	code, out, _ := run(t, spec, nil, "validate", "-f", "-", "-o", "json")
	var report struct {
		Valid     bool `json:"valid"`
		Resources []struct {
			Kind, Name string
		} `json:"resources"`
		Diagnostics []any `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if code != ExitOK || !report.Valid || len(report.Resources) != 1 || report.Resources[0].Name != "m" || report.Diagnostics == nil {
		t.Errorf("code %d, report %+v", code, report)
	}
}

func TestUsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no command":      nil,
		"unknown command": {"deploy"},
		"no files":        {"validate"},
		"bad output":      {"validate", "-f", "x", "-o", "xml"},
		"stray argument":  {"validate", "-f", "x", "y"},
		"unknown flag":    {"validate", "--nope"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, _ := run(t, "", nil, args...); code != ExitUsage {
				t.Errorf("code %d, want %d", code, ExitUsage)
			}
		})
	}
	if code, out, _ := run(t, "", nil, "help"); code != ExitOK || !strings.Contains(out, "validate") {
		t.Errorf("help: %d %s", code, out)
	}
	if code, _, _ := run(t, "", nil, "validate", "-h"); code != ExitOK {
		t.Errorf("validate -h: %d", code)
	}
}

func TestMissingFileIsAFailure(t *testing.T) {
	code, out, _ := run(t, "", nil, "validate", "-f", testdata+"/does-not-exist.yaml")
	if code != ExitFailure || !strings.Contains(out, "no such file") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestConfigCommand(t *testing.T) {
	vars := map[string]string{
		"MF_DATABASE_URL": "postgres://u:hidden@db/mf", "MF_AMQP_URL": "amqp://u:hidden@mq/",
		"MF_S3_ENDPOINT": "s3:9000", "MF_S3_ACCESS_KEY": "ak", "MF_S3_SECRET_KEY": "hidden",
	}
	code, out, _ := run(t, "", func(k string) string { return vars[k] }, "config")
	if code != ExitOK || strings.Contains(out, "hidden") || !strings.Contains(out, `"database_url": "[redacted]"`) {
		t.Errorf("code %d\n%s", code, out)
	}
	if code, _, errOut := run(t, "", nil, "config"); code != ExitFailure || !strings.Contains(errOut, "MF_DATABASE_URL is required") {
		t.Errorf("missing config: code %d\n%s", code, errOut)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "", nil, "version", "-o", "json")
	var v struct{ Version, Contract string }
	if err := json.Unmarshal([]byte(out), &v); err != nil || code != ExitOK || v.Version == "" || v.Contract == "" {
		t.Errorf("code %d, %v\n%s", code, err, out)
	}
	if code, out, _ := run(t, "", nil, "version"); code != ExitOK || !strings.HasPrefix(out, "manufacture ") {
		t.Errorf("text: %d %s", code, out)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
