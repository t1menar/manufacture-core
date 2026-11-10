# Changelog

## Contract

### 0.2.1 (2026-11-10)
- Gateway API 0.3.0, aligned with what the admin panel consumes: `/api/v1/system` (was `/info`),
  `ModuleStatus` (conditions, queue/DLQ depth, SDK and contract versions), `StageView` in pipelines,
  `RunDetail = {run, steps, context, snapshots}`, `POST /runs/{id}/retry`, `GET /runs/{id}/snapshots/{after_stage}`,
  `GET /modules/{name}/metrics` (replaces `/metrics/query`), `POST /modules/{name}/scale`, 422 as problem+json with `errors[]`.
- SSE: granular event types (`run.created|updated|finished`, `step.dispatched|started|progress|succeeded|failed|skipped`,
  `module.status|deleted`, `pipeline.applied|deleted`, `metrics.tick` without id, `reset`); `data` is the payload itself.
- Envelope v2 unchanged.

### 0.2.0 (2026-11-10)
- Envelope v2 (`schemas/envelope/v2`): step, source and module messages, attempt id instead of fence, blob references (`$blob`).
- Gateway OpenAPI (`api/openapi.yaml`) including the SSE stream format.
- Module protocol description (`docs/messaging.md`).

### 0.1.0 (2026-11-10)
- Resources `Module` and `Pipeline` (`schemas/v1`).
