package gateway

import (
	"context"
	"time"

	"github.com/t1menar/manufacture-core/internal/spec"
	"github.com/t1menar/manufacture-core/internal/store"
)

// Lane is one step of a stage.
type Lane struct {
	StepID string `json:"step_id"`
	Module string `json:"module"`
}

// RouteView is a loop back to an earlier stage.
type RouteView struct {
	Goto      string `json:"goto"`
	MaxVisits int    `json:"max_visits,omitempty"`
}

// StageView is a compiled stage.
type StageView struct {
	Name   string      `json:"name"`
	Index  int         `json:"index"`
	Kind   string      `json:"kind"`
	Lanes  []Lane      `json:"lanes"`
	Routes []RouteView `json:"routes,omitempty"`
}

// StageViews compiles the stages of a validated pipeline.
func StageViews(p *spec.Pipeline) []StageView {
	out := make([]StageView, 0, len(p.Spec.Stages))
	for i, st := range p.Spec.Stages {
		v := StageView{Name: st.Name, Index: i, Kind: string(st.Kind()), Lanes: []Lane{}}
		for _, step := range st.Steps() {
			v.Lanes = append(v.Lanes, Lane{StepID: step.ID, Module: step.Module})
		}
		for _, r := range st.Routes {
			v.Routes = append(v.Routes, RouteView{Goto: r.Goto, MaxVisits: r.MaxVisits})
		}
		out = append(out, v)
	}
	return out
}

// ReplicaCount is desired vs ready replicas.
type ReplicaCount struct {
	Desired int `json:"desired"`
	Ready   int `json:"ready"`
}

// ModuleStatus is a module with its live state.
type ModuleStatus struct {
	Name            string       `json:"name"`
	Revision        int          `json:"revision"`
	Image           string       `json:"image"`
	Runtime         string       `json:"runtime"`
	Driver          string       `json:"driver"`
	Replicas        ReplicaCount `json:"replicas"`
	Health          string       `json:"health"`
	Conditions      []Condition  `json:"conditions"`
	LastHeartbeatAt *time.Time   `json:"last_heartbeat_at,omitempty"`
	QueueDepth      int          `json:"queue_depth"`
	DLQDepth        int          `json:"dlq_depth"`
	InFlight        int          `json:"in_flight"`
	SDKVersion      string       `json:"sdk_version,omitempty"`
	ContractVersion string       `json:"contract_version,omitempty"`
	Live            LiveMetrics  `json:"live"`
}

// ModuleDetail adds the spec and the replicas.
type ModuleDetail struct {
	ModuleStatus
	Spec        any            `json:"spec"`
	YAML        string         `json:"yaml"`
	ReplicaList []Replica      `json:"replica_list"`
	Manifest    map[string]any `json:"manifest,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at"`
	AppliedBy   string         `json:"applied_by"`
	Source      string         `json:"source"`
	ManagedBy   string         `json:"managed_by,omitempty"`
}

func (s *Server) moduleStatus(ctx context.Context, rev store.Revision) (ModuleStatus, ModuleLive, error) {
	m, err := rev.Module()
	if err != nil {
		return ModuleStatus{}, ModuleLive{}, err
	}
	live := s.live.Module(ctx, rev.Name)
	desired := 0
	if m.Spec.Replicas != nil {
		desired = m.Spec.Replicas.Min
	}
	driver := live.Driver
	if driver == "" {
		driver = s.system.Driver
		if m.Spec.EffectiveRuntime() == spec.RuntimeExternal {
			driver = "external"
		}
	}
	conds := live.Conditions
	if conds == nil {
		conds = []Condition{}
	}
	st := ModuleStatus{
		Name: rev.Name, Revision: rev.Revision, Image: m.Spec.Image, Runtime: string(m.Spec.EffectiveRuntime()),
		Driver: driver, Replicas: ReplicaCount{Desired: desired, Ready: live.ReadyReplicas},
		Health: live.Health, Conditions: conds, LastHeartbeatAt: live.LastHeartbeatAt,
		QueueDepth: live.QueueDepth, DLQDepth: live.DLQDepth, InFlight: live.InFlight,
		SDKVersion: live.SDKVersion, ContractVersion: live.ContractVersion, Live: live.Metrics,
	}
	if st.Health == "" {
		st.Health = "unknown"
	}
	return st, live, nil
}

// PipelineSummary is a pipeline in lists.
type PipelineSummary struct {
	Name      string      `json:"name"`
	Revision  int         `json:"revision"`
	Stages    []StageView `json:"stages"`
	UpdatedAt time.Time   `json:"updated_at"`
	ManagedBy string      `json:"managed_by,omitempty"`
	Runs      RunCounters `json:"runs"`
}

// PipelineDetail is one pipeline revision.
type PipelineDetail struct {
	Name      string      `json:"name"`
	Revision  int         `json:"revision"`
	YAML      string      `json:"yaml"`
	Spec      any         `json:"spec"`
	Stages    []StageView `json:"stages"`
	UpdatedAt time.Time   `json:"updated_at"`
	AppliedBy string      `json:"applied_by"`
	Source    string      `json:"source"`
	ManagedBy string      `json:"managed_by,omitempty"`
}

func (s *Server) pipelineSummary(ctx context.Context, rev store.Revision) (PipelineSummary, error) {
	p, err := rev.Pipeline()
	if err != nil {
		return PipelineSummary{}, err
	}
	return PipelineSummary{Name: rev.Name, Revision: rev.Revision, Stages: StageViews(p), UpdatedAt: rev.AppliedAt,
		ManagedBy: rev.ManagedBy(), Runs: s.live.Runs(ctx, rev.Name)}, nil
}

func pipelineDetail(rev store.Revision) (PipelineDetail, error) {
	p, err := rev.Pipeline()
	if err != nil {
		return PipelineDetail{}, err
	}
	return PipelineDetail{Name: rev.Name, Revision: rev.Revision, YAML: rev.YAML, Spec: rev.Spec, Stages: StageViews(p),
		UpdatedAt: rev.AppliedAt, AppliedBy: rev.AppliedBy, Source: string(rev.Source), ManagedBy: rev.ManagedBy()}, nil
}
