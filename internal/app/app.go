// Package app wires the roles of `manufacture serve` (gateway, engine, controller)
// from the configuration.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/t1menar/manufacture-core/internal/buildinfo"
	"github.com/t1menar/manufacture-core/internal/config"
	"github.com/t1menar/manufacture-core/internal/contract"
	"github.com/t1menar/manufacture-core/internal/db"
	"github.com/t1menar/manufacture-core/internal/gateway"
	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
	"github.com/t1menar/manufacture-core/schemas"
)

// Options of a serve run that do not come from the environment.
type Options struct {
	// ApplyPaths are applied (source "bootstrap") after migrations, before serving.
	ApplyPaths []string
	// UI serves the admin panel; nil serves a placeholder page.
	UI http.Handler
	// Stderr receives the one-time admin token banner.
	Stderr io.Writer
	// Ready is closed when the listeners are up (tests).
	Ready chan<- net.Addr
}

// NewLogger builds the process logger from the configuration.
func NewLogger(cfg config.Config, w io.Writer) *slog.Logger {
	ho := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, ho))
	}
	return slog.New(slog.NewJSONHandler(w, ho))
}

// Migrate opens the database and applies migrations.
func Migrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value())
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "migrations applied", slog.Any("versions", applied))
	return nil
}

// Serve runs the configured roles until ctx is done.
func Serve(ctx context.Context, cfg config.Config, log *slog.Logger, o Options) error {
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value())
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	st := store.New(pool)

	if len(o.ApplyPaths) > 0 {
		if err := bootstrapApply(ctx, st, o.ApplyPaths, log); err != nil {
			return err
		}
	}

	if !cfg.HasRole(config.RoleGateway) {
		log.InfoContext(ctx, "gateway role disabled")
		<-ctx.Done()
		return nil
	}
	hash, err := adminTokenHash(ctx, cfg, st, o.Stderr)
	if err != nil {
		return err
	}
	bi := buildinfo.Get()
	roles := make([]string, 0, len(cfg.Roles))
	for _, r := range cfg.Roles {
		roles = append(roles, string(r))
	}
	gw := gateway.New(gateway.Options{
		Store: st, TokenHash: hash, UI: o.UI, Logger: log, SecureCookies: cfg.SecureCookies,
		System: gateway.System{Version: bi.Version, Commit: bi.Commit, ContractVersion: schemas.Version(),
			Driver: string(cfg.Runtime), Roles: roles,
			Limits: gateway.Limits{MaxMessageBytes: contract.MaxMessageBytes, InlineMaxBytes: contract.InlineMaxBytes,
				MaxApplyBytes: gateway.MaxApplyBytes}},
	})
	return listen(ctx, gw.HTTPServer(cfg.ListenAddr), log, o.Ready)
}

func listen(ctx context.Context, srv *http.Server, log *slog.Logger, ready chan<- net.Addr) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "gateway listening", slog.String("addr", ln.Addr().String()))
	if ready != nil {
		ready <- ln.Addr()
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func adminTokenHash(ctx context.Context, cfg config.Config, st *store.Store, stderr io.Writer) ([32]byte, error) {
	if t := cfg.AdminToken.Value(); t != "" {
		return gateway.TokenHash(t), nil
	}
	hash, token, created, err := st.EnsureAdminToken(ctx)
	if err != nil {
		return hash, fmt.Errorf("admin token: %w", err)
	}
	if created {
		fmt.Fprintf(stderr, "\n  Admin token (shown once, store it now):\n\n    %s\n\n"+
			"  Use it as MF_ADMIN_TOKEN for mfctl and to sign in to the panel.\n\n", token)
	}
	return hash, nil
}

func bootstrapApply(ctx context.Context, st *store.Store, paths []string, log *slog.Logger) error {
	inputs, diags := spec.ReadInputs(paths, nil)
	if diags.Errors() > 0 {
		return fmt.Errorf("read %v: %s", paths, diags[0])
	}
	files := make([]store.File, 0, len(inputs))
	for _, in := range inputs {
		files = append(files, store.File{Name: in.Name, Content: in.Data})
	}
	res, err := st.Apply(ctx, store.ApplyRequest{Files: files, Source: store.SourceBootstrap, Actor: "bootstrap"})
	var ve *store.ValidationError
	if errors.As(err, &ve) {
		for _, d := range ve.Diagnostics {
			log.ErrorContext(ctx, "bootstrap apply", slog.String("diagnostic", d.String()))
		}
		return fmt.Errorf("bootstrap apply of %v failed", paths)
	}
	if err != nil {
		return err
	}
	for _, it := range res.Results {
		log.InfoContext(ctx, "bootstrap apply", slog.String("resource", string(it.Kind)+"/"+it.Name),
			slog.String("action", it.Action), slog.Int("revision", it.Revision))
	}
	return nil
}
