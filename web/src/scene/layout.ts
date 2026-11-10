import type { ConveyorScene } from "./model";

export type Vec3 = [number, number, number];

export type LaneLayout = { stepId: string; module: string; pos: Vec3 };
export type StageLayout = { index: number; name: string; center: Vec3; lanes: LaneLayout[]; row: number; dir: 1 | -1; s: number };
export type Layout = {
  stages: StageLayout[];
  /** Belt polyline: entry → stage centers (with U-turns between rows) → exit. */
  path: Vec3[];
  /** Cumulative arc length at each path vertex. */
  cum: number[];
  length: number;
  entry: { pos: Vec3; s: number };
  exit: { pos: Vec3; s: number };
  /** Where rejected items fall. */
  reject: Vec3;
  bounds: { min: Vec3; max: Vec3 };
  laneGap: number;
  rowGap: number;
};

export const STAGE_GAP = 6.5;
export const LANE_GAP = 3.4;
export const PER_ROW = 8;

/**
 * Places stages along the belt: X steps per stage, parallel lanes spread on Z, and a snake
 * (boustrophedon) with U-turns after every PER_ROW stages so any number of stages fits the view.
 */
export function computeLayout(scene: Pick<ConveyorScene, "stages">, perRow = PER_ROW): Layout {
  const n = scene.stages.length;
  const maxLanes = Math.max(1, ...scene.stages.map((s) => s.lanes.length));
  const rowGap = Math.max(10, maxLanes * LANE_GAP + 6);
  const stages: StageLayout[] = scene.stages.map((st, i) => {
    const row = Math.floor(i / perRow);
    const col = i % perRow;
    const dir: 1 | -1 = row % 2 === 0 ? 1 : -1;
    const x = (dir === 1 ? col : perRow - 1 - col) * STAGE_GAP;
    const z = row * rowGap;
    const k = st.lanes.length;
    const lanes = st.lanes.map((l, j) => ({ stepId: l.stepId, module: l.module, pos: [x, 0, z + (j - (k - 1) / 2) * LANE_GAP] as Vec3 }));
    return { index: i, name: st.name, center: [x, 0, z], lanes, row, dir, s: 0 };
  });

  const path: Vec3[] = [];
  const first = stages[0];
  const entryPos: Vec3 = first ? [first.center[0] - STAGE_GAP, 0, first.center[2]] : [-STAGE_GAP, 0, 0];
  path.push(entryPos);
  const stageVertex: number[] = [];
  stages.forEach((st, i) => {
    const prev = stages[i - 1];
    if (prev && prev.row !== st.row) {
      const turnX = prev.center[0] + prev.dir * (STAGE_GAP * 0.75);
      path.push([turnX, 0, prev.center[2]], [turnX, 0, st.center[2]]);
    }
    stageVertex.push(path.length);
    path.push(st.center);
  });
  const last = stages[n - 1];
  const exitPos: Vec3 = last ? [last.center[0] + last.dir * STAGE_GAP, 0, last.center[2]] : [STAGE_GAP, 0, 0];
  path.push(exitPos);

  const cum = [0];
  for (let i = 1; i < path.length; i++) cum.push(cum[i - 1]! + dist(path[i - 1]!, path[i]!));
  stages.forEach((st, i) => (st.s = cum[stageVertex[i]!]!));

  const xs = path.map((p) => p[0]);
  const zs = [...path.map((p) => p[2]), ...stages.flatMap((s) => s.lanes.map((l) => l.pos[2]))];
  const min: Vec3 = [Math.min(...xs) - 2, -1, Math.min(...zs) - LANE_GAP];
  const max: Vec3 = [Math.max(...xs) + 2, 4, Math.max(...zs) + LANE_GAP];
  const reject: Vec3 = [(min[0] + max[0]) / 2, -0.6, min[2] - 3];
  return {
    stages,
    path,
    cum,
    length: cum[cum.length - 1]!,
    entry: { pos: entryPos, s: 0 },
    exit: { pos: exitPos, s: cum[cum.length - 1]! },
    reject,
    bounds: { min, max },
    laneGap: LANE_GAP,
    rowGap,
  };
}

function dist(a: Vec3, b: Vec3): number {
  return Math.hypot(b[0] - a[0], b[1] - a[1], b[2] - a[2]);
}

/** Point at arc length `s` along the belt (clamped), written into `out`. */
export function pointAt(l: Layout, s: number, out: Vec3 = [0, 0, 0]): Vec3 {
  const t = Math.max(0, Math.min(l.length, s));
  let i = 1;
  while (i < l.cum.length - 1 && l.cum[i]! < t) i++;
  const a = l.path[i - 1]!;
  const b = l.path[i]!;
  const seg = l.cum[i]! - l.cum[i - 1]!;
  const k = seg > 0 ? (t - l.cum[i - 1]!) / seg : 0;
  out[0] = a[0] + (b[0] - a[0]) * k;
  out[1] = a[1] + (b[1] - a[1]) * k;
  out[2] = a[2] + (b[2] - a[2]) * k;
  return out;
}

/** Unit direction of the belt at arc length `s`. */
export function directionAt(l: Layout, s: number): Vec3 {
  const a = pointAt(l, s - 0.01);
  const b = pointAt(l, s + 0.01);
  const len = Math.hypot(b[0] - a[0], b[2] - a[2]) || 1;
  return [(b[0] - a[0]) / len, 0, (b[2] - a[2]) / len];
}

export const smooth = (k: number): number => {
  const t = Math.max(0, Math.min(1, k));
  return t * t * (3 - 2 * t);
};

/** Seconds an item needs to travel between two stations. */
export const TRAVEL_MS = 900;

/**
 * Position of one item part at time `now`. Pure, so it is unit-tested and shared by the 3D and 2D views.
 * Travel: along the belt from the previous station, sliding onto its lane near the end.
 * Processing: inside the module block. Done (finished): rolls off past the exit. Failed: drops to the reject bin.
 */
export function itemPosition(
  l: Layout,
  it: { stage: number; fromStage: number; state: string; t0: number; finishedAt?: number },
  lane: string | undefined,
  partIndex: number,
  partCount: number,
  now: number,
  out: Vec3 = [0, 0, 0],
): Vec3 {
  const sOf = (i: number) => (i < 0 ? l.entry.s : (l.stages[i]?.s ?? l.exit.s));
  const st = l.stages[it.stage];
  const lanePos = st?.lanes.find((x) => x.stepId === lane)?.pos ?? st?.center;
  const k = smooth((now - it.t0) / TRAVEL_MS);
  const fan = partCount > 1 && st?.lanes.length === 1 ? (partIndex - (partCount - 1) / 2) * 0.55 : 0;

  if (it.finishedAt !== undefined && it.state === "done") {
    const kk = smooth((now - it.finishedAt) / 1800);
    pointAt(l, sOf(it.stage) + (l.exit.s + 2 - sOf(it.stage)) * kk, out);
    out[1] = 0.55;
    return out;
  }
  if (it.state === "failed" && it.finishedAt !== undefined) {
    const base = lanePos ?? l.entry.pos;
    const kk = smooth((now - it.finishedAt) / 1400);
    out[0] = base[0];
    out[1] = 0.55 + 0.8 * Math.sin(kk * Math.PI) - 1.2 * kk;
    out[2] = base[2] + (-2.6 + fan) * kk;
    return out;
  }
  if (it.stage < 0 || !st || !lanePos) {
    pointAt(l, l.entry.s - 1.2 - partIndex * 0.2, out);
    out[1] = 0.55;
    return out;
  }
  pointAt(l, sOf(it.fromStage) + (sOf(it.stage) - sOf(it.fromStage)) * k, out);
  const lk = smooth((k - 0.55) / 0.45);
  out[2] += (lanePos[2] - st.center[2] + fan) * lk;
  out[1] = 0.55 + (it.state === "attention" ? 1.9 * lk : 0);
  return out;
}
