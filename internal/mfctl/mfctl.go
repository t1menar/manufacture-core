// Package mfctl implements the mfctl command: a thin client of the gateway API.
package mfctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/t1menar/manufacture-core/internal/spec"
)

// Exit codes (same as manufacture).
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// Env is what a command may touch outside of its arguments.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

type command struct {
	synopsis string
	summary  string
	run      func(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"apply":    {"-f <file|dir|-> [-f ...] [--dry-run] [-o text|json]", "create or update modules and pipelines", runApply},
		"get":      {"modules|pipelines|runs|revisions <kind>/<name> [-o table|json|yaml]", "list resources", runGet},
		"describe": {"module|pipeline <name>", "show one resource with its status and YAML", runDescribe},
		"delete":   {"module|pipeline <name>", "delete a resource (its revisions are kept)", runDelete},
		"scale":    {"module <name> --replicas N", "set a fixed replica count", runScale},
		"run":      {"<pipeline> [--data JSON]", "start a run", runRun},
		"logs":     {"module <name> [--tail N]", "print recent logs of the module replicas", runLogs},
	}
}

// Run executes the command line (without the program name).
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		w := env.Stderr
		code := ExitUsage
		if len(args) > 0 {
			w, code = env.Stdout, ExitOK
		}
		usage(w)
		return code
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(env.Stderr, "mfctl: unknown command %q\n\n", args[0])
		usage(env.Stderr)
		return ExitUsage
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(env.Stderr, "Usage: mfctl %s %s\n\nFlags:\n", args[0], cmd.synopsis)
		fs.PrintDefaults()
	}
	err := cmd.run(ctx, newClient(env.Getenv), env, fs, args[1:])
	var ue usageError
	var ae *APIError
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, flag.ErrHelp):
		return ExitOK
	case errors.As(err, &ue):
		if ue.msg != "" {
			fmt.Fprintf(env.Stderr, "mfctl %s: %s\n", args[0], ue.msg)
		}
		return ExitUsage
	case errors.As(err, &ae):
		fmt.Fprintf(env.Stderr, "mfctl %s: %v\n", args[0], ae)
		for _, d := range ae.Errors {
			fmt.Fprintf(env.Stderr, "  %s\n", d)
		}
		return ExitFailure
	default:
		fmt.Fprintf(env.Stderr, "mfctl %s: %v\n", args[0], err)
		return ExitFailure
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: mfctl <command> [args] [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-9s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Environment: %s (default %s), %s.\n", EnvURL, DefaultURL, EnvToken)
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// parse parses flags that may follow positional arguments ("get modules -o json").
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageError{}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// target reads "kind name" or "kind/name" with kind normalized to module or pipeline.
func target(pos []string) (kind, name string, err error) {
	if len(pos) == 0 {
		return "", "", usageError{"missing resource (module <name> or pipeline <name>)"}
	}
	k, n, ok := strings.Cut(pos[0], "/")
	rest := pos[1:]
	if !ok {
		if len(pos) < 2 {
			return "", "", usageError{"missing resource name"}
		}
		n, rest = pos[1], pos[2:]
	}
	if len(rest) > 0 {
		return "", "", usageError{fmt.Sprintf("unexpected arguments %q", rest)}
	}
	switch strings.ToLower(k) {
	case "module", "modules", "mod":
		k = "module"
	case "pipeline", "pipelines", "pipe":
		k = "pipeline"
	default:
		return "", "", usageError{fmt.Sprintf("unknown kind %q (module or pipeline)", k)}
	}
	return k, n, nil
}

func printYAML(w io.Writer, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func runApply(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	var files stringList
	fs.Var(&files, "f", "spec file, directory (recursive) or - for stdin; repeatable")
	dry := fs.Bool("dry-run", false, "validate on the server and show what would change")
	out := fs.String("o", "text", "output: text or json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError{fmt.Sprintf("unexpected arguments %q; pass files with -f", pos)}
	}
	if len(files) == 0 {
		return usageError{"at least one -f is required"}
	}
	inputs, diags := spec.ReadInputs(files, env.Stdin)
	if diags.Errors() > 0 {
		for _, d := range diags {
			fmt.Fprintln(env.Stderr, d)
		}
		return errors.New("cannot read the input")
	}
	type file struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	body := struct {
		Files []file `json:"files"`
	}{}
	for _, in := range inputs {
		body.Files = append(body.Files, file{Name: in.Name, Content: string(in.Data)})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	q := url.Values{"source": {"cli"}}
	if *dry {
		q.Set("dry_run", "true")
	}
	var res struct {
		Applied bool `json:"applied"`
		Results []struct {
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			Action   string `json:"action"`
			Revision int    `json:"revision"`
		} `json:"results"`
		Warnings spec.Diagnostics `json:"warnings"`
	}
	var raw json.RawMessage
	if err := c.do(ctx, "POST", "/api/v1/apply?"+q.Encode(), "application/json", b, &raw); err != nil {
		return err
	}
	if *out == "json" {
		_, err := env.Stdout.Write(raw)
		return err
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(env.Stderr, w)
	}
	suffix := ""
	if *dry {
		suffix = " (dry run)"
	}
	for _, r := range res.Results {
		fmt.Fprintf(env.Stdout, "%s/%s %s (revision %d)%s\n", strings.ToLower(r.Kind), r.Name, r.Action, r.Revision, suffix)
	}
	return nil
}

func outputFlag(fs *flag.FlagSet) *string {
	return fs.String("o", "table", "output: table, json or yaml")
}

func runGet(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	out := outputFlag(fs)
	pipeline := fs.String("p", "", "runs: only this pipeline")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usageError{"what to get: modules, pipelines, runs or revisions <kind>/<name>"}
	}
	var path string
	var cols []column
	switch strings.ToLower(pos[0]) {
	case "modules", "module", "mod":
		path, cols = "/api/v1/modules", moduleColumns
	case "pipelines", "pipeline", "pipe":
		path, cols = "/api/v1/pipelines", pipelineColumns
	case "runs", "run":
		q := url.Values{}
		if *pipeline != "" {
			q.Set("pipeline", *pipeline)
		}
		path, cols = "/api/v1/runs?"+q.Encode(), runColumns
	case "revisions", "revision", "rev":
		k, n, err := target(pos[1:])
		if err != nil {
			return err
		}
		path, cols = "/api/v1/"+k+"s/"+url.PathEscape(n)+"/revisions", revisionColumns
	default:
		return usageError{fmt.Sprintf("unknown resource %q", pos[0])}
	}
	var raw json.RawMessage
	if err := c.do(ctx, "GET", path, "", nil, &raw); err != nil {
		return err
	}
	return render(env.Stdout, *out, raw, cols)
}

func runDescribe(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	out := fs.String("o", "text", "output: text, json or yaml")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	k, n, err := target(pos)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.do(ctx, "GET", "/api/v1/"+k+"s/"+url.PathEscape(n), "", nil, &raw); err != nil {
		return err
	}
	switch *out {
	case "json":
		_, err := env.Stdout.Write(raw)
		return err
	case "yaml":
		return printYAML(env.Stdout, raw)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		return err
	}
	y, _ := d["yaml"].(string)
	delete(d, "yaml")
	delete(d, "spec")
	b, err := yaml.Marshal(d)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "%s\n--- spec (revision %v)\n%s", strings.TrimRight(string(b), "\n"), d["revision"], y)
	return nil
}

func runDelete(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	k, n, err := target(pos)
	if err != nil {
		return err
	}
	if err := c.do(ctx, "DELETE", "/api/v1/"+k+"s/"+url.PathEscape(n), "", nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "%s/%s deleted\n", k, n)
	return nil
}

func runScale(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	replicas := fs.Int("replicas", -1, "replica count (required)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	k, n, err := target(pos)
	if err != nil {
		return err
	}
	if k != "module" || *replicas < 0 {
		return usageError{"usage: mfctl scale module <name> --replicas N"}
	}
	b, _ := json.Marshal(map[string]int{"replicas": *replicas})
	var st struct {
		Revision int `json:"revision"`
	}
	if err := c.do(ctx, "POST", "/api/v1/modules/"+url.PathEscape(n)+"/scale?source=cli", "application/json", b, &st); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "module/%s scaled to %d (revision %d)\n", n, *replicas, st.Revision)
	return nil
}

func runRun(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	data := fs.String("data", "", "initial run data as a JSON object; without it a source-triggered pipeline asks its source module")
	out := fs.String("o", "text", "output: text or json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError{"usage: mfctl run <pipeline> [--data JSON]"}
	}
	body := []byte("{}")
	if *data != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(*data), &obj); err != nil {
			return usageError{"--data must be a JSON object: " + err.Error()}
		}
		body, _ = json.Marshal(map[string]any{"data": obj})
	}
	var raw json.RawMessage
	if err := c.do(ctx, "POST", "/api/v1/pipelines/"+url.PathEscape(pos[0])+"/runs", "application/json", body, &raw); err != nil {
		return err
	}
	if *out == "json" {
		_, err := env.Stdout.Write(raw)
		return err
	}
	var r struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		TriggerID string `json:"trigger_id"`
	}
	_ = json.Unmarshal(raw, &r)
	if r.TriggerID != "" {
		fmt.Fprintf(env.Stdout, "pipeline/%s: source triggered (trigger %s)\n", pos[0], r.TriggerID)
		return nil
	}
	fmt.Fprintf(env.Stdout, "run %s %s\n", r.ID, r.Status)
	return nil
}

func runLogs(ctx context.Context, c *client, env Env, fs *flag.FlagSet, args []string) error {
	tail := fs.Int("tail", 200, "number of lines")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	k, n, err := target(pos)
	if err != nil {
		return err
	}
	if k != "module" {
		return usageError{"logs are available for modules"}
	}
	var raw json.RawMessage
	path := fmt.Sprintf("/api/v1/modules/%s/logs?tail=%d", url.PathEscape(n), *tail)
	if err := c.do(ctx, "GET", path, "", nil, &raw); err != nil {
		return err
	}
	_, err = env.Stdout.Write(raw)
	return err
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
