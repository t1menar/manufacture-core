import { parse } from "yaml";

import type {
  Attempt,
  LiveModuleMetrics,
  ModuleMetrics,
  ModuleDetail,
  ModuleStatus,
  Replica,
  Overview,
  PipelineDetail,
  PipelineRevision,
  PipelineSummary,
  Run,
  RunContext,
  RunStatus,
  Snapshot,
  Conveyor,
  StageView,
  Step,
  ErrorInfo,
  RunEventData,
  StepEventData,
  StreamEvent,
} from "../api/types";
import { compileStages, type PipelineSpec } from "../lib/spec";
import { validateSpecText } from "../lib/validate";
import { benchSeeds, GEO_YAML, HELLO_YAML, MODULE_SEEDS, stressYaml, VIDEO_YAML, type ModuleSeed } from "./data";

/** Deterministic PRNG (mulberry32) so tests and screenshots are reproducible. */
export function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type PipelineRev = PipelineRevision & { yaml: string; spec: Record<string, unknown>; stages: StageView[] };
type SimPipeline = { name: string; revisions: PipelineRev[]; spawnEveryMs: number; nextSpawn: number; maxRuns: number; source?: string };

type SimModule = {
  status: ModuleStatus;
  seed: ModuleSeed;
  /** Completed steps: [t, durationMs, ok]. */
  samples: [number, number, boolean][];
  busy: number;
  rampAt: number;
};

type Part = {
  step: Step;
  phase: "queued" | "dispatched" | "running" | "done";
  at: number;
  due: number;
};

type SimRun = {
  run: Run;
  steps: Step[];
  parts: Part[];
  data: Record<string, unknown>;
  outputs: Record<string, unknown>;
  snapshots: (Snapshot & { body: RunContext })[];
  visits: Record<string, number>;
};

type LoggedEvent = { id: string; ev: StreamEvent };

export type SimOptions = {
  seed?: number;
  /** Time multiplier for durations: 2 = everything twice as fast. */
  speed?: number;
  /** Include the 20-module stress pipeline. */
  stress?: boolean;
  /** Start with modules offline and let them "dock" one by one. */
  docking?: boolean;
  now?: number;
};

const HEX = "0123456789abcdef";

export class Simulator {
  readonly startedAt: number;
  now: number;
  private rand: () => number;
  private speed: number;
  private pipelines = new Map<string, SimPipeline>();
  private modules = new Map<string, SimModule>();
  private runs = new Map<string, SimRun>();
  private initialData = new Map<string, Record<string, unknown>>();
  private order: string[] = [];
  private log: LoggedEvent[] = [];
  private nextId = 1;
  private runSeq = 1;
  private subs = new Set<(e: LoggedEvent) => void>();
  private lastTick = 0;
  private timer: ReturnType<typeof setInterval> | undefined;

  constructor(opts: SimOptions = {}) {
    this.rand = rng(opts.seed ?? 42);
    this.speed = opts.speed ?? 1;
    this.now = opts.now ?? Date.now();
    this.startedAt = this.now;
    const seeds = [...MODULE_SEEDS, ...(opts.stress ? benchSeeds() : [])];
    seeds.forEach((s, i) => {
      const ready = opts.docking ? 0 : s.replicas;
      this.modules.set(s.name, {
        seed: s,
        samples: [],
        busy: 0,
        rampAt: this.now + (opts.docking ? 400 + i * 260 : 0),
        status: {
          name: s.name,
          revision: 1,
          image: s.image,
          runtime: s.external ? "external" : "auto",
          driver: s.external ? "external" : "docker",
          replicas: { desired: s.replicas, ready },
          health: ready > 0 ? "up" : "down",
          conditions: [{ type: "Ready", status: ready > 0 ? "True" : "False", ...(ready > 0 ? {} : { reason: "Starting" }) }],
          last_heartbeat_at: new Date(this.now).toISOString(),
          queue_depth: 0,
          dlq_depth: s.failRate > 0.02 ? Math.floor(this.rand() * 4) : 0,
          in_flight: 0,
          sdk_version: "0.1.0",
          contract_version: "0.2.1",
          live: zeroLive(),
        },
      });
    });
    this.addPipeline(HELLO_YAML, 1200);
    this.addPipeline(GEO_YAML, 3500);
    this.addPipeline(VIDEO_YAML, 5000);
    if (opts.stress) this.addPipeline(stressYaml(), 180);
  }

  // ---------------------------------------------------------------- lifecycle

  start(intervalMs = 100): void {
    if (this.timer) return;
    this.lastTick = Date.now();
    this.timer = setInterval(() => {
      const t = Date.now();
      this.advance(t - this.lastTick);
      this.lastTick = t;
    }, intervalMs);
  }

  stop(): void {
    clearInterval(this.timer);
    this.timer = undefined;
  }

  subscribe(fn: (e: LoggedEvent) => void): () => void {
    this.subs.add(fn);
    return () => this.subs.delete(fn);
  }

  /** Events after `lastId`, or null when that id is older than the retained log (client must reset). */
  since(lastId: string | undefined): LoggedEvent[] | null {
    if (!lastId) return [];
    const n = Number(lastId);
    const first = this.log[0];
    if (!first || Number.isNaN(n)) return [];
    if (n < Number(first.id) - 1) return null;
    return this.log.filter((e) => Number(e.id) > n);
  }

  get lastEventId(): string {
    return String(this.nextId - 1);
  }

  /** Advances simulated time by `dt` ms (tests call it directly). */
  advance(dt: number): void {
    const end = this.now + dt;
    const stepMs = 50;
    while (this.now < end) {
      this.now = Math.min(end, this.now + stepMs);
      this.step();
      if (this.now - this.lastMetrics >= 1000) {
        this.lastMetrics = this.now;
        this.emitMetrics();
      }
    }
  }

  private lastMetrics = 0;

  // ---------------------------------------------------------------- model

  private addPipeline(text: string, spawnEveryMs: number): void {
    const yaml = text.split(/^---\s*$/m).find((d) => /^kind: Pipeline$/m.test(d))?.replace(/^\n+/, "") ?? text;
    const doc = parse(yaml) as { metadata: { name: string }; spec: PipelineSpec & { concurrency?: { max_runs?: number } } };
    const name = doc.metadata.name;
    const rev = this.makeRevision(1, yaml, doc, "bootstrap", "system");
    this.pipelines.set(name, {
      name,
      revisions: [rev],
      spawnEveryMs,
      nextSpawn: this.now + 300 + this.rand() * spawnEveryMs,
      maxRuns: doc.spec.concurrency?.max_runs ?? 10,
      ...(doc.spec.trigger?.source ? { source: doc.spec.trigger.source } : {}),
    });
  }

  private makeRevision(revision: number, yaml: string, res: Record<string, unknown>, source: PipelineRev["source"], by: string): PipelineRev {
    const spec = res.spec as PipelineSpec;
    return {
      revision,
      yaml,
      spec: res,
      stages: compileStages(spec),
      spec_hash: `sha256:${hash(yaml)}`,
      applied_at: new Date(this.now - (revision === 1 ? 86_400_000 * 2 : 0)).toISOString(),
      applied_by: by,
      source,
    };
  }

  private current(p: SimPipeline): PipelineRev {
    return p.revisions[p.revisions.length - 1]!;
  }

  private emit(ev: StreamEvent, withId = true): void {
    const id = withId ? String(this.nextId++) : "";
    const e = { id, ev: withId ? { ...ev, id } : ev };
    if (withId) {
      this.log.push(e);
      if (this.log.length > 5000) this.log.splice(0, this.log.length - 5000);
    }
    for (const s of this.subs) s(e);
  }

  private emitRun(e: { type: "run.created" | "run.updated" | "run.finished"; data: Omit<RunEventData, "at"> }): void {
    this.emit({ type: e.type, data: { ...e.data, at: iso(this.now) } });
  }

  private emitStep(e: { type: StepEventData extends never ? never : "step.dispatched" | "step.started" | "step.progress" | "step.succeeded" | "step.failed" | "step.skipped"; data: Omit<StepEventData, "at"> }): void {
    this.emit({ type: e.type, data: { ...e.data, at: iso(this.now) } });
  }

  private dur(ms: number): number {
    // log-normal-ish jitter around the module latency
    const f = Math.exp((this.rand() - 0.5) * 0.9);
    return (ms * f) / this.speed;
  }

  private step(): void {
    // module ramp-up (docking) and scaling
    for (const m of this.modules.values()) {
      const st = m.status;
      if (st.replicas.ready !== st.replicas.desired && this.now >= m.rampAt) {
        st.replicas = { ...st.replicas, ready: st.replicas.ready + Math.sign(st.replicas.desired - st.replicas.ready) };
        m.rampAt = this.now + 700 / this.speed;
        this.refreshHealth(m, true);
      }
    }
    // spawn
    for (const p of this.pipelines.values()) {
      if (this.now >= p.nextSpawn) {
        p.nextSpawn = this.now + p.spawnEveryMs * (0.6 + this.rand() * 0.8);
        this.createRun(p.name, p.source ? { kind: "source", by: p.source } : { kind: "api", by: "scheduler" });
      }
    }
    // admission
    for (const p of this.pipelines.values()) {
      let active = this.countRuns(p.name, "running");
      for (const id of this.order) {
        if (active >= p.maxRuns) break;
        const r = this.runs.get(id)!;
        if (r.run.pipeline === p.name && r.run.status === "queued") {
          this.setRunStatus(r, "running");
          this.enterStage(r, 0);
          active++;
        }
      }
    }
    // parts
    for (const r of this.runs.values()) {
      if (r.run.status !== "running") continue;
      for (const part of r.parts) this.stepPart(r, part);
      if (r.parts.length && r.parts.every((p) => p.phase === "done")) this.finishStage(r);
    }
    for (const m of this.modules.values()) {
      const q = this.queued(m.status.name);
      if (q !== m.status.queue_depth || m.busy !== m.status.in_flight) {
        m.status.queue_depth = q;
        m.status.in_flight = m.busy;
      }
    }
  }

  private queued(module: string): number {
    let n = 0;
    for (const r of this.runs.values()) for (const p of r.parts) if (p.phase === "queued" && p.step.module === module) n++;
    return n;
  }

  private countRuns(pipeline: string, status: RunStatus): number {
    let n = 0;
    for (const r of this.runs.values()) if (r.run.pipeline === pipeline && r.run.status === status) n++;
    return n;
  }

  private capacity(m: SimModule): number {
    return m.status.replicas.ready * m.seed.concurrency;
  }

  private stepPart(r: SimRun, part: Part): void {
    const m = this.modules.get(part.step.module);
    if (!m) return;
    const base = this.stepEvent(r, part);
    switch (part.phase) {
      case "queued":
        if (m.busy < this.capacity(m)) {
          m.busy++;
          part.phase = "dispatched";
          part.at = this.now;
          part.due = this.now + (80 + this.rand() * 300) / this.speed;
          part.step.status = "dispatched";
          const attempt = this.newAttempt(part.step, m);
          this.emitStep({ type: "step.dispatched", data: { ...base, attempt: attempt.number, attempt_id: attempt.attempt_id } });
        }
        break;
      case "dispatched":
        if (this.now >= part.due) {
          part.phase = "running";
          part.at = this.now;
          part.due = this.now + this.dur(m.seed.latencyMs);
          part.step.status = "running";
          part.step.started_at ??= iso(this.now);
          const a = part.step.attempts[part.step.attempts.length - 1]!;
          a.status = "running";
          a.started_at = iso(this.now);
          this.emitStep({ type: "step.started", data: { ...base, attempt: a.number, attempt_id: a.attempt_id } });
        }
        break;
      case "running": {
        if (this.now < part.due) {
          if (part.due - part.at > 2500 && this.rand() < 0.01) {
            const progress = Math.min(0.99, (this.now - part.at) / (part.due - part.at));
            this.emitStep({ type: "step.progress", data: { ...base, progress: Math.round(progress * 100) / 100 } });
          }
          break;
        }
        m.busy = Math.max(0, m.busy - 1);
        const a = part.step.attempts[part.step.attempts.length - 1]!;
        a.finished_at = iso(this.now);
        const roll = this.rand();
        const sd = m.seed;
        const ms = this.now - part.at;
        if (roll < sd.failRate) {
          const err: ErrorInfo = { class: "fatal", code: "handler_error", message: sample(this.rand, FATAL) };
          a.status = "failed";
          a.error = err;
          part.step.status = "failed";
          part.step.finished_at = iso(this.now);
          part.phase = "done";
          m.samples.push([this.now, ms, false]);
          this.emitStep({ type: "step.failed", data: { ...base, error: err } });
          this.refreshHealth(m, false);
          const optional = r.run.pipeline === "geo-analysis" && part.step.module === "geo-roads";
          if (!optional) this.failRun(r, err);
        } else if (roll < sd.failRate + sd.transientRate && part.step.attempts.length < 3) {
          const err: ErrorInfo = { class: "transient", code: "upstream_timeout", message: sample(this.rand, TRANSIENT) };
          a.status = "failed";
          a.error = err;
          m.samples.push([this.now, ms, false]);
          this.emitStep({ type: "step.failed", data: { ...base, error: err } });
          part.phase = "queued";
          part.step.status = "pending";
        } else if (roll < sd.failRate + sd.transientRate + (sd.attentionRate ?? 0)) {
          const err: ErrorInfo = { class: "attention", code: "needs_review", message: "Оператор должен решить вручную" };
          a.status = "failed";
          a.error = err;
          part.step.status = "failed";
          part.phase = "done";
          m.samples.push([this.now, ms, true]);
          this.emitStep({ type: "step.failed", data: { ...base, error: err } });
          r.run.error = err;
          this.setRunStatus(r, "needs_attention");
        } else {
          a.status = "succeeded";
          part.step.status = "succeeded";
          part.step.finished_at = iso(this.now);
          part.phase = "done";
          m.samples.push([this.now, ms, true]);
          this.applyOutput(r, part.step);
          this.emitStep({ type: "step.succeeded", data: base });
        }
        if (m.samples.length > 4000) m.samples.splice(0, 1000);
        break;
      }
      case "done":
        break;
    }
  }

  private stepEvent(r: SimRun, part: Part) {
    return {
      run_id: r.run.id,
      pipeline: r.run.pipeline,
      stage: part.step.stage,
      stage_index: part.step.stage_index,
      step_id: part.step.step_id,
      module: part.step.module,
      ...(part.step.item_index !== undefined ? { item_index: part.step.item_index } : {}),
    };
  }

  private newAttempt(step: Step, m: SimModule): Attempt {
    const a: Attempt = {
      attempt_id: uuid(this.rand),
      number: step.attempts.length + 1,
      status: "dispatched",
      replica_id: `mf-${m.status.name}-${Math.floor(this.rand() * Math.max(1, m.status.replicas.ready))}`,
    };
    step.attempts.push(a);
    return a;
  }

  private applyOutput(r: SimRun, step: Step): void {
    const p = r.run.pipeline;
    let out: Record<string, unknown>;
    if (p === "hello-world") {
      const text = typeof r.data.text === "string" ? r.data.text : "";
      const suffix = step.module === "hello-hello" ? "hello" : step.module === "hello-world" ? " world" : "!";
      out = { text: text + suffix };
      r.data = { ...r.data, ...out };
    } else if (step.module === "geo-ingest") {
      out = { scene: this.blob(r, step, "scene.tif", "image/tiff", 73_400_320) };
      r.data = { ...r.data, ...out };
    } else if (step.module === "geo-split") {
      out = { tiles: Array.from({ length: 4 }, (_, i) => ({ id: i, size: 512 })) };
    } else if (step.module === "geo-analyze") {
      out = { tile: step.item_index, mean: Math.round(this.rand() * 1000) / 1000 };
    } else if (step.module === "video-render") {
      out = { video: this.blob(r, step, "final.mp4", "video/mp4", 18_874_368), duration_s: 33 };
      r.data = { ...r.data, ...out };
    } else if (step.module === "review-telegram") {
      out = { decision: this.rand() < 0.3 ? "rejected" : "approved" };
      r.data = { ...r.data, review: out };
    } else if (step.module === "publish-instagram") {
      out = { permalink: `https://www.instagram.com/reel/${uuid(this.rand).slice(0, 11)}/` };
      r.data = { ...r.data, published: out };
    } else {
      out = { ok: true, ms: Math.round(this.rand() * 900) };
    }
    const key = step.item_index === undefined ? step.step_id : step.step_id;
    if (step.item_index === undefined) r.outputs[key] = { status: "succeeded", output: out };
    else {
      const prev = (r.outputs[key] as { outputs?: unknown[] } | undefined)?.outputs ?? [];
      const outputs = [...prev];
      outputs[step.item_index] = out;
      r.outputs[key] = { status: "running", outputs };
    }
  }

  private blob(r: SimRun, step: Step, file: string, contentType: string, size: number) {
    return {
      $blob: `s3://mf-blobs/runs/${r.run.id}/${step.step_id}/${step.attempts.length}/${file}`,
      sha256: hash(file + r.run.id).repeat(4).slice(0, 64),
      size,
      content_type: contentType,
    };
  }

  private refreshHealth(m: SimModule, force: boolean): void {
    const st = m.status;
    const recent = m.samples.filter((s) => this.now - s[0] < 60_000);
    const errRate = recent.length ? recent.filter((s) => !s[2]).length / recent.length : 0;
    const health: ModuleStatus["health"] =
      st.replicas.desired > 0 && st.replicas.ready === 0 ? "down" : st.replicas.ready < st.replicas.desired || errRate > 0.2 ? "degraded" : "up";
    if (!force && health === st.health) return;
    st.health = health;
    st.conditions = [
      { type: "Ready", status: st.replicas.ready > 0 ? "True" : "False", ...(st.replicas.ready < st.replicas.desired ? { reason: "Scaling" } : {}) },
      ...(errRate > 0.2 ? [{ type: "ErrorRate", status: "True" as const, reason: "High", message: `${Math.round(errRate * 100)}% ошибок за минуту` }] : []),
    ];
    st.last_heartbeat_at = iso(this.now);
    this.emit({ type: "module.status", data: { ...st, replicas: { ...st.replicas } } });
  }

  createRun(pipeline: string, trigger: Run["trigger"], data?: Record<string, unknown>): SimRun | null {
    const p = this.pipelines.get(pipeline);
    if (!p) return null;
    const id = runId(this.runSeq++, this.rand);
    const rev = this.current(p);
    const run: Run = {
      id,
      pipeline,
      revision: rev.revision,
      status: "queued",
      stage_index: 0,
      stage: rev.stages[0]?.name ?? "",
      trigger,
      created_at: iso(this.now),
    };
    const sim: SimRun = {
      run,
      steps: [],
      parts: [],
      data: data ?? (pipeline === "hello-world" ? { text: "" } : pipeline === "video" ? { idea: { title: sample(this.rand, IDEAS) } } : { bbox: [17.9, 59.2, 18.2, 59.4] }),
      outputs: {},
      snapshots: [],
      visits: {},
    };
    this.initialData.set(id, structuredClone(sim.data));
    this.runs.set(id, sim);
    this.order.push(id);
    this.trimRuns();
    this.emitRun({ type: "run.created", data: { run_id: id, pipeline, revision: rev.revision, status: "queued", stage_index: 0, stage: run.stage ?? "" } });
    return sim;
  }

  private trimRuns(): void {
    if (this.order.length <= 4000) return;
    const drop = this.order.splice(0, this.order.length - 4000);
    for (const id of drop) {
      this.runs.delete(id);
      this.initialData.delete(id);
    }
  }

  private stagesOf(r: SimRun): StageView[] {
    const p = this.pipelines.get(r.run.pipeline)!;
    return (p.revisions.find((x) => x.revision === r.run.revision) ?? this.current(p)).stages;
  }

  private enterStage(r: SimRun, index: number): void {
    const stages = this.stagesOf(r);
    const st = stages[index];
    if (!st) return;
    r.run.stage_index = index;
    r.run.stage = st.name;
    r.visits[st.name] = (r.visits[st.name] ?? 0) + 1;
    const visits = r.visits[st.name]!;
    const mk = (lane: StageView["lanes"][number], item?: number): Part => {
      const step: Step = {
        stage: st.name,
        stage_index: index,
        step_id: lane.step_id,
        module: lane.module,
        ...(item !== undefined ? { item_index: item } : {}),
        status: "pending",
        visits,
        attempts: [],
      };
      r.steps.push(step);
      return { step, phase: "queued", at: this.now, due: this.now };
    };
    if (st.kind === "foreach") {
      const n = 3 + Math.floor(this.rand() * 3);
      r.parts = Array.from({ length: n }, (_, i) => mk(st.lanes[0]!, i));
    } else {
      r.parts = st.lanes.map((l) => mk(l));
    }
    this.emitRun({ type: "run.updated", data: { run_id: r.run.id, pipeline: r.run.pipeline, revision: r.run.revision, status: r.run.status, stage_index: index, stage: st.name } });
  }

  private finishStage(r: SimRun): void {
    const stages = this.stagesOf(r);
    const st = stages[r.run.stage_index]!;
    for (const part of r.parts) {
      const o = r.outputs[part.step.step_id] as { status?: string } | undefined;
      if (o && part.step.item_index !== undefined) o.status = "succeeded";
    }
    r.parts = [];
    const body: RunContext = { run: { id: r.run.id, pipeline: r.run.pipeline, revision: r.run.revision, trigger: r.run.trigger }, data: structuredClone(r.data), steps: structuredClone(r.outputs) };
    const text = JSON.stringify(body);
    r.snapshots.push({ after_stage: r.run.stage_index, sha256: hash(text).repeat(4).slice(0, 64), created_at: iso(this.now), size: text.length, body });
    // routes
    for (const route of st.routes ?? []) {
      const rejected = (r.data.review as { decision?: string } | undefined)?.decision === "rejected";
      const target = stages.findIndex((s) => s.name === route.goto);
      if (rejected && target >= 0 && (r.visits[route.goto] ?? 0) < (route.max_visits ?? 1)) {
        this.enterStage(r, target);
        return;
      }
    }
    if (r.run.stage_index + 1 >= stages.length) {
      this.setRunStatus(r, "succeeded");
      return;
    }
    this.enterStage(r, r.run.stage_index + 1);
  }

  private failRun(r: SimRun, err: ErrorInfo): void {
    for (const p of r.parts) {
      if (p.phase === "running" || p.phase === "dispatched") {
        const m = this.modules.get(p.step.module);
        if (m) m.busy = Math.max(0, m.busy - 1);
      }
      if (p.phase !== "done") {
        p.step.status = "cancelled";
        p.phase = "done";
      }
    }
    r.parts = [];
    r.run.error = err;
    this.setRunStatus(r, "failed");
  }

  private setRunStatus(r: SimRun, status: RunStatus): void {
    r.run.status = status;
    if (status === "running") r.run.started_at ??= iso(this.now);
    const terminal = status === "succeeded" || status === "failed" || status === "cancelled";
    if (terminal) r.run.finished_at = iso(this.now);
    this.emitRun({
      type: terminal ? "run.finished" : "run.updated",
      data: { run_id: r.run.id, pipeline: r.run.pipeline, revision: r.run.revision, status, stage_index: r.run.stage_index, stage: r.run.stage ?? "" },
    });
  }

  private liveMetrics(m: SimModule): LiveModuleMetrics {
    const win = m.samples.filter((s) => this.now - s[0] < 10_000);
    const durs = win.map((s) => s[1]).sort((a, b) => a - b);
    const q = (p: number) => (durs.length ? durs[Math.min(durs.length - 1, Math.floor(p * durs.length))]! : 0);
    return {
      rate: Math.round((win.length / 10) * 100) / 100,
      p50_ms: Math.round(q(0.5)),
      p95_ms: Math.round(q(0.95)),
      in_flight: m.busy,
      queue_depth: m.status.queue_depth,
      error_rate: win.length ? Math.round((win.filter((s) => !s[2]).length / win.length) * 1000) / 1000 : 0,
    };
  }

  private emitMetrics(): void {
    const modules: Record<string, LiveModuleMetrics> = {};
    for (const m of this.modules.values()) {
      modules[m.status.name] = this.liveMetrics(m);
      m.status.live = modules[m.status.name]!;
      if (this.rand() < 0.05) this.refreshHealth(m, false);
    }
    let active = 0;
    let queued = 0;
    for (const r of this.runs.values()) {
      if (r.run.status === "running") active++;
      else if (r.run.status === "queued") queued++;
    }
    this.emit({ type: "metrics.tick", data: { ts: this.now, modules, runs: { active, queued } } }, false);
  }

  // ---------------------------------------------------------------- REST views

  overview(): Overview {
    const active = [...this.runs.values()].filter((r) => r.run.status === "running" || r.run.status === "queued" || r.run.status === "needs_attention");
    return {
      pipelines: this.pipelineSummaries(),
      modules: this.moduleList(),
      runs: {
        active: active.filter((r) => r.run.status === "running").length,
        queued: active.filter((r) => r.run.status === "queued").length,
        needs_attention: active.filter((r) => r.run.status === "needs_attention").length,
        succeeded_24h: [...this.runs.values()].filter((r) => r.run.status === "succeeded").length,
        failed_24h: [...this.runs.values()].filter((r) => r.run.status === "failed").length,
      },
      last_event_id: this.lastEventId,
    };
  }

  /** GET /pipelines/{name}/conveyor: stages with live lane state plus the runs in flight. */
  conveyor(name: string): Conveyor | null {
    const p = this.pipelines.get(name);
    if (!p) return null;
    const cur = this.current(p);
    const items: Conveyor["items"] = [];
    for (const r of this.runs.values()) {
      if (r.run.pipeline !== name) continue;
      const st = r.run.status;
      if (st !== "running" && st !== "queued" && st !== "needs_attention") continue;
      const busy = r.parts.filter((x) => x.phase === "running" || x.phase === "dispatched");
      items.push({
        run_id: r.run.id,
        stage_index: r.run.stage_index,
        step_ids: [...new Set(busy.map((x) => x.step.step_id))],
        state: st === "queued" ? "queued" : st === "needs_attention" ? "attention" : busy.some((x) => x.phase === "running") ? "processing" : "moving",
        since: iso(this.now),
      });
    }
    const overflow = Math.max(0, items.length - 2000);
    return {
      pipeline: name,
      revision: cur.revision,
      stages: cur.stages.map((s) => ({
        ...s,
        live: Object.fromEntries(
          s.lanes.map((l) => {
            const m = this.modules.get(l.module);
            return [l.step_id, { health: m?.status.health ?? "unknown", replicas: { ...(m?.status.replicas ?? { desired: 0, ready: 0 }) }, metrics: m ? this.liveMetrics(m) : zeroLive() }];
          }),
        ),
      })),
      items: items.slice(0, 2000),
      overflow,
      last_event_id: this.lastEventId,
    };
  }

  pipelineSummaries(): PipelineSummary[] {
    return [...this.pipelines.values()].map((p) => {
      const cur = this.current(p);
      return {
        name: p.name,
        revision: cur.revision,
        stages: cur.stages,
        updated_at: cur.applied_at,
        runs: {
          active: this.countRuns(p.name, "running"),
          queued: this.countRuns(p.name, "queued"),
          needs_attention: this.countRuns(p.name, "needs_attention"),
          succeeded_24h: this.countRuns(p.name, "succeeded"),
          failed_24h: this.countRuns(p.name, "failed"),
        },
      };
    });
  }

  pipelineDetail(name: string, revision?: number): PipelineDetail | null {
    const p = this.pipelines.get(name);
    if (!p) return null;
    const rev = revision === undefined ? this.current(p) : p.revisions.find((r) => r.revision === revision);
    if (!rev) return null;
    return { name, revision: rev.revision, yaml: rev.yaml, spec: rev.spec, stages: rev.stages, updated_at: rev.applied_at, applied_by: rev.applied_by, source: rev.source };
  }

  revisions(name: string): PipelineRevision[] | null {
    const p = this.pipelines.get(name);
    if (!p) return null;
    return p.revisions.map(({ revision, spec_hash, applied_at, applied_by, source }) => ({ revision, spec_hash, applied_at, applied_by, source })).reverse();
  }

  moduleList(): ModuleStatus[] {
    return [...this.modules.values()].map((m) => ({ ...m.status, replicas: { ...m.status.replicas } }));
  }

  module(name: string): ModuleStatus | null {
    const m = this.modules.get(name);
    return m ? { ...m.status, replicas: { ...m.status.replicas } } : null;
  }

  moduleDetail(name: string): ModuleDetail | null {
    const m = this.modules.get(name);
    if (!m) return null;
    const st = m.status;
    const spec = {
      apiVersion: "manufacture.dev/v1",
      kind: "Module",
      metadata: { name },
      spec: { ...(st.image ? { image: st.image } : {}), runtime: st.runtime, replicas: st.replicas.desired, concurrency: m.seed.concurrency },
    };
    const yaml = `apiVersion: manufacture.dev/v1\nkind: Module\nmetadata: {name: ${name}}\nspec:\n${st.image ? `  image: ${st.image}\n` : ""}  runtime: ${st.runtime}\n  replicas: ${st.replicas.desired}\n  concurrency: ${m.seed.concurrency}\n`;
    const replica_list: Replica[] = Array.from({ length: Math.max(st.replicas.desired, st.replicas.ready) }, (_, i) => ({
      id: `mf-${name}-${i}`,
      state: i < st.replicas.ready ? "ready" : "starting",
      sdk_version: "0.1.0",
      version: "0.1.0",
      last_seen_at: iso(this.now - Math.floor(this.rand() * 4000)),
      in_flight: i < st.replicas.ready ? Math.round(m.busy / Math.max(1, st.replicas.ready)) : 0,
    }));
    return {
      ...this.module(name)!,
      spec,
      yaml,
      replica_list,
      manifest: { sdk_version: "0.1.0", contract_version: "0.2.1", input_schema: { type: "object" }, output_schema: { type: "object" } },
      updated_at: iso(this.startedAt - 3_600_000),
      applied_by: "admin",
      source: "bootstrap",
    };
  }

  scale(name: string, replicas: number): ModuleStatus | null {
    const m = this.modules.get(name);
    if (!m) return null;
    m.status.replicas = { ...m.status.replicas, desired: Math.max(0, Math.min(50, Math.round(replicas))) };
    m.rampAt = this.now + 300 / this.speed;
    this.refreshHealth(m, true);
    return this.module(name);
  }

  metrics(name: string, range: string): ModuleMetrics | null {
    const m = this.modules.get(name);
    if (!m) return null;
    const rangeMs = parseRange(range);
    const stepMs = Math.max(15_000, Math.round(rangeMs / 120));
    const r = rng(hashNum(name));
    const base = (1000 / m.seed.latencyMs) * Math.min(1, m.seed.replicas * 0.6);
    const series: ModuleMetrics["series"] = { throughput: [], latency_p50_ms: [], latency_p95_ms: [], errors: [], in_flight: [], queue_depth: [] };
    for (let t = this.now - rangeMs; t <= this.now; t += stepMs) {
      const wave = 1 + 0.35 * Math.sin(t / 900_000 + hashNum(name)) + (r() - 0.5) * 0.25;
      const ts = Math.floor(t / 1000);
      const thr = Math.max(0, base * wave);
      const p50 = m.seed.latencyMs * (0.9 + r() * 0.2) * (wave > 1.2 ? 1.25 : 1);
      series.throughput.push([ts, round(thr)]);
      series.latency_p50_ms.push([ts, Math.round(p50)]);
      series.latency_p95_ms.push([ts, Math.round(p50 * (1.6 + r() * 0.6))]);
      series.errors.push([ts, round(Math.max(0, thr * (m.seed.failRate + m.seed.transientRate) * (r() * 2)))]);
      series.in_flight.push([ts, Math.round(Math.min(m.seed.replicas * m.seed.concurrency, thr * (m.seed.latencyMs / 1000)))]);
      series.queue_depth.push([ts, Math.max(0, Math.round((wave - 1.1) * 20 * r()))]);
    }
    const live = this.liveMetrics(m);
    const ts = Math.floor(this.now / 1000);
    series.in_flight.push([ts, live.in_flight]);
    series.queue_depth.push([ts, live.queue_depth]);
    return { range, step: `${Math.round(stepMs / 1000)}s`, series };
  }

  listRuns(f: { pipeline?: string | null; status?: string | null; q?: string | null; cursor?: string | null; limit?: number }): { items: Run[]; next_cursor?: string } {
    const limit = Math.min(200, f.limit ?? 50);
    let ids = [...this.order].reverse();
    if (f.cursor) {
      const i = ids.indexOf(f.cursor);
      ids = i >= 0 ? ids.slice(i + 1) : [];
    }
    const out: Run[] = [];
    let last: string | undefined;
    for (const id of ids) {
      const r = this.runs.get(id)!.run;
      if (f.pipeline && r.pipeline !== f.pipeline) continue;
      if (f.status && r.status !== f.status) continue;
      if (f.q && !r.id.includes(f.q)) continue;
      out.push({ ...r });
      last = id;
      if (out.length >= limit) break;
    }
    return out.length >= limit && last ? { items: out, next_cursor: last } : { items: out };
  }

  runDetail(id: string) {
    const r = this.runs.get(id);
    if (!r) return null;
    return {
      run: { ...r.run },
      steps: r.steps.map((s) => ({ ...s, attempts: s.attempts.map((a) => ({ ...a })) })),
      context: { run: { id: r.run.id, pipeline: r.run.pipeline, revision: r.run.revision, trigger: r.run.trigger }, data: structuredClone(r.data), steps: structuredClone(r.outputs) },
      snapshots: r.snapshots.map(({ body: _b, ...s }) => s),
    };
  }

  snapshot(id: string, afterStage: number): RunContext | null {
    const r = this.runs.get(id);
    const s = r?.snapshots.filter((x) => x.after_stage === afterStage).pop();
    return s ? s.body : null;
  }

  runAction(id: string, action: string): Run | { conflict: string } | null {
    const r = this.runs.get(id);
    if (!r) return null;
    const st = r.run.status;
    if (action === "cancel") {
      if (st === "succeeded" || st === "failed" || st === "cancelled") return { conflict: `run is already ${st}` };
      this.failRun(r, { class: "fatal", code: "cancelled", message: "Отменено оператором" });
      r.run.status = "cancelled";
      this.emitRun({ type: "run.finished", data: { run_id: id, pipeline: r.run.pipeline, revision: r.run.revision, status: "cancelled" } });
      return { ...r.run };
    }
    if (action === "resume") {
      if (st !== "needs_attention") return { conflict: `cannot resume a run that is ${st}` };
      delete r.run.error;
      this.setRunStatus(r, "running");
      this.enterStage(r, r.run.stage_index);
      return { ...r.run };
    }
    if (action === "retry") {
      if (st !== "failed" && st !== "cancelled") return { conflict: `cannot retry a run that is ${st}` };
      const copy = this.createRun(r.run.pipeline, { kind: "retry", by: r.run.id }, structuredClone(this.initialData.get(r.run.id) ?? {}));
      return copy ? { ...copy.run } : { conflict: "pipeline is gone" };
    }
    return { conflict: `unknown action ${action}` };
  }

  apply(text: string, dryRun: boolean): { status: number; body: unknown } {
    const { diagnostics, resources } = validateSpecText(text, this.modules.keys());
    const errors = diagnostics.filter((d) => d.severity === "error");
    if (errors.length) {
      return {
        status: 422,
        body: {
          type: "about:blank",
          title: "Спек не прошёл проверку",
          status: 422,
          code: "validation_failed",
          errors: errors.map((e) => ({ severity: "error", source: "request", line: e.line, path: toPointer(e.path), message: e.message, ...(resources[e.doc] ? { resource: `${resources[e.doc]!.kind}/${resources[e.doc]!.name}` } : {}) })),
        },
      };
    }
    const results: { kind: "Module" | "Pipeline"; name: string; action: "created" | "updated" | "unchanged"; revision: number }[] = [];
    const docs = text.split(/^---\s*$/m).filter((d) => d.trim());
    for (const res of resources) {
      if (res.kind === "Module") {
        const spec = (res.value.spec ?? {}) as { image?: string; replicas?: number | { min: number }; concurrency?: number };
        const m = this.modules.get(res.name);
        const replicas = typeof spec.replicas === "number" ? spec.replicas : (spec.replicas?.min ?? 1);
        if (!m) {
          if (!dryRun) {
            const seed: ModuleSeed = { name: res.name, image: spec.image ?? "", replicas, concurrency: spec.concurrency ?? 1, latencyMs: 600, failRate: 0, transientRate: 0.01 };
            this.modules.set(res.name, {
              seed,
              samples: [],
              busy: 0,
              rampAt: this.now + 500,
              status: { name: res.name, revision: 1, image: seed.image, runtime: "auto", driver: "docker", replicas: { desired: replicas, ready: 0 }, health: "down", conditions: [{ type: "Ready", status: "False", reason: "Starting" }], queue_depth: 0, dlq_depth: 0, in_flight: 0, live: zeroLive() },
            });
            this.refreshHealth(this.modules.get(res.name)!, true);
          }
          results.push({ kind: "Module", name: res.name, action: "created", revision: 1 });
        } else {
          const changed = (spec.image ?? "") !== m.status.image || replicas !== m.status.replicas.desired;
          if (changed && !dryRun) {
            m.status.image = spec.image ?? m.status.image;
            m.status.revision += 1;
            this.scale(res.name, replicas);
          }
          results.push({ kind: "Module", name: res.name, action: changed ? "updated" : "unchanged", revision: m.status.revision + (changed && dryRun ? 1 : 0) });
        }
      } else {
        const p = this.pipelines.get(res.name);
        const yaml = docs[res.doc]?.replace(/^\n+/, "") ?? text;
        const hashNew = `sha256:${hash(yaml)}`;
        if (!p) {
          if (!dryRun) {
            this.pipelines.set(res.name, { name: res.name, revisions: [this.makeRevision(1, yaml, res.value, "ui", "admin")], spawnEveryMs: 4000, nextSpawn: this.now + 2000, maxRuns: 5 });
            this.emit({ type: "pipeline.applied", data: { name: res.name, revision: 1 } });
          }
          results.push({ kind: "Pipeline", name: res.name, action: "created", revision: 1 });
        } else if (this.current(p).spec_hash === hashNew) {
          results.push({ kind: "Pipeline", name: res.name, action: "unchanged", revision: this.current(p).revision });
        } else {
          const revision = this.current(p).revision + 1;
          if (!dryRun) {
            p.revisions.push(this.makeRevision(revision, yaml, res.value, "ui", "admin"));
            this.emit({ type: "pipeline.applied", data: { name: res.name, revision } });
          }
          results.push({ kind: "Pipeline", name: res.name, action: "updated", revision });
        }
      }
    }
    const warnings = diagnostics.filter((d) => d.severity === "warning").map((d) => ({ severity: "warning" as const, source: "request", line: d.line, path: toPointer(d.path), message: d.message }));
    return { status: 200, body: { applied: !dryRun, results, ...(warnings.length ? { warnings } : {}) } };
  }
}

const FATAL = ["KeyError: 'frames'", "invalid input: text must be a string", "model returned an empty response", "unsupported projection EPSG:3006"];
const TRANSIENT = ["S3 503 SlowDown", "upstream timeout after 30s", "connection reset by peer", "rate limited, retry after 5s"];
const IDEAS = ["CREATE DATABASE за 60 секунд", "BEGIN TRANSACTION", "Индексы в PostgreSQL", "Что такое MVCC"];

/** `spec.stages[1].step` → `/spec/stages/1/step` (the gateway reports JSON pointers). */
export function toPointer(path: string): string {
  if (!path) return "";
  return `/${path.replace(/\[(\d+)\]/g, ".$1").split(".").filter(Boolean).join("/")}`;
}

export function zeroLive(): LiveModuleMetrics {
  return { rate: 0, p50_ms: 0, p95_ms: 0, in_flight: 0, queue_depth: 0, error_rate: 0 };
}

function sample<T>(r: () => number, xs: readonly T[]): T {
  return xs[Math.floor(r() * xs.length)]!;
}

function iso(t: number): string {
  return new Date(t).toISOString();
}

function round(n: number): number {
  return Math.round(n * 100) / 100;
}

export function hashNum(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
}

function hash(s: string): string {
  return hashNum(s).toString(16).padStart(8, "0") + hashNum(`${s}#`).toString(16).padStart(8, "0");
}

function uuid(r: () => number): string {
  let s = "";
  for (let i = 0; i < 32; i++) s += HEX.charAt(Math.floor(r() * 16));
  return `${s.slice(0, 8)}-${s.slice(8, 12)}-4${s.slice(13, 16)}-a${s.slice(17, 20)}-${s.slice(20)}`;
}

function runId(seq: number, r: () => number): string {
  const u = uuid(r);
  return `${seq.toString(16).padStart(4, "0")}${u.slice(4)}`;
}

function parseRange(r: string): number {
  const m = /^(\d+)(m|h|d)$/.exec(r);
  if (!m) return 3_600_000;
  const n = Number(m[1]);
  return n * (m[2] === "m" ? 60_000 : m[2] === "h" ? 3_600_000 : 86_400_000);
}
