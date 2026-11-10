import { describe, expect, it } from "vitest";

import { buildRequest, setTransport } from "../api/http";
import { api } from "../api/client";
import { setToken } from "../auth/token";
import type { StreamEvent } from "../api/types";
import { createMockTransport } from "./server";
import { Simulator } from "./simulator";

describe("Simulator", () => {
  it("runs hello-world to `hello world!`", () => {
    const sim = new Simulator({ seed: 1, speed: 4, now: 0 });
    const events: StreamEvent[] = [];
    sim.subscribe((e) => events.push(e.ev));
    sim.advance(30_000);
    const done = sim.listRuns({ pipeline: "hello-world", status: "succeeded" }).items;
    expect(done.length).toBeGreaterThan(3);
    const detail = sim.runDetail(done[0]!.id)!;
    expect(detail.context.data.text).toBe("hello world!");
    expect(detail.snapshots.map((s) => s.after_stage)).toEqual([0, 1, 2]);
    expect(events.some((e) => e.type === "metrics.tick")).toBe(true);
    // ids are monotonic and replayable
    const last = Number(sim.lastEventId);
    expect(sim.since(String(last - 3))?.map((e) => Number(e.id))).toEqual([last - 2, last - 1, last]);
  });

  it("fans out parallel and foreach stages", () => {
    const sim = new Simulator({ seed: 3, speed: 4, now: 0 });
    sim.advance(60_000);
    const geo = sim.listRuns({ pipeline: "geo-analysis", status: "succeeded" }).items[0]!;
    const steps = sim.runDetail(geo.id)!.steps;
    expect(steps.filter((s) => s.stage === "analyze-tiles").length).toBeGreaterThanOrEqual(3);
    expect(new Set(steps.filter((s) => s.stage === "layers").map((s) => s.step_id))).toEqual(new Set(["geo-ndvi", "geo-roads", "buildings-v2"]));
  });
});

describe("mock transport", () => {
  it("requires a token and serves the typed API", async () => {
    const sim = new Simulator({ seed: 2, now: 0, docking: false });
    setTransport(createMockTransport(sim));
    setToken(null);
    await expect(api.overview()).rejects.toMatchObject({ status: 401 });
    setToken("t");
    const o = await api.overview();
    expect(o.pipelines.map((p) => p.name)).toContain("hello-world");
    const bad = await api.apply("apiVersion: manufacture.dev/v1\nkind: Pipeline\nmetadata: {name: x}\nspec: {stages: []}\n").catch((e: unknown) => e);
    expect(bad).toMatchObject({ status: 422 });
    const res = await api.apply("apiVersion: manufacture.dev/v1\nkind: Pipeline\nmetadata: {name: x}\nspec:\n  stages:\n    - {name: a, step: {module: hello-world}}\n");
    expect(res.results).toEqual([{ kind: "Pipeline", name: "x", action: "created", revision: 1 }]);
    const res2 = await api.apply("apiVersion: manufacture.dev/v1\nkind: Pipeline\nmetadata: {name: x}\nspec:\n  stages:\n    - {name: a, step: {module: hello-world}}\n");
    expect(res2.results[0]?.action).toBe("unchanged");
    const scaled = await api.scaleModule("hello-world", 3);
    expect(scaled.replicas.desired).toBe(3);
    const stream = await createMockTransport(sim)(buildRequest("/stream"));
    expect(stream.headers.get("content-type")).toBe("text/event-stream");
    await stream.body?.cancel();
  });
});
