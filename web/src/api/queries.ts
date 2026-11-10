import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import { api, type RunFilter } from "./client";

/** Query keys and options in one place so live events can invalidate precisely. */
export const qk = {
  overview: ["overview"] as const,
  system: ["system"] as const,
  pipelines: ["pipelines"] as const,
  pipeline: (name: string) => ["pipelines", name] as const,
  revisions: (name: string) => ["pipelines", name, "revisions"] as const,
  revision: (name: string, rev: number) => ["pipelines", name, "revisions", rev] as const,
  modules: ["modules"] as const,
  module: (name: string) => ["modules", name] as const,
  moduleMetrics: (name: string, range: string) => ["modules", name, "metrics", range] as const,
  runs: (f: RunFilter) => ["runs", f] as const,
  run: (id: string) => ["run", id] as const,
  snapshot: (id: string, stage: number) => ["run", id, "snapshot", stage] as const,
  conveyor: (name: string) => ["pipelines", name, "conveyor"] as const,
  audit: ["audit"] as const,
};

export const q = {
  overview: () => queryOptions({ queryKey: qk.overview, queryFn: ({ signal }) => api.overview(signal), meta: { silent: true } }),
  system: () => queryOptions({ queryKey: qk.system, queryFn: ({ signal }) => api.system(signal), staleTime: 60_000 }),
  pipelines: () => queryOptions({ queryKey: qk.pipelines, queryFn: ({ signal }) => api.pipelines(signal) }),
  pipeline: (name: string) => queryOptions({ queryKey: qk.pipeline(name), queryFn: ({ signal }) => api.pipeline(name, signal) }),
  revisions: (name: string) => queryOptions({ queryKey: qk.revisions(name), queryFn: ({ signal }) => api.pipelineRevisions(name, signal) }),
  revision: (name: string, rev: number) =>
    queryOptions({ queryKey: qk.revision(name, rev), queryFn: ({ signal }) => api.pipelineRevision(name, rev, signal), staleTime: Infinity }),
  modules: () => queryOptions({ queryKey: qk.modules, queryFn: ({ signal }) => api.modules(signal) }),
  module: (name: string) => queryOptions({ queryKey: qk.module(name), queryFn: ({ signal }) => api.module(name, signal) }),
  moduleMetrics: (name: string, range: string) =>
    queryOptions({
      queryKey: qk.moduleMetrics(name, range),
      queryFn: ({ signal }) => api.moduleMetrics(name, range, signal),
      refetchInterval: 15_000,
      placeholderData: keepPreviousData,
    }),
  runs: (f: RunFilter) => queryOptions({ queryKey: qk.runs(f), queryFn: ({ signal }) => api.runs(f, signal), placeholderData: keepPreviousData }),
  run: (id: string) => queryOptions({ queryKey: qk.run(id), queryFn: ({ signal }) => api.run(id, signal) }),
  conveyor: (name: string) => queryOptions({ queryKey: qk.conveyor(name), queryFn: ({ signal }) => api.conveyor(name, signal), meta: { silent: true } }),
  audit: () => queryOptions({ queryKey: qk.audit, queryFn: ({ signal }) => api.audit(undefined, signal) }),
  snapshot: (id: string, stage: number) =>
    queryOptions({ queryKey: qk.snapshot(id, stage), queryFn: ({ signal }) => api.snapshot(id, stage, signal), staleTime: Infinity }),
};
