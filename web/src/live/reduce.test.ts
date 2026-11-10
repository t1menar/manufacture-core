import { describe, expect, it } from "vitest";

import type { StreamEvent } from "../api/types";
import { applyEvent, emptyLive, MAX_ITEMS, pruneItems, type LiveState } from "./reduce";

const step = (type: "step.dispatched" | "step.started" | "step.succeeded" | "step.failed", stage: number, extra: object = {}): StreamEvent => ({
  type,
  data: { run_id: "r1", pipeline: "p", stage: `s${stage}`, stage_index: stage, step_id: `s${stage}`, module: `m${stage}`, at: "", ...extra },
});

function run(events: StreamEvent[], from: LiveState = emptyLive(), now = 1000): LiveState {
  return events.reduce((s, e, i) => applyEvent(s, e, now + i), from);
}

describe("applyEvent", () => {
  it("moves an item along the belt from created to done", () => {
    let s = run([{ type: "run.created", id: "1", data: { run_id: "r1", pipeline: "p", revision: 1, status: "running", at: "" } }]);
    expect(s.items.r1).toMatchObject({ stage: -1, state: "moving" });
    s = run([step("step.dispatched", 0), step("step.started", 0)], s);
    expect(s.items.r1).toMatchObject({ stage: 0, fromStage: -1, state: "processing" });
    s = run([step("step.succeeded", 0), step("step.dispatched", 1)], s);
    expect(s.items.r1).toMatchObject({ stage: 1, fromStage: 0, state: "moving" });
    s = run([{ type: "run.finished", id: "9", data: { run_id: "r1", pipeline: "p", revision: 1, status: "succeeded", at: "" } }], s, 5000);
    expect(s.items.r1).toMatchObject({ state: "done", finishedAt: 5000 });
    expect(s.lastEventId).toBe("9");
    expect(pruneItems(s, 5000 + 10_000).items.r1).toBeUndefined();
  });

  it("splits parallel stages into parts and aggregates their state", () => {
    let s = run([
      { type: "step.started", data: { run_id: "r1", pipeline: "p", stage: "x", stage_index: 2, step_id: "a", module: "ma", at: "" } },
      { type: "step.started", data: { run_id: "r1", pipeline: "p", stage: "x", stage_index: 2, step_id: "b", module: "mb", at: "" } },
      { type: "step.succeeded", data: { run_id: "r1", pipeline: "p", stage: "x", stage_index: 2, step_id: "a", module: "ma", at: "" } },
    ]);
    expect(Object.keys(s.items.r1!.parts)).toEqual(["a", "b"]);
    expect(s.items.r1!.state).toBe("processing");
    s = run(
      [{ type: "step.failed", data: { run_id: "r1", pipeline: "p", stage: "x", stage_index: 2, step_id: "b", module: "mb", at: "", error: { class: "fatal", code: "boom", message: "bad" } } }],
      s,
      2000,
    );
    expect(s.items.r1!.state).toBe("failed");
    expect(s.lastErrorAt.mb).toBe(2000);
    expect(s.feed[0]).toMatchObject({ tone: "error", text: "mb · x: boom — bad" });
  });

  it("keeps transient failures processing (they are retried)", () => {
    const s = run([step("step.started", 0), step("step.failed", 0, { error: { class: "transient", code: "t", message: "" } })]);
    expect(s.items.r1!.state).toBe("processing");
  });

  it("collects metric ticks into a bounded history", () => {
    let s = emptyLive();
    for (let i = 0; i < 70; i++) {
      s = applyEvent(s, { type: "metrics.tick", data: { ts: i, modules: { m: { rate: i, p50_ms: 1, p95_ms: 2, in_flight: 0, queue_depth: 0, error_rate: 0 } } } }, i);
    }
    expect(s.rateHistory.m).toHaveLength(60);
    expect(s.metrics.m?.rate).toBe(69);
  });

  it("caps the number of items", () => {
    let s = emptyLive();
    for (let i = 0; i < MAX_ITEMS + 5; i++) {
      s = applyEvent(s, { type: "run.created", data: { run_id: `r${i}`, pipeline: "p", revision: 1, status: "running", at: "" } }, i);
    }
    expect(Object.keys(s.items)).toHaveLength(MAX_ITEMS);
    expect(s.items.r0).toBeUndefined();
  });
});
