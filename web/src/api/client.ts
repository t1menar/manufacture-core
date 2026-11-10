import { request } from "./http";
import type {
  ApplyResult,
  AuditEntry,
  Conveyor,
  List,
  ModuleDetail,
  ModuleMetrics,
  ModuleStatus,
  Overview,
  PipelineDetail,
  PipelineRevision,
  PipelineSummary,
  Presigned,
  Run,
  RunContext,
  RunDetail,
  RunList,
  RunStatus,
  SystemInfo,
} from "./types";

const enc = encodeURIComponent;

export type RunFilter = { pipeline?: string; status?: RunStatus | ""; q?: string; cursor?: string; limit?: number };
export type RunAction = "cancel" | "retry" | "resume";

/** Typed gateway client (`api/openapi.yaml` 0.3.0). Retry creates a new run (201); resume continues a needs_attention run. Every call goes through the pluggable transport (real core or simulator). */
export const api = {
  overview: (signal?: AbortSignal) => request<Overview>("/overview", { signal }),
  system: (signal?: AbortSignal) => request<SystemInfo>("/system", { signal }),
  verifyToken: (token: string) => request<SystemInfo>("/system", { token }),

  pipelines: async (signal?: AbortSignal) => (await request<List<PipelineSummary>>("/pipelines", { signal })).items,
  conveyor: (name: string, signal?: AbortSignal) => request<Conveyor>(`/pipelines/${enc(name)}/conveyor`, { signal }),
  pipeline: (name: string, signal?: AbortSignal) => request<PipelineDetail>(`/pipelines/${enc(name)}`, { signal }),
  pipelineRevisions: async (name: string, signal?: AbortSignal) =>
    (await request<List<PipelineRevision>>(`/pipelines/${enc(name)}/revisions`, { signal })).items,
  pipelineRevision: (name: string, rev: number, signal?: AbortSignal) =>
    request<PipelineDetail>(`/pipelines/${enc(name)}/revisions/${rev}`, { signal }),
  startRun: (pipeline: string, data?: Record<string, unknown>) =>
    request<Run>(`/pipelines/${enc(pipeline)}/runs`, { method: "POST", body: { data } }),

  apply: (yaml: string, dryRun = false) =>
    request<ApplyResult>("/apply", {
      method: "POST",
      query: { dry_run: dryRun ? "true" : undefined, source: "ui" },
      raw: { body: yaml, contentType: "application/yaml" },
    }),

  modules: async (signal?: AbortSignal) => (await request<List<ModuleStatus>>("/modules", { signal })).items,
  module: (name: string, signal?: AbortSignal) => request<ModuleDetail>(`/modules/${enc(name)}`, { signal }),
  scaleModule: (name: string, replicas: number) =>
    request<ModuleDetail>(`/modules/${enc(name)}/scale`, { method: "POST", body: { replicas } }),
  moduleMetrics: (name: string, range: string, signal?: AbortSignal) =>
    request<ModuleMetrics>(`/modules/${enc(name)}/metrics`, { query: { range }, signal }),

  runs: (f: RunFilter, signal?: AbortSignal) =>
    request<RunList>("/runs", {
      query: { pipeline: f.pipeline, status: f.status, q: f.q, cursor: f.cursor, limit: f.limit },
      signal,
    }),
  run: (id: string, signal?: AbortSignal) => request<RunDetail>(`/runs/${enc(id)}`, { signal }),
  snapshot: (id: string, afterStage: number, signal?: AbortSignal) =>
    request<RunContext>(`/runs/${enc(id)}/snapshots/${afterStage}`, { signal }),
  runAction: (id: string, action: RunAction) => request<Run>(`/runs/${enc(id)}/${action}`, { method: "POST" }),

  presign: (ref: string) => request<Presigned>("/blobs/presign", { query: { ref } }),
  audit: (cursor?: string, signal?: AbortSignal) => request<List<AuditEntry>>("/audit", { query: { cursor, limit: 100 }, signal }),
};
