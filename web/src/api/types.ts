/**
 * Gateway API types. REST shapes come from `api/openapi.yaml` (generated into schema.gen.ts by
 * `npm run gen:api`; CI fails when it is stale), so the panel cannot drift from the core contract.
 */
import type { components } from "./schema.gen";

type S = components["schemas"];

export type RunStatus = S["RunStatus"];
export type StepStatus = S["StepStatus"];
export type Health = S["Health"];
export type ErrorInfo = S["ErrorInfo"];
export type ErrorClass = ErrorInfo["class"];
export type StageView = S["StageView"];
export type StageKind = StageView["kind"];
export type LaneView = StageView["lanes"][number];
export type RunCounts = S["RunCounters"];
export type PipelineSummary = S["PipelineSummary"];
export type PipelineDetail = S["PipelineDetail"];
export type PipelineRevision = S["Revision"];
export type Condition = S["Condition"];
export type LiveModuleMetrics = S["LiveMetrics"];
export type ModuleStatus = S["ModuleStatus"];
export type ModuleDetail = S["ModuleDetail"];
export type Replica = S["Replica"];
export type ApplyResult = S["ApplyResult"];
export type ServerDiagnostic = S["Diagnostic"];
export type Run = S["Run"];
export type Trigger = Run["trigger"];
export type Attempt = S["Attempt"];
export type Step = S["Step"];
export type RunDetail = S["RunDetail"];
export type Snapshot = RunDetail["snapshots"][number];
export type Overview = S["Overview"];
export type SystemInfo = S["System"];
export type ModuleMetrics = S["ModuleMetrics"];
export type MetricSeriesName = keyof ModuleMetrics["series"];
export type MetricPoint = S["Series"][number];
export type Conveyor = S["Conveyor"];
export type ConveyorItem = Conveyor["items"][number];
export type AuditEntry = S["AuditEntry"];
export type RunEventData = S["RunEvent"];
export type StepEventData = S["StepEvent"];
export type MetricsTick = S["MetricsTick"];
export type StreamEventType = S["StreamEventType"];

export type List<T> = { items: T[]; next_cursor?: string };
export type RunList = List<Run>;
export type Presigned = { url: string; expires_at: string };
/** Run context as returned in RunDetail.context and snapshots (free-form JSON). */
export type RunContext = Record<string, unknown>;

export type StepEventType = Extract<StreamEventType, `step.${string}`>;

/** SSE frame: `event:` is the type, `data:` the payload (per `x-sse-payloads` in the OpenAPI). */
export type StreamEvent =
  | { type: "run.created" | "run.updated" | "run.finished"; id?: string; data: RunEventData }
  | { type: StepEventType; id?: string; data: StepEventData }
  | { type: "module.status"; id?: string; data: ModuleStatus }
  | { type: "module.deleted" | "pipeline.deleted"; id?: string; data: { name: string } }
  | { type: "pipeline.applied"; id?: string; data: { name: string; revision: number } }
  | { type: "metrics.tick"; id?: string; data: MetricsTick }
  | { type: "reset"; id?: string; data: Record<string, never> };

export const TERMINAL_RUN: readonly RunStatus[] = ["succeeded", "failed", "cancelled"];

/** Claim-check reference (schemas/envelope/v2/blob-ref.schema.json): the key is `$blob`. */
export type BlobRef = { $blob: string; size: number; sha256?: string; content_type?: string };

export function isBlobRef(v: unknown): v is BlobRef {
  return typeof v === "object" && v !== null && typeof (v as { $blob?: unknown }).$blob === "string";
}
