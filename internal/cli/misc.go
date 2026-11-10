package cli

import (
	"context"
	"fmt"

	"github.com/t1menar/manufacture-core/internal/buildinfo"
	"github.com/t1menar/manufacture-core/internal/config"
	"github.com/t1menar/manufacture-core/schemas"
)

func runConfig(_ context.Context, env Env, args []string) int {
	fs := newFlagSet("config", "", env.Stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		fmt.Fprintf(env.Stderr, "invalid configuration:\n%v\n", err)
		return ExitFailure
	}
	if err := writeJSON(env.Stdout, cfg); err != nil {
		fmt.Fprintf(env.Stderr, "manufacture config: %v\n", err)
		return ExitFailure
	}
	return ExitOK
}

type versionInfo struct {
	buildinfo.Info
	Contract string `json:"contract"`
}

func runVersion(_ context.Context, env Env, args []string) int {
	fs := newFlagSet("version", "[-o text|json]", env.Stderr)
	output := fs.String("o", "text", "output format: text or json")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	v := versionInfo{Info: buildinfo.Get(), Contract: schemas.Version()}
	var err error
	switch *output {
	case "json":
		err = writeJSON(env.Stdout, v)
	case "text":
		_, err = fmt.Fprintf(env.Stdout, "manufacture %s (commit %s, %s, contract %s)\n", v.Version, orUnknown(v.Commit), v.GoVersion, v.Contract)
	default:
		fmt.Fprintf(env.Stderr, "manufacture version: -o must be text or json, got %q\n", *output)
		return ExitUsage
	}
	if err != nil {
		return ExitFailure
	}
	return ExitOK
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
