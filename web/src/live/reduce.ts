import type {
  Conveyor,
  ConveyorItem,
  LiveModuleMetrics,
  ModuleStatus,
  Overview,
  ErrorClass,
  RunStatus,
  StepEventData,
  StreamEvent,
} from "../api/types";

/** Where a run (an "item" on the belt) is and what it is doing. */
export type ItemState = "queued" | "moving" | "processing" | "failed" | "attention" | "done";

/** Part of an item at one lane of its current stage (parallel and foreach stages split items into parts). */
export type ItemPart = { lane: string; state: ItemState; itemIndex?: number; progress?: number };

export type Item = {
  runId: string;
  pipeline: string;
  /** Index of the current stage; -1 means "waiting at the entry". */
  stage: number;
  /** Stage the item came from, for the travel animation. */
  fromStage: number;
  state: ItemState;
  parts: Record<string, ItemPart>;
  /** ms timestamp of the last state change (animation clock). */
  t0: number;
  finishedAt?: number;
};

/** `pipeline` is set for run/step entries so the overview can show only the selected pipeline. */
export type FeedEntry = { key: number; at: number; type: StreamEvent["type"]; text: string; tone: "info" | "ok" | "warn" | "error"; pipeline?: string };

export type LiveState = {
  modules: Record<string, ModuleStatus>;
  metrics: Record<string, LiveModuleMetrics>;
  /** Last 60 ticks of throughput per module, for sparklines. */
  rateHistory: Record<string, number[]>;
  items: Record<string, Item>;
  /** ms timestamp of the last failure per module (red flash on its block). */
  lastErrorAt: Record<string, number>;
  /** ms timestamp of the last "something happened" per module (pulse). */
  lastActivityAt: Record<string, number>;
  feed: FeedEntry[];
  lastEventId?: string;
  /** Bumped by events that should refetch REST data (pipeline.applied, reset). */
  epoch: number;
  seq: number;
};

export const MAX_ITEMS = 2000;
export const FEED_SIZE = 80;
const HISTORY = 60;
/** How long finished items stay on the belt for their exit animation. */
export const LINGER_MS = { done: 2600, failed: 3500 } as const;

export function emptyLive(): LiveState {
  return { modules: {}, metrics: {}, rateHistory: {}, items: {}, lastErrorAt: {}, lastActivityAt: {}, feed: [], epoch: 0, seq: 0 };
}

function runStateToItem(status: RunStatus): ItemState {
  switch (status) {
    case "queued":
      return "queued";
    case "succeeded":
      return "done";
    case "failed":
    case "cancelled":
      return "failed";
    case "needs_attention":
      return "attention";
    default:
      return "moving";
  }
}

/** Seeds modules from a REST snapshot (after load or `reset`). */
export function seedFromOverview(s: LiveState, o: Overview): LiveState {
  const modules: Record<string, ModuleStatus> = {};
  const metrics: Record<string, LiveModuleMetrics> = { ...s.metrics };
  for (const m of o.modules) {
    modules[m.name] = m;
    metrics[m.name] = m.live;
  }
  return { ...s, modules, metrics, lastEventId: o.last_event_id || s.lastEventId, epoch: s.epoch + 1 };
}

/** Replaces the items of one pipeline with the runs in flight from `/pipelines/{name}/conveyor`, keeping animation state of known runs. */
export function seedFromConveyor(s: LiveState, c: Conveyor, now: number): LiveState {
  const items: Record<string, Item> = {};
  for (const [k, it] of Object.entries(s.items)) if (it.pipeline !== c.pipeline) items[k] = it;
  for (const ci of c.items) {
    items[ci.run_id] = s.items[ci.run_id] ?? itemFromConveyor(c.pipeline, ci, now);
  }
  return { ...s, items: capItems(items) };
}

export function itemFromConveyor(pipeline: string, ci: ConveyorItem, now: number): Item {
  const parts: Record<string, ItemPart> = {};
  for (const id of ci.step_ids ?? []) parts[id] = { lane: id, state: ci.state === "moving" ? "moving" : ci.state };
  return {
    runId: ci.run_id,
    pipeline,
    stage: ci.state === "queued" ? -1 : ci.stage_index,
    fromStage: ci.state === "queued" ? -1 : ci.stage_index,
    state: ci.state,
    parts,
    t0: now - 10_000,
  };
}

function retriable(c: ErrorClass | undefined): boolean {
  return c === "transient" || c === "timeout" || c === "lost";
}

function partKey(d: StepEventData): string {
  return d.item_index === undefined ? d.step_id : `${d.step_id}#${d.item_index}`;
}

function aggregate(parts: Record<string, ItemPart>): ItemState {
  const states = Object.values(parts).map((p) => p.state);
  if (states.includes("failed")) return "failed";
  if (states.includes("attention")) return "attention";
  if (states.includes("processing")) return "processing";
  if (states.includes("moving")) return "moving";
  return states.length ? "done" : "moving";
}

function pushFeed(s: LiveState, e: Omit<FeedEntry, "key">): FeedEntry[] {
  const next = [{ ...e, key: s.seq }, ...s.feed];
  if (next.length > FEED_SIZE) next.length = FEED_SIZE;
  return next;
}

function capItems(items: Record<string, Item>): Record<string, Item> {
  const keys = Object.keys(items);
  if (keys.length <= MAX_ITEMS) return items;
  // Drop finished first, then the oldest.
  const sorted = keys.sort((a, b) => {
    const A = items[a]!;
    const B = items[b]!;
    const fa = A.finishedAt ? 0 : 1;
    const fb = B.finishedAt ? 0 : 1;
    return fa - fb || A.t0 - B.t0;
  });
  const drop = new Set(sorted.slice(0, keys.length - MAX_ITEMS));
  return Object.fromEntries(Object.entries(items).filter(([k]) => !drop.has(k)));
}

/** Pure reducer: applies one SSE event to the live state. */
export function applyEvent(s0: LiveState, ev: StreamEvent, now: number): LiveState {
  const s: LiveState = { ...s0, seq: s0.seq + 1, lastEventId: ev.id ?? s0.lastEventId };
  switch (ev.type) {
    case "metrics.tick": {
      const rateHistory = { ...s.rateHistory };
      for (const [name, m] of Object.entries(ev.data.modules)) {
        const h = [...(rateHistory[name] ?? []), m.rate];
        if (h.length > HISTORY) h.splice(0, h.length - HISTORY);
        rateHistory[name] = h;
      }
      return { ...s, metrics: { ...s.metrics, ...ev.data.modules }, rateHistory };
    }
    case "module.status": {
      const prev = s.modules[ev.data.name];
      const feed =
        prev && prev.health !== ev.data.health
          ? pushFeed(s, {
              at: now,
              type: ev.type,
              text: `${ev.data.name}: ${prev.health} → ${ev.data.health}`,
              tone: ev.data.health === "up" ? "ok" : ev.data.health === "down" ? "error" : "warn",
            })
          : !prev
            ? pushFeed(s, { at: now, type: ev.type, text: `модуль ${ev.data.name} подключён`, tone: "info" })
            : s.feed;
      return { ...s, modules: { ...s.modules, [ev.data.name]: ev.data }, metrics: { ...s.metrics, [ev.data.name]: ev.data.live }, feed };
    }
    case "pipeline.deleted": {
      const items: Record<string, Item> = {};
      for (const [k, it] of Object.entries(s.items)) if (it.pipeline !== ev.data.name) items[k] = it;
      return { ...s, items, epoch: s.epoch + 1, feed: pushFeed(s, { at: now, type: ev.type, text: `пайплайн ${ev.data.name} удалён`, tone: "warn" }) };
    }
    case "module.deleted": {
      const { [ev.data.name]: _gone, ...modules } = s.modules;
      return { ...s, modules, feed: pushFeed(s, { at: now, type: ev.type, text: `модуль ${ev.data.name} удалён`, tone: "warn" }) };
    }
    case "pipeline.applied":
      return {
        ...s,
        epoch: s.epoch + 1,
        feed: pushFeed(s, { at: now, type: ev.type, text: `пайплайн ${ev.data.name} → ревизия ${ev.data.revision}`, tone: "info" }),
      };
    case "reset":
      return { ...s, epoch: s.epoch + 1 };
    case "run.created": {
      const d = ev.data;
      const item: Item = {
        runId: d.run_id,
        pipeline: d.pipeline,
        stage: -1,
        fromStage: -1,
        state: d.status === "queued" ? "queued" : "moving",
        parts: {},
        t0: now,
      };
      return { ...s, items: capItems({ ...s.items, [d.run_id]: item }) };
    }
    case "run.updated":
    case "run.finished": {
      const d = ev.data;
      const prev = s.items[d.run_id];
      if (!prev) {
        if (ev.type === "run.finished") return s;
        const st = runStateToItem(d.status);
        const fresh: Item = { runId: d.run_id, pipeline: d.pipeline, stage: d.status === "queued" ? -1 : (d.stage_index ?? 0), fromStage: -1, state: st, parts: {}, t0: now };
        return { ...s, items: capItems({ ...s.items, [d.run_id]: fresh }) };
      }
      const state = runStateToItem(d.status);
      if (ev.type === "run.updated" && state === "moving") return s;
      const finished = d.status === "succeeded" || d.status === "failed" || d.status === "cancelled";
      const item: Item = { ...prev, state, t0: now, ...(finished ? { finishedAt: now } : {}) };
      if (d.status === "succeeded") item.fromStage = prev.stage;
      const tone = d.status === "succeeded" ? "ok" : d.status === "needs_attention" ? "warn" : finished ? "error" : "info";
      const feed =
        ev.type === "run.finished" || d.status === "needs_attention"
          ? pushFeed(s, { at: now, type: ev.type, text: `${d.run_id.slice(0, 8)}: ${d.status}`, tone, pipeline: d.pipeline })
          : s.feed;
      return { ...s, items: { ...s.items, [d.run_id]: item }, feed };
    }
    case "step.dispatched":
    case "step.started":
    case "step.progress":
    case "step.succeeded":
    case "step.failed":
    case "step.skipped": {
      const d = ev.data;
      const prev = s.items[d.run_id] ?? {
        runId: d.run_id,
        pipeline: d.pipeline,
        stage: d.stage_index,
        fromStage: d.stage_index - 1,
        state: "moving" as ItemState,
        parts: {},
        t0: now,
      };
      const entering = d.stage_index !== prev.stage;
      const parts = entering ? {} : { ...prev.parts };
      const key = partKey(d);
      const old = parts[key];
      let pstate: ItemState;
      switch (ev.type) {
        case "step.dispatched":
          pstate = "moving";
          break;
        case "step.started":
        case "step.progress":
          pstate = "processing";
          break;
        case "step.succeeded":
        case "step.skipped":
          pstate = "done";
          break;
        case "step.failed":
          // transient/timeout/lost are retried by the core; the run event settles the final state.
          pstate = retriable(d.error?.class) ? "processing" : d.error?.class === "attention" ? "attention" : "failed";
          break;
      }
      parts[key] = {
        lane: d.step_id,
        state: pstate,
        ...(d.item_index !== undefined ? { itemIndex: d.item_index } : {}),
        ...(d.progress !== undefined ? { progress: d.progress } : old?.progress !== undefined ? { progress: old.progress } : {}),
      };
      const state = aggregate(parts);
      const item: Item = {
        ...prev,
        stage: d.stage_index,
        fromStage: entering ? prev.stage : prev.fromStage,
        parts,
        state,
        t0: entering || state !== prev.state ? now : prev.t0,
      };
      const lastActivityAt = { ...s.lastActivityAt, [d.module]: now };
      let lastErrorAt = s.lastErrorAt;
      let feed = s.feed;
      if (ev.type === "step.failed") {
        lastErrorAt = { ...s.lastErrorAt, [d.module]: now };
        feed = pushFeed(s, {
          at: now,
          type: ev.type,
          text: `${d.module} · ${d.stage}: ${d.error?.code ?? "error"}${d.error?.message ? ` — ${d.error.message}` : ""}`,
          pipeline: d.pipeline,
          tone: retriable(d.error?.class) ? "warn" : "error",
        });
      }
      return { ...s, items: capItems({ ...s.items, [d.run_id]: item }), lastActivityAt, lastErrorAt, feed };
    }
  }
}

/** Removes items whose exit animation is over. Returns the same object when nothing changed. */
export function pruneItems(s: LiveState, now: number): LiveState {
  let changed = false;
  const items: Record<string, Item> = {};
  for (const [k, it] of Object.entries(s.items)) {
    const linger = it.state === "done" ? LINGER_MS.done : LINGER_MS.failed;
    if (it.finishedAt !== undefined && now - it.finishedAt > linger) {
      changed = true;
      continue;
    }
    items[k] = it;
  }
  return changed ? { ...s, items } : s;
}
