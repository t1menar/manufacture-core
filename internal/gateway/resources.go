package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
)

type items[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// applyFiles is the JSON form of an apply body: several named files.
type applyFiles struct {
	Files []struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	} `json:"files"`
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dry, _ := strconv.ParseBool(q.Get("dry_run"))
	src := store.Source(q.Get("source"))
	if src == "" {
		src = store.SourceAPI
	}
	if !src.Valid() {
		problem(w, http.StatusBadRequest, CodeInvalidRequest, "source must be cli, ui, bootstrap or api")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(s.system.Limits.MaxApplyBytes)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	files, err := applyInput(r.Header.Get("Content-Type"), body)
	if err != nil {
		problem(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	res, err := s.store.Apply(r.Context(), store.ApplyRequest{Files: files, DryRun: dry, Source: src, Actor: actorAdmin})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if res.Results == nil {
		res.Results = []store.ApplyItem{}
	}
	writeJSON(w, http.StatusOK, res)
}

// applyInput accepts YAML (one stream of documents), JSON documents, or
// {"files": [{"name", "content"}]} so that diagnostics name the original files.
func applyInput(contentType string, body []byte) ([]store.File, error) {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch mt {
	case "application/yaml", "application/x-yaml", "text/yaml", "text/plain", "":
		return []store.File{{Name: "request", Content: body}}, nil
	case "application/json":
		var f applyFiles
		if json.Unmarshal(body, &f) == nil && len(f.Files) > 0 {
			out := make([]store.File, 0, len(f.Files))
			for i, x := range f.Files {
				name := x.Name
				if name == "" {
					name = fmt.Sprintf("file %d", i+1)
				}
				out = append(out, store.File{Name: name, Content: []byte(x.Content)})
			}
			return out, nil
		}
		return []store.File{{Name: "request", Content: body}}, nil // JSON is YAML
	default:
		return nil, fmt.Errorf("unsupported content type %q; send application/yaml or application/json", mt)
	}
}

func (s *Server) listModules(w http.ResponseWriter, r *http.Request) {
	revs, err := s.store.List(r.Context(), spec.KindModule)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := items[ModuleStatus]{Items: make([]ModuleStatus, 0, len(revs))}
	for _, rev := range revs {
		st, _, err := s.moduleStatus(r.Context(), rev)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Items = append(out.Items, st)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) moduleDetail(r *http.Request, rev store.Revision) (ModuleDetail, error) {
	st, live, err := s.moduleStatus(r.Context(), rev)
	if err != nil {
		return ModuleDetail{}, err
	}
	reps := live.Replicas
	if reps == nil {
		reps = []Replica{}
	}
	return ModuleDetail{ModuleStatus: st, Spec: rev.Spec, YAML: rev.YAML, ReplicaList: reps, Manifest: live.Manifest,
		UpdatedAt: rev.AppliedAt, AppliedBy: rev.AppliedBy, Source: string(rev.Source), ManagedBy: rev.ManagedBy()}, nil
}

func (s *Server) getModule(w http.ResponseWriter, r *http.Request) {
	rev, err := s.store.Get(r.Context(), spec.KindModule, r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.moduleDetail(r, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteModule(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.Context(), spec.KindModule, r.PathValue("name"), actorAdmin, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) scaleModule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Replicas *int `json:"replicas"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Replicas == nil || *body.Replicas < 0 || *body.Replicas > 1000 {
		problem(w, http.StatusBadRequest, CodeInvalidRequest, "body must be {\"replicas\": 0..1000}")
		return
	}
	src := store.Source(r.URL.Query().Get("source"))
	if !src.Valid() {
		src = store.SourceAPI
	}
	rev, err := s.store.Scale(r.Context(), r.PathValue("name"), *body.Replicas, src, actorAdmin)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.moduleDetail(r, rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) listPipelines(w http.ResponseWriter, r *http.Request) {
	revs, err := s.store.List(r.Context(), spec.KindPipeline)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := items[PipelineSummary]{Items: make([]PipelineSummary, 0, len(revs))}
	for _, rev := range revs {
		p, err := s.pipelineSummary(r.Context(), rev)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Items = append(out.Items, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getPipeline(w http.ResponseWriter, r *http.Request) {
	rev, err := s.store.Get(r.Context(), spec.KindPipeline, r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := pipelineDetail(rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deletePipeline(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.Context(), spec.KindPipeline, r.PathValue("name"), actorAdmin, s.busy(r.PathValue("name"))); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPipelineRevision(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("revision"))
	if err != nil || n < 1 {
		problem(w, http.StatusBadRequest, CodeInvalidRequest, "revision must be a positive integer")
		return
	}
	rev, err := s.store.RevisionAt(r.Context(), spec.KindPipeline, r.PathValue("name"), n)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := pipelineDetail(rev)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type revisionMeta struct {
	Revision  int    `json:"revision"`
	SpecHash  string `json:"spec_hash"`
	AppliedAt string `json:"applied_at"`
	AppliedBy string `json:"applied_by"`
	Source    string `json:"source"`
	Current   bool   `json:"current"`
}

func (s *Server) listRevisions(kind spec.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		revs, err := s.store.Revisions(r.Context(), kind, r.PathValue("name"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := items[revisionMeta]{Items: make([]revisionMeta, 0, len(revs))}
		for _, rev := range revs {
			out.Items = append(out.Items, revisionMeta{Revision: rev.Revision, SpecHash: rev.SpecHash,
				AppliedAt: rev.AppliedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), AppliedBy: rev.AppliedBy,
				Source: string(rev.Source), Current: rev.Current})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mods, err := s.store.List(ctx, spec.KindModule)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pipes, err := s.store.List(ctx, spec.KindPipeline)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := struct {
		Pipelines   []PipelineSummary `json:"pipelines"`
		Modules     []ModuleStatus    `json:"modules"`
		Runs        RunCounters       `json:"runs"`
		LastEventID string            `json:"last_event_id"`
	}{Pipelines: []PipelineSummary{}, Modules: []ModuleStatus{}, LastEventID: s.live.LastEventID(ctx)}
	for _, rev := range pipes {
		p, err := s.pipelineSummary(ctx, rev)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Pipelines = append(out.Pipelines, p)
	}
	for _, rev := range mods {
		m, _, err := s.moduleStatus(ctx, rev)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Modules = append(out.Modules, m)
	}
	out.Runs = s.live.Runs(ctx, "")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, before, ok := page(w, r)
	if !ok {
		return
	}
	es, err := s.store.ListAudit(r.Context(), before, limit+1)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := items[store.AuditEntry]{Items: es}
	if len(es) > limit {
		out.Items = es[:limit]
		out.NextCursor = strconv.FormatInt(es[limit-1].ID, 10)
	}
	writeJSON(w, http.StatusOK, out)
}

// page reads limit (1..200, default 50) and a numeric cursor.
func page(w http.ResponseWriter, r *http.Request) (limit int, cursor int64, ok bool) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problem(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be 1..200")
			return 0, 0, false
		}
		limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			problem(w, http.StatusBadRequest, CodeInvalidRequest, "invalid cursor")
			return 0, 0, false
		}
		cursor = n
	}
	return limit, cursor, true
}
