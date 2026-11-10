package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
)

// Problem is an RFC 9457 error body.
type Problem struct {
	Type   string           `json:"type"`
	Title  string           `json:"title"`
	Status int              `json:"status"`
	Code   string           `json:"code"`
	Detail string           `json:"detail,omitempty"`
	Errors spec.Diagnostics `json:"errors,omitempty"`
}

// Stable problem codes.
const (
	CodeNotFound         = "not_found"
	CodeConflict         = "conflict"
	CodeInvalidRequest   = "invalid_request"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeValidationFailed = "validation_failed"
	CodeNotImplemented   = "not_implemented"
	CodeUnavailable      = "unavailable"
	CodeInternal         = "internal"
	CodeTooLarge         = "too_large"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeProblem(w http.ResponseWriter, p Problem) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	writeProblem(w, Problem{Status: status, Code: code, Detail: detail})
}

// fail maps store errors to problems; unexpected errors are logged and hidden.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *store.ValidationError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ve):
		writeProblem(w, Problem{Status: http.StatusUnprocessableEntity, Code: CodeValidationFailed,
			Detail: ve.Error(), Errors: ve.Diagnostics})
	case errors.As(err, &mbe):
		problem(w, http.StatusRequestEntityTooLarge, CodeTooLarge, err.Error())
	case errors.Is(err, store.ErrNotFound):
		problem(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		problem(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		s.log.ErrorContext(r.Context(), "request failed", slog.String("method", r.Method),
			slog.String("path", r.URL.Path), slog.Any("err", err))
		problem(w, http.StatusInternalServerError, CodeInternal, "internal error; see the core logs")
	}
}
