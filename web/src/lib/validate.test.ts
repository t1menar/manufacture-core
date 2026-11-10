import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { compileStages, type PipelineSpec } from "./spec";
import { validateSpecText } from "./validate";

const TESTDATA = join(import.meta.dirname, "../../../testdata");

function files(dir: string): string[] {
  return readdirSync(join(TESTDATA, dir)).map((f) => join(TESTDATA, dir, f));
}

describe("validateSpecText against the core testdata", () => {
  it.each(files("valid"))("accepts %s", (file) => {
    const { diagnostics, resources } = validateSpecText(readFileSync(file, "utf8"));
    expect(diagnostics.filter((d) => d.severity === "error")).toEqual([]);
    expect(resources.length).toBeGreaterThan(0);
  });

  it.each(files("invalid/schema"))("rejects %s", (file) => {
    const { diagnostics } = validateSpecText(readFileSync(file, "utf8"));
    expect(diagnostics.some((d) => d.severity === "error")).toBe(true);
  });

  it.each(["ids.yaml", "stage-shape.yaml", "routes.yaml"])("reports every `# want:` path of rules/%s", (name) => {
    const text = readFileSync(join(TESTDATA, "invalid/rules", name), "utf8");
    const want = [...text.matchAll(/^# want: (\S+) \| (.+)$/gm)].map((m) => ({ path: m[1], msg: m[2] }));
    const { diagnostics } = validateSpecText(text);
    for (const w of want) {
      expect(diagnostics.some((d) => d.path === w.path && d.message.includes(w.msg!.slice(0, 20))), `${w.path}: ${w.msg}`).toBe(true);
    }
  });
});

describe("validateSpecText", () => {
  it("maps schema errors to the offending line", () => {
    const text = [
      "apiVersion: manufacture.dev/v1",
      "kind: Pipeline",
      "metadata: {name: p}",
      "spec:",
      "  stages:",
      "    - name: a",
      "      step: {module: m, timeout: 5 minutes}",
    ].join("\n");
    const { diagnostics } = validateSpecText(text, ["m"]);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0]).toMatchObject({ line: 7, path: "spec.stages[0].step.timeout" });
  });

  it("reports YAML syntax errors and unknown modules as warnings", () => {
    expect(validateSpecText("kind: [oops").diagnostics[0]?.severity).toBe("error");
    const text = "apiVersion: manufacture.dev/v1\nkind: Pipeline\nmetadata: {name: p}\nspec:\n  stages:\n    - {name: a, step: {module: ghost}}\n";
    const d = validateSpecText(text, ["real"]).diagnostics;
    expect(d).toEqual([expect.objectContaining({ severity: "warning", message: 'module "ghost" is not applied yet' })]);
  });
});

describe("compileStages", () => {
  it("defaults step ids like the core and keeps routes", () => {
    const spec: PipelineSpec = {
      stages: [
        { name: "a", step: { module: "m1" } },
        { name: "b", parallel: [{ module: "m2" }, { id: "x", module: "m3" }] },
        { name: "c", foreach: "data.items", step: { module: "m4" }, routes: [{ when: "true", goto: "a", max_visits: 2 }] },
      ],
    };
    expect(compileStages(spec)).toEqual([
      { name: "a", index: 0, kind: "single", lanes: [{ step_id: "a", module: "m1" }] },
      { name: "b", index: 1, kind: "parallel", lanes: [{ step_id: "m2", module: "m2" }, { step_id: "x", module: "m3" }] },
      { name: "c", index: 2, kind: "foreach", lanes: [{ step_id: "c", module: "m4" }], routes: [{ goto: "a", max_visits: 2 }] },
    ]);
  });
});
