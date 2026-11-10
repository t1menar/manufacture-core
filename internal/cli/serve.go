package cli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/t1menar/manufacture-core/internal/app"
	"github.com/t1menar/manufacture-core/internal/config"
)

// UI is the admin panel handler compiled into the binary; set by main.
var UI http.Handler

func runServe(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet("serve", "[--apply <file|dir>]...", env.Stderr)
	var apply stringList
	fs.Var(&apply, "apply", "apply these specs (source bootstrap) at start; repeatable")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(env.Stderr, "manufacture serve: unexpected arguments %q\n", fs.Args())
		return ExitUsage
	}
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		fmt.Fprintf(env.Stderr, "invalid configuration:\n%v\n", err)
		return ExitFailure
	}
	log := app.NewLogger(cfg, env.Stderr)
	if err := app.Serve(ctx, cfg, log, app.Options{ApplyPaths: apply, UI: UI, Stderr: env.Stderr}); err != nil {
		log.Error("serve failed", "err", err)
		return ExitFailure
	}
	return ExitOK
}

func runMigrate(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet("migrate", "", env.Stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		fmt.Fprintf(env.Stderr, "invalid configuration:\n%v\n", err)
		return ExitFailure
	}
	log := app.NewLogger(cfg, env.Stderr)
	if err := app.Migrate(ctx, cfg, log); err != nil {
		log.Error("migrate failed", "err", err)
		return ExitFailure
	}
	return ExitOK
}
