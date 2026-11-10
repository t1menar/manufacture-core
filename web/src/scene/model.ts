import type { Health, LiveModuleMetrics, ModuleStatus, StageKind, StageView } from "../api/types";
import type { Item, ItemState } from "../live/reduce";

/** The 3D conveyor data model from the design (§8.3), plus the source module at the entry. */
export type LaneModel = {
  stepId: string;
  module: string;
  replicas: { desired: number; ready: number };
  health: Health;
  live: { inFlight: number; queueDepth: number; rate: number; p95ms: number; errorRate: number };
};

export type StageModel = { name: string; index: number; kind: StageKind; lanes: LaneModel[] };

export type ItemModel = {
  runId: string;
  stage: number;
  fromStage: number;
  state: ItemState;
  t0: number;
  finishedAt?: number;
  parts: { lane: string; state: ItemState; itemIndex?: number }[];
};

export type ConveyorScene = {
  pipeline: { name: string; revision: number };
  source?: LaneModel;
  stages: StageModel[];
  routes: { from: string; to: string }[];
  items: Map<string, ItemModel>;
};

const ZERO_LIVE = { inFlight: 0, queueDepth: 0, rate: 0, p95ms: 0, errorRate: 0 };

export function laneModel(stepId: string, module: string, modules: Record<string, ModuleStatus>, metrics: Record<string, LiveModuleMetrics>): LaneModel {
  const st = modules[module];
  const m = metrics[module];
  return {
    stepId,
    module,
    replicas: st ? { ...st.replicas } : { desired: 0, ready: 0 },
    health: st ? st.health : "down",
    live: m
      ? { inFlight: m.in_flight, queueDepth: m.queue_depth, rate: m.rate, p95ms: m.p95_ms, errorRate: m.error_rate }
      : st
        ? { ...ZERO_LIVE, inFlight: st.in_flight, queueDepth: st.queue_depth }
        : ZERO_LIVE,
  };
}

export function buildScene(args: {
  name: string;
  revision: number;
  stages: StageView[];
  source?: string;
  modules: Record<string, ModuleStatus>;
  metrics: Record<string, LiveModuleMetrics>;
  items: Record<string, Item>;
}): ConveyorScene {
  const stages = args.stages.map((s) => ({
    name: s.name,
    index: s.index,
    kind: s.kind,
    lanes: s.lanes.map((l) => laneModel(l.step_id, l.module, args.modules, args.metrics)),
  }));
  const routes = args.stages.flatMap((s) => (s.routes ?? []).map((r) => ({ from: s.name, to: r.goto })));
  const items = new Map<string, ItemModel>();
  for (const it of Object.values(args.items)) {
    if (it.pipeline !== args.name) continue;
    items.set(it.runId, {
      runId: it.runId,
      stage: it.stage,
      fromStage: it.fromStage,
      state: it.state,
      t0: it.t0,
      ...(it.finishedAt !== undefined ? { finishedAt: it.finishedAt } : {}),
      parts: Object.values(it.parts).map((p) => ({ lane: p.lane, state: p.state, ...(p.itemIndex !== undefined ? { itemIndex: p.itemIndex } : {}) })),
    });
  }
  const scene: ConveyorScene = { pipeline: { name: args.name, revision: args.revision }, stages, routes, items };
  if (args.source) scene.source = laneModel("source", args.source, args.modules, args.metrics);
  return scene;
}

/** Stable color per module name (hue from FNV-1a). */
export function moduleHue(name: string): number {
  let h = 2166136261;
  for (let i = 0; i < name.length; i++) h = Math.imul(h ^ name.charCodeAt(i), 16777619);
  return ((h >>> 0) % 360) / 360;
}

export const STATE_COLORS: Record<ItemState, string> = {
  queued: "#64748b",
  moving: "#38bdf8",
  processing: "#e0f2fe",
  done: "#4ade80",
  failed: "#f43f5e",
  attention: "#fb923c",
};

export const HEALTH_COLORS: Record<Health, string> = { up: "#22c55e", degraded: "#eab308", down: "#ef4444", unknown: "#64748b" };
