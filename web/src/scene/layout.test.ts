import { describe, expect, it } from "vitest";

import { computeLayout, itemPosition, pointAt, STAGE_GAP, TRAVEL_MS } from "./layout";

const stage = (name: string, lanes = 1) => ({ name, lanes: Array.from({ length: lanes }, (_, j) => ({ stepId: `${name}-${j}`, module: `m-${name}-${j}` })) });

describe("computeLayout", () => {
  it("puts parallel lanes side by side at the same X", () => {
    const l = computeLayout({ stages: [stage("a"), stage("b", 3), stage("c")] as never });
    const b = l.stages[1]!;
    expect(new Set(b.lanes.map((x) => x.pos[0]))).toEqual(new Set([STAGE_GAP]));
    expect(b.lanes.map((x) => x.pos[2])).toEqual([-3.4, 0, 3.4]);
    expect(l.stages.map((s) => s.s)).toEqual([STAGE_GAP, 2 * STAGE_GAP, 3 * STAGE_GAP]);
  });

  it("snakes after PER_ROW stages and keeps the belt continuous", () => {
    const l = computeLayout({ stages: Array.from({ length: 20 }, (_, i) => stage(`s${i}`)) as never });
    expect(l.stages[7]!.row).toBe(0);
    expect(l.stages[8]!.row).toBe(1);
    expect(l.stages[8]!.center[0]).toBe(l.stages[7]!.center[0]);
    expect(l.stages[8]!.dir).toBe(-1);
    for (let i = 1; i < l.stages.length; i++) expect(l.stages[i]!.s).toBeGreaterThan(l.stages[i - 1]!.s);
    expect(pointAt(l, l.stages[12]!.s)).toEqual(l.stages[12]!.center);
  });

  it("interpolates items from the previous station onto their lane", () => {
    const l = computeLayout({ stages: [stage("a"), stage("b", 2)] as never });
    const it = { stage: 1, fromStage: 0, state: "moving", t0: 0 };
    const start = itemPosition(l, it, "b-1", 0, 1, 0);
    expect(start[0]).toBeCloseTo(l.stages[0]!.center[0]);
    const end = itemPosition(l, it, "b-1", 0, 1, TRAVEL_MS);
    expect(end[0]).toBeCloseTo(l.stages[1]!.lanes[1]!.pos[0]);
    expect(end[2]).toBeCloseTo(l.stages[1]!.lanes[1]!.pos[2]);
    const failed = itemPosition(l, { ...it, state: "failed", finishedAt: 0 }, "b-1", 0, 1, 5000);
    expect(failed[1]).toBeLessThan(0);
  });
});
