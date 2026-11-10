package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/t1menar/manufacture-core/internal/engine"
)

// Runs is the engine as the gateway sees it.
type Runs interface {
	StartRun(ctx context.Context, req engine.StartRequest) (engine.Started, error)
	TriggerSource(ctx context.Context, pipeline string, data json.RawMessage) (uuid.UUID, error)
	HasSource(ctx context.Context, pipeline string) (bool, error)
	GetRun(ctx context.Context, id uuid.UUID) (engine.RunView, error)
	ListRuns(ctx context.Context, f engine.RunFilter) ([]engine.RunView, string, error)
	GetRunDetail(ctx context.Context, id uuid.UUID) (engine.RunDetail, error)
	Snapshot(ctx context.Context, id uuid.UUID, afterStage, visit int) (json.RawMessage, error)
	RunEvents(ctx context.Context, id uuid.UUID, cursor string, limit int) ([]engine.StoredEvent, string, error)
	CancelRun(ctx context.Context, id uuid.UUID, op engine.Operator) (engine.RunView, error)
	ResumeRun(ctx context.Context, id uuid.UUID, op engine.Operator, ack bool) (engine.RunView, error)
	RetryRun(ctx context.Context, id uuid.UUID, op engine.Operator) (engine.RunView, error)
	SetPriority(ctx context.Context, id uuid.UUID, priority int, op engine.Operator) (engine.RunView, error)
	SetBudget(ctx context.Context, id uuid.UUID, runUSD, warnUSD float64, op engine.Operator) (engine.RunView, error)
}

// RunRoutes registers the run endpoints.
func (s *Server) RunRoutes(mux *http.ServeMux, runs Runs) {
	h := &runHandlers{s: s, runs: runs}
	mux.HandleFunc("POST /api/v1/pipelines/{name}/runs", h.start)
	mux.HandleFunc("GET /api/v1/runs", h.list)
	mux.HandleFunc("GET /api/v1/runs/{id}", h.get)
	mux.HandleFunc("POST /api/v1/runs/{id}/cancel", h.cancel)
	mux.HandleFunc("POST /api/v1/runs/{id}/resume", h.resume)
	mux.HandleFunc("POST /api/v1/runs/{id}/retry", h.retry)
	mux.HandleFunc("GET /api/v1/runs/{id}/snapshots/{after_stage}", h.snapshot)
	mux.HandleFunc("GET /api/v1/runs/{id}/events", h.events)
	mux.HandleFunc("PATCH /api/v1/runs/{id}/priority", h.priority)
	mux.HandleFunc("PATCH /api/v1/runs/{id}/budget", h.budget)
}

type runHandlers struct {
	s    *Server
	runs Runs
}

const maxRunBody = 1 << 20

// decode reads a strict JSON body; an empty body leaves v unchanged.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRunBody))
	if err != nil {
		problem(w, http.StatusRequestEntityTooLarge, CodeTooLarge, err.Error())
		return false
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		problem(w, http.StatusUnprocessableEntity, CodeValidationFailed, "invalid body: "+err.Error())
		return false
	}
	return true
}

func runID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, http.StatusNotFound, CodeNotFound, "no such run")
		return id, false
	}
	return id, true
}

func (h *runHandlers) start(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data     *map[string]any   `json:"data"`
		Priority *int              `json:"priority"`
		Labels   map[string]string `json:"labels"`
		DedupKey string            `json:"dedup_key"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := r.PathValue("name")
	if body.Priority != nil && (*body.Priority < -100 || *body.Priority > 100) {
		problem(w, http.StatusUnprocessableEntity, CodeValidationFailed, "priority must be between -100 and 100")
		return
	}
	if body.DedupKey == "" {
		body.DedupKey = r.Header.Get("Idempotency-Key")
	}
	if body.Data == nil {
		src, err := h.runs.HasSource(r.Context(), name)
		if err != nil {
			h.s.fail(w, r, err)
			return
		}
		if src {
			id, err := h.runs.TriggerSource(r.Context(), name, nil)
			if err != nil {
				h.s.fail(w, r, err)
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"trigger_id": id})
			return
		}
	}
	req := engine.StartRequest{Pipeline: name, Priority: body.Priority, Labels: body.Labels, DedupKey: body.DedupKey,
		Trigger: engine.Trigger{Kind: "manual", By: actorAdmin}}
	if body.Data != nil {
		req.Data = *body.Data
	}
	res, err := h.runs.StartRun(r.Context(), req)
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	h.writeRun(w, r, http.StatusCreated, res.RunID)
}

func (h *runHandlers) writeRun(w http.ResponseWriter, r *http.Request, status int, id uuid.UUID) {
	v, err := h.runs.GetRun(r.Context(), id)
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

func (h *runHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := h.runs.ListRuns(r.Context(), engine.RunFilter{Pipeline: q.Get("pipeline"), Status: q.Get("status"), Q: q.Get("q"),
		Cursor: q.Get("cursor"), Limit: limitParam(r)})
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	out := map[string]any{"items": items}
	if next != "" {
		out["next_cursor"] = next
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *runHandlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	d, err := h.runs.GetRunDetail(r.Context(), id)
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type operatorBody struct {
	Comment string `json:"comment"`
	Ack     bool   `json:"acknowledge_attention"`
}

func (h *runHandlers) operator(w http.ResponseWriter, r *http.Request, allowAck bool) (uuid.UUID, operatorBody, bool) {
	id, ok := runID(w, r)
	if !ok {
		return id, operatorBody{}, false
	}
	var body operatorBody
	if !decode(w, r, &body) {
		return id, body, false
	}
	if body.Ack && !allowAck {
		problem(w, http.StatusUnprocessableEntity, CodeValidationFailed, "acknowledge_attention is only accepted by resume")
		return id, body, false
	}
	if body.Comment == "" || len(body.Comment) > 2000 {
		problem(w, http.StatusUnprocessableEntity, CodeValidationFailed, "comment is required (1..2000 characters)")
		return id, body, false
	}
	return id, body, true
}

func (h *runHandlers) respond(w http.ResponseWriter, r *http.Request, status int, v engine.RunView, err error) {
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

func (h *runHandlers) cancel(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.operator(w, r, false)
	if ok {
		v, err := h.runs.CancelRun(r.Context(), id, engine.Operator{Actor: actorAdmin, Comment: body.Comment})
		h.respond(w, r, http.StatusOK, v, err)
	}
}

func (h *runHandlers) resume(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.operator(w, r, true)
	if ok {
		v, err := h.runs.ResumeRun(r.Context(), id, engine.Operator{Actor: actorAdmin, Comment: body.Comment}, body.Ack)
		h.respond(w, r, http.StatusOK, v, err)
	}
}

func (h *runHandlers) retry(w http.ResponseWriter, r *http.Request) {
	id, body, ok := h.operator(w, r, false)
	if ok {
		v, err := h.runs.RetryRun(r.Context(), id, engine.Operator{Actor: actorAdmin, Comment: body.Comment})
		h.respond(w, r, http.StatusCreated, v, err)
	}
}

func (h *runHandlers) priority(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	var body struct {
		Priority *int   `json:"priority"`
		Comment  string `json:"comment"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Priority == nil || *body.Priority < -100 || *body.Priority > 100 || body.Comment == "" {
		problem(w, http.StatusUnprocessableEntity, CodeValidationFailed, "priority (-100..100) and comment are required")
		return
	}
	v, err := h.runs.SetPriority(r.Context(), id, *body.Priority, engine.Operator{Actor: actorAdmin, Comment: body.Comment})
	h.respond(w, r, http.StatusOK, v, err)
}

func (h *runHandlers) budget(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	var body struct {
		RunUSD  float64 `json:"run_usd"`
		WarnUSD float64 `json:"warn_usd"`
		Comment string  `json:"comment"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.RunUSD <= 0 || body.WarnUSD < 0 || body.Comment == "" {
		problem(w, http.StatusUnprocessableEntity, CodeValidationFailed, "run_usd (> 0) and comment are required")
		return
	}
	v, err := h.runs.SetBudget(r.Context(), id, body.RunUSD, body.WarnUSD, engine.Operator{Actor: actorAdmin, Comment: body.Comment})
	h.respond(w, r, http.StatusOK, v, err)
}

func (h *runHandlers) snapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	stage, err := strconv.Atoi(r.PathValue("after_stage"))
	if err != nil || stage < 0 {
		problem(w, http.StatusBadRequest, CodeInvalidRequest, "after_stage must be a non-negative integer")
		return
	}
	visit := 0
	if v := r.URL.Query().Get("visit"); v != "" {
		if visit, err = strconv.Atoi(v); err != nil || visit < 1 {
			problem(w, http.StatusBadRequest, CodeInvalidRequest, "visit must be a positive integer")
			return
		}
	}
	body, err := h.runs.Snapshot(r.Context(), id, stage, visit)
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body) //nolint:gosec // JSON from our own snapshot table, served as application/json with nosniff
}

func (h *runHandlers) events(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	items, next, err := h.runs.RunEvents(r.Context(), id, r.URL.Query().Get("cursor"), limitParam(r))
	if errors.Is(err, engine.ErrNotFound) {
		problem(w, http.StatusNotFound, CodeNotFound, "no such run")
		return
	}
	if err != nil {
		h.s.fail(w, r, err)
		return
	}
	out := map[string]any{"items": items}
	if next != "" {
		out["next_cursor"] = next
	}
	writeJSON(w, http.StatusOK, out)
}
