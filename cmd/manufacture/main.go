// Command manufacture is the Manufacture core: a domain-agnostic pipeline engine
// with a built-in admin panel (web/, embedded at build time).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/t1menar/manufacture-core/internal/cli"
	"github.com/t1menar/manufacture-core/web"
)

func main() {
	cli.UI = web.Handler()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}, os.Args[1:])
	stop()
	os.Exit(code)
}
