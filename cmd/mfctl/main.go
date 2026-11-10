// Command mfctl manages a Manufacture core through its API: apply specs, list and
// describe resources, start runs.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/t1menar/manufacture-core/internal/mfctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := mfctl.Run(ctx, mfctl.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}, os.Args[1:])
	stop()
	os.Exit(code)
}
