# Changelog

## Contract

### 0.3.0 (2026-11-10)
Breaking (allowed before 1.0); implements the module team requirements R1–R10 (`docs/adr/0013`, `0014`).
- Envelope: new `step.waiting` (module -> core; `reason`, `seq`, `expected_by`, `usage`), `run.started` and
  `run.finished` (core -> `mf.notify.<module>`), `run.rejected` (core -> source replicas).
- `step.requested`: `step_key` = uuid5(STEP_NS, "<run_id>:<stage>:<step_id>:<item_index|->:<visit>"), `visit`,
  `timeout_seconds` (counted from the first heartbeat), `deadline` now an optional ceiling, `budget`, `priority`, `labels`.
- `usage {cost_usd, tokens}` (cumulative per attempt) on heartbeat, waiting, succeeded, failed; `step.failed`
  `recommendation`, `details`; `step.cancel` reasons `budget`, `operator`; `run.requested` `priority`, `labels`;
  `module.online` `min_budget_usd`; reserved codes `module_shutdown`, `budget`, `invalid_blob_ref`, `data_too_large`.
- blob-ref: `sha256` required, `keep`, `name`; every `$blob` object in a payload is validated.
- Test vectors: `testdata/envelope/step_key.json`, `testdata/envelope/message_ids.json`.
- Pipeline: `priority`, `budget {run_usd, warn_usd, day_usd}`, `concurrency.min_interval` (gap between run starts) and
  `count_waiting`, `context.max_bytes`; step `on_timeout` (retry|fail|attention), `on_cancel`, `wait.timeout`,
  `min_budget_usd`. Module: `subscribe` (`run.started`, `run.finished`, optionally per pipeline), `min_budget_usd`.
  CEL: `visit`, `steps.<id>.visits[n]`, `.last`, `.visit`.
- Gateway API 0.4.0: `POST /attempts/{id}/confirm` (module token), operator actions with `comment`
  (`steps/{step_key}/retry|skip|cancel`, `PATCH runs/{id}/budget|priority`), `/dead-letters`, `/usage`, statuses
  `waiting` / `waiting_human`, snapshots by visit. Additive for API 0.3 clients: `Run.attention` stays a string
  (plus `attention_info {code, message}`), `Step.visits` stays (= `visit`); SSE `step.waiting` and
  `run.budget_warning` are planned for API 0.5.
- `docs/messaging.md` rewritten for 0.3.0, including the SDK Q&A; `docs/patterns/review-gate.md`.

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
