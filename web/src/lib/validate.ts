import Ajv2020, { type ErrorObject, type ValidateFunction } from "ajv/dist/2020.js";
import { isMap, isSeq, LineCounter, parseAllDocuments, type Document, type Node } from "yaml";

import commonSchema from "../../../schemas/v1/common.schema.json";
import moduleSchema from "../../../schemas/v1/module.schema.json";
import pipelineSchema from "../../../schemas/v1/pipeline.schema.json";
import type { PipelineSpec, StageSpec } from "./spec";

export type Severity = "error" | "warning";

export type Diagnostic = {
  severity: Severity;
  message: string;
  /** Path inside the resource, e.g. `spec.stages[1].routes[0].goto`. */
  path: string;
  /** 1-based line in the whole file. */
  line: number;
  from: number;
  to: number;
  doc: number;
};

export type ParsedResource = { doc: number; kind: string; name: string; value: Record<string, unknown> };

export type ValidationResult = { diagnostics: Diagnostic[]; resources: ParsedResource[] };

let validators: { Module: ValidateFunction; Pipeline: ValidateFunction } | null = null;

/** JSON Schemas come straight from `schemas/v1` of this repository (the contract owner), so they never drift. */
function getValidators() {
  if (!validators) {
    const ajv = new Ajv2020({ allErrors: true, strict: false });
    ajv.addSchema(commonSchema);
    validators = {
      Module: ajv.compile(moduleSchema),
      Pipeline: ajv.compile(pipelineSchema),
    };
  }
  return validators;
}

type PathSeg = string | number;

function pointerToSegs(ptr: string): PathSeg[] {
  if (!ptr) return [];
  return ptr
    .slice(1)
    .split("/")
    .map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"))
    .map((s) => (/^\d+$/.test(s) ? Number(s) : s));
}

export function formatPath(segs: PathSeg[]): string {
  let out = "";
  for (const s of segs) out += typeof s === "number" ? `[${s}]` : out ? `.${s}` : s;
  return out;
}

/** Finds the deepest YAML node that exists along `segs` and returns its source range. */
function rangeOf(doc: Document, segs: PathSeg[], key = false): [number, number] {
  let node: unknown = doc.contents;
  let range: [number, number] = (doc.contents)?.range?.slice(0, 2) as [number, number] | undefined ?? [0, 0];
  for (const s of segs) {
    if (isMap(node)) {
      const pair = node.items.find((p) => {
        const k = p.key as { value?: unknown } | null;
        return k?.value === s;
      });
      if (!pair) break;
      const target = (key ? pair.key : (pair.value ?? pair.key)) as Node | null;
      if (target?.range) range = [target.range[0], target.range[1]];
      node = pair.value;
    } else if (isSeq(node) && typeof s === "number") {
      const item = node.items[s] as Node | undefined;
      if (!item) break;
      if (item.range) range = [item.range[0], item.range[1]];
      node = item;
    } else break;
  }
  return range;
}

function ajvMessage(e: ErrorObject): { segs: PathSeg[]; message: string; key: boolean } {
  const segs = pointerToSegs(e.instancePath);
  const p = e.params as Record<string, unknown>;
  switch (e.keyword) {
    case "additionalProperties":
      return { segs: [...segs, String(p.additionalProperty)], message: `unknown field "${String(p.additionalProperty)}"`, key: true };
    case "required":
      return { segs, message: `missing required field "${String(p.missingProperty)}"`, key: false };
    case "enum":
      return {
        segs,
        message: `must be one of ${(p.allowedValues as unknown[]).map((v) => JSON.stringify(v)).join(", ")}`,
        key: false,
      };
    case "const":
      return { segs, message: `must be ${JSON.stringify(p.allowedValue)}`, key: false };
    default:
      return { segs, message: e.message ?? e.keyword, key: false };
  }
}

/** Semantic rules mirrored from `internal/spec/rules_pipeline.go` (subset; the server stays authoritative). */
export function pipelineRules(spec: PipelineSpec): { segs: PathSeg[]; message: string }[] {
  const out: { segs: PathSeg[]; message: string }[] = [];
  const stages: StageSpec[] = Array.isArray(spec.stages) ? spec.stages : [];
  const stageAt = new Map<string, number>();
  const stepAt = new Map<string, string>();
  stages.forEach((st, i) => {
    const base: PathSeg[] = ["spec", "stages", i];
    const prev = stageAt.get(st.name);
    if (prev !== undefined) out.push({ segs: [...base, "name"], message: `stage "${st.name}" is already defined at spec.stages[${prev}]` });
    else stageAt.set(st.name, i);
    const hasStep = st.step !== undefined;
    const hasPar = st.parallel !== undefined;
    if (hasStep === hasPar) out.push({ segs: base, message: "a stage needs exactly one of step or parallel" });
    if (st.foreach !== undefined && hasPar) out.push({ segs: [...base, "foreach"], message: "foreach runs one step per item: use step instead of parallel" });
    if (st.max_parallel !== undefined && st.foreach === undefined) out.push({ segs: [...base, "max_parallel"], message: "max_parallel applies only to foreach stages" });
    if (st.join !== undefined && !hasPar && st.foreach === undefined) out.push({ segs: [...base, "join"], message: "join applies only to parallel and foreach stages" });
    const claim = (id: string, segs: PathSeg[]) => {
      const where = formatPath(segs);
      const used = stepAt.get(id);
      if (used !== undefined) out.push({ segs, message: `step id "${id}" is already used at ${used}` });
      else stepAt.set(id, where);
    };
    if (st.step && !hasPar) claim(st.step.id ?? st.name, [...base, "step"]);
    st.parallel?.forEach((s, j) => claim(s.id ?? s.module, [...base, "parallel", j]));
  });
  stages.forEach((st, i) => {
    st.routes?.forEach((r, j) => {
      const segs: PathSeg[] = ["spec", "stages", i, "routes", j];
      const target = stageAt.get(r.goto);
      if (target === undefined) out.push({ segs: [...segs, "goto"], message: `stage "${r.goto}" is not defined` });
      else if (target <= i && r.max_visits === undefined) out.push({ segs, message: "this route forms a loop; set max_visits" });
    });
  });
  return out;
}

/**
 * Validates a (multi-document) YAML spec in the browser: YAML syntax, JSON Schema from `schemas/v1`,
 * and the cross-field rules. `knownModules` turns references to unknown modules into warnings.
 */
export function validateSpecText(text: string, knownModules: Iterable<string> = []): ValidationResult {
  const lc = new LineCounter();
  const docs = parseAllDocuments(text, { lineCounter: lc, prettyErrors: false });
  const diagnostics: Diagnostic[] = [];
  const resources: ParsedResource[] = [];
  const v = getValidators();
  const add = (doc: number, severity: Severity, message: string, path: string, from: number, to: number) => {
    diagnostics.push({ severity, message, path, line: lc.linePos(from).line, from, to: Math.max(to, from + 1), doc });
  };
  const list = Array.isArray(docs) ? docs : [docs];
  const declared = new Set<string>(knownModules);
  const pipelines: { doc: number; d: Document; spec: PipelineSpec }[] = [];

  list.forEach((d, i) => {
    for (const e of d.errors) add(i, "error", e.message.split("\n")[0] ?? e.message, "", e.pos[0], e.pos[1]);
    if (d.errors.length) return;
    const value: unknown = d.toJS();
    if (value === null || value === undefined) return;
    if (typeof value !== "object" || Array.isArray(value)) {
      const [f, t] = rangeOf(d, []);
      add(i, "error", "a resource must be a mapping", "", f, t);
      return;
    }
    const obj = value as Record<string, unknown>;
    const kind = obj.kind;
    if (kind !== "Module" && kind !== "Pipeline") {
      const [f, t] = rangeOf(d, ["kind"]);
      add(i, "error", 'kind must be "Module" or "Pipeline"', "kind", f, t);
      return;
    }
    const validate = v[kind];
    if (!validate(obj)) {
      for (const e of validate.errors ?? []) {
        if (e.keyword === "if" || e.keyword === "oneOf" || e.keyword === "anyOf") continue;
        const m = ajvMessage(e);
        const [f, t] = rangeOf(d, m.segs, m.key);
        add(i, "error", m.message, formatPath(m.segs), f, t);
      }
    }
    const meta = obj.metadata as { name?: unknown } | undefined;
    const name = typeof meta?.name === "string" ? meta.name : "";
    resources.push({ doc: i, kind, name, value: obj });
    if (kind === "Module" && name) declared.add(name);
    if (kind === "Pipeline" && obj.spec && typeof obj.spec === "object") pipelines.push({ doc: i, d, spec: obj.spec as PipelineSpec });
  });

  for (const p of pipelines) {
    for (const r of pipelineRules(p.spec)) {
      const [f, t] = rangeOf(p.d, r.segs);
      add(p.doc, "error", r.message, formatPath(r.segs), f, t);
    }
    if (declared.size === 0) continue;
    (Array.isArray(p.spec.stages) ? p.spec.stages : []).forEach((st, i) => {
      const check = (mod: unknown, segs: PathSeg[]) => {
        if (typeof mod === "string" && !declared.has(mod)) {
          const [f, t] = rangeOf(p.d, segs);
          add(p.doc, "warning", `module "${mod}" is not applied yet`, formatPath(segs), f, t);
        }
      };
      check(st.step?.module, ["spec", "stages", i, "step", "module"]);
      st.parallel?.forEach((s, j) => check(s.module, ["spec", "stages", i, "parallel", j, "module"]));
    });
  }
  diagnostics.sort((a, b) => a.from - b.from);
  return { diagnostics, resources };
}
