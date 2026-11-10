// Package gateway serves the REST API (api/openapi.yaml), the live event stream
// and the embedded admin panel.
package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/t1menar/manufacture-core/api"
	"github.com/t1menar/manufacture-core/internal/store"
)

// actorAdmin is the actor of every authenticated change (one admin token for now).
const actorAdmin = "admin"

// Limits shown in /api/v1/system.
type Limits struct {
	MaxMessageBytes int `json:"max_message_bytes"`
	InlineMaxBytes  int `json:"inline_max_bytes"`
	MaxApplyBytes   int `json:"max_apply_bytes"`
	MaxForeachItems int `json:"max_foreach_items,omitempty"`
}

// System is the body of GET /api/v1/system.
type System struct {
	Version         string   `json:"version"`
	Commit          string   `json:"commit,omitempty"`
	ContractVersion string   `json:"contract_version"`
	Driver          string   `json:"driver"`
	Roles           []string `json:"roles"`
	Limits          Limits   `json:"limits"`
}

// MaxApplyBytes limits the body of POST /api/v1/apply.
const MaxApplyBytes = 8 << 20

// Options configure a Server.
type Options struct {
	Store     *store.Store
	TokenHash [32]byte
	System    System
	// UI serves the admin panel for every path outside the API; nil serves a placeholder.
	UI http.Handler
	// Live provides module and pipeline status; nil reports "unknown".
	Live Live
	// Routes, when set, may add more routes (engine, stream) to the authenticated API mux.
	Routes func(mux *http.ServeMux, auth func(http.Handler) http.Handler)
	Ready  func(ctx context.Context) map[string]error
	// PipelineBusy, set by the engine, refuses deleting a pipeline with active runs
	// (it runs in the deleting transaction).
	PipelineBusy  func(ctx context.Context, tx pgx.Tx, pipeline string) error
	SecureCookies bool
	Logger        *slog.Logger
}

// Server is the HTTP gateway.
type Server struct {
	store         *store.Store
	auth          *Auth
	system        System
	live          Live
	ready         func(ctx context.Context) map[string]error
	pipelineBusy  func(ctx context.Context, tx pgx.Tx, pipeline string) error
	secureCookies bool
	log           *slog.Logger
	handler       http.Handler
}

// New builds the gateway handler.
func New(o Options) *Server {
	s := &Server{store: o.Store, auth: NewAuth(o.TokenHash), system: o.System, live: o.Live,
		ready: o.Ready, pipelineBusy: o.PipelineBusy, secureCookies: o.SecureCookies, log: o.Logger}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.live == nil {
		s.live = noLive{}
	}
	if s.system.Limits.MaxApplyBytes == 0 {
		s.system.Limits.MaxApplyBytes = MaxApplyBytes
	}
	ui := o.UI
	if ui == nil {
		ui = http.HandlerFunc(placeholderUI)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /api/v1/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(api.OpenAPI)
	})
	mux.HandleFunc("POST /api/v1/session", s.createSession)
	mux.HandleFunc("DELETE /api/v1/session", s.deleteSession)

	authed := http.NewServeMux()
	authed.HandleFunc("GET /api/v1/system", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, s.system) })
	authed.HandleFunc("POST /api/v1/apply", s.apply)
	authed.HandleFunc("GET /api/v1/modules", s.listModules)
	authed.HandleFunc("GET /api/v1/modules/{name}", s.getModule)
	authed.HandleFunc("DELETE /api/v1/modules/{name}", s.deleteModule)
	authed.HandleFunc("GET /api/v1/modules/{name}/revisions", s.listRevisions("Module"))
	authed.HandleFunc("POST /api/v1/modules/{name}/scale", s.scaleModule)
	authed.HandleFunc("GET /api/v1/pipelines", s.listPipelines)
	authed.HandleFunc("GET /api/v1/pipelines/{name}", s.getPipeline)
	authed.HandleFunc("DELETE /api/v1/pipelines/{name}", s.deletePipeline)
	authed.HandleFunc("GET /api/v1/pipelines/{name}/revisions", s.listRevisions("Pipeline"))
	authed.HandleFunc("GET /api/v1/pipelines/{name}/revisions/{revision}", s.getPipelineRevision)
	authed.HandleFunc("GET /api/v1/overview", s.overview)
	authed.HandleFunc("GET /api/v1/audit", s.listAudit)
	if o.Routes != nil {
		o.Routes(authed, s.requireAuth)
	}
	// Everything else under /api/ that is planned but not served yet.
	authed.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		problem(w, http.StatusNotFound, CodeNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	})
	mux.Handle("/api/", s.requireAuth(authed))
	mux.Handle("/", ui)

	s.handler = recoverer(s.log, securityHeaders(mux))
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// HTTPServer wraps the handler with timeouts suitable for the API and SSE.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: the event stream is long-lived; handlers bound their own work.
	}
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]string{}
	ready := true
	add := func(name string, err error) {
		if err != nil {
			ready = false
			checks[name] = err.Error()
			return
		}
		checks[name] = "ok"
	}
	if s.store != nil {
		add("database", s.store.Ping(ctx))
	}
	if s.ready != nil {
		for k, err := range s.ready(ctx) {
			add(k, err)
		}
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ready": ready, "checks": checks})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as net/http does.
					panic(v)
				}
				log.ErrorContext(r.Context(), "panic in handler", slog.Any("panic", v), slog.String("path", r.URL.Path))
				problem(w, http.StatusInternalServerError, CodeInternal, "internal error; see the core logs")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func placeholderUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><title>Manufacture</title><p>The admin panel is not part of this build. " +
		"The API is at <a href=\"/api/v1/openapi.yaml\">/api/v1/openapi.yaml</a>.</p>\n"))
}

func (s *Server) busy(pipeline string) func(context.Context, pgx.Tx) error {
	if s.pipelineBusy == nil {
		return nil
	}
	return func(ctx context.Context, tx pgx.Tx) error { return s.pipelineBusy(ctx, tx, pipeline) }
}
