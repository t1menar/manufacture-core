// Command manufacture is the Manufacture core: a domain-agnostic pipeline engine
// with a built-in admin panel. This version ships the spec tooling; the server
// roles arrive in the next PRs.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/t1menar/manufacture-core/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}, os.Args[1:])
	stop()
	os.Exit(code)
}
