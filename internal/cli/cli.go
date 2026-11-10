// Package cli implements the manufacture command line: argument parsing, output
// formatting and exit codes. Commands get their streams and environment injected,
// so they are tested without a subprocess.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitFailure = 1 // the command ran and found a problem (invalid spec, bad config)
	ExitUsage   = 2 // the command line itself is wrong
)

// Env is what a command may touch outside of its arguments.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

type command struct {
	summary string
	run     func(ctx context.Context, env Env, args []string) int
}

var commands = map[string]command{
	"validate": {"check Module and Pipeline specs without a running core", runValidate},
	"config":   {"print the effective configuration (secrets redacted)", runConfig},
	"version":  {"print version information", runVersion},
}

// Run executes the command line (without the program name) and returns the exit code.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		usage(env.Stderr)
		return ExitUsage
	}
	name, rest := args[0], args[1:]
	switch name {
	case "help", "-h", "-help", "--help":
		usage(env.Stdout)
		return ExitOK
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(env.Stderr, "manufacture: unknown command %q\n\n", name)
		usage(env.Stderr)
		return ExitUsage
	}
	return cmd.run(ctx, env, rest)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: manufacture <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-10s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "manufacture <command> -h" for the flags of a command.`)
}

// newFlagSet returns a flag set that reports errors instead of exiting.
func newFlagSet(name, synopsis string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: manufacture %s %s\n\nFlags:\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags maps flag errors to exit codes; ok is false when the caller must return code.
func parseFlags(fs *flag.FlagSet, args []string) (code int, ok bool) {
	err := fs.Parse(args)
	switch {
	case err == nil:
		return ExitOK, true
	case errors.Is(err, flag.ErrHelp):
		return ExitOK, false
	default:
		return ExitUsage, false
	}
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return fmt.Sprint(*s) }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
