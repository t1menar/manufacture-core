import type { StageKind, StageView } from "../api/types";

/** Minimal typed view of a Pipeline spec, enough for the panel (the core owns full semantics). */
export type StepSpec = { id?: string; module: string; optional?: boolean; timeout?: string };
export type RouteSpec = { when: string; goto: string; max_visits?: number };
export type StageSpec = {
  name: string;
  step?: StepSpec;
  parallel?: StepSpec[];
  foreach?: string;
  max_parallel?: number;
  join?: Record<string, unknown>;
  when?: string;
  routes?: RouteSpec[];
};
export type PipelineSpec = { trigger?: { source?: string; schedule?: string }; stages: StageSpec[] };

export function stageKind(st: StageSpec): StageKind {
  if (st.parallel?.length) return "parallel";
  if (st.foreach) return "foreach";
  return "single";
}

/**
 * Compiles stages into the conveyor view. Step ids default like the core does
 * (`internal/spec/rules_pipeline.go`): a single step takes the stage name, a parallel step its module name.
 */
export function compileStages(spec: PipelineSpec): StageView[] {
  return spec.stages.map((st, index) => {
    const lanes = st.parallel?.length
      ? st.parallel.map((s) => ({ step_id: s.id ?? s.module, module: s.module }))
      : st.step
        ? [{ step_id: st.step.id ?? st.name, module: st.step.module }]
        : [];
    const view: StageView = { name: st.name, index, kind: stageKind(st), lanes };
    if (st.routes?.length) {
      view.routes = st.routes.map((r) => (r.max_visits ? { goto: r.goto, max_visits: r.max_visits } : { goto: r.goto }));
    }
    return view;
  });
}

/** Modules referenced by a pipeline, in stage order, without duplicates. */
export function referencedModules(stages: StageView[]): string[] {
  return [...new Set(stages.flatMap((s) => s.lanes.map((l) => l.module)))];
}
