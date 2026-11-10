-- Engine state (design-amendments §12): runs, visits of steps, attempts, admission,
-- usage, snapshots, blobs, module replicas and tokens, dead letters, events, outbox, inbox.

-- +goose Up
CREATE TABLE runs (
    id               uuid PRIMARY KEY,
    pipeline         text        NOT NULL,
    revision         integer     NOT NULL,
    status           text        NOT NULL CHECK (status IN ('queued', 'running', 'waiting_human', 'succeeded', 'failed', 'needs_attention', 'cancelled')),
    priority         smallint    NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100 AND 100),
    labels           jsonb       NOT NULL DEFAULT '{}',
    dedup_key        text,
    trigger          jsonb       NOT NULL,
    data             jsonb       NOT NULL DEFAULT '{}',
    initial_data     jsonb       NOT NULL DEFAULT '{}',
    stage_index      integer     NOT NULL DEFAULT 0,
    budget           jsonb,                                 -- {run_usd, warn_usd}: revision snapshot, PATCH
    spent_usd        numeric(14, 6) NOT NULL DEFAULT 0,
    budget_warned_at timestamptz,
    attention        jsonb,                                 -- {code, message}
    error            jsonb,                                 -- {class, code, message} of a failed run
    queue_reason     text,
    finished_count   integer     NOT NULL DEFAULT 0,        -- run.finished notifications sent
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT runs_dedup_key UNIQUE (pipeline, dedup_key)
);
CREATE INDEX runs_created_idx ON runs (created_at DESC, id DESC);
CREATE INDEX runs_pipeline_status_idx ON runs (pipeline, status);

CREATE TABLE run_queue (
    run_id      uuid PRIMARY KEY REFERENCES runs (id) ON DELETE CASCADE,
    pipeline    text        NOT NULL,
    priority    smallint    NOT NULL DEFAULT 0,
    enqueued_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX run_queue_order_idx ON run_queue (pipeline, priority DESC, enqueued_at);

CREATE TABLE pipeline_admission (
    pipeline        text PRIMARY KEY,
    last_started_at timestamptz
);

-- One row per visit of a step (a stage visited again after a route back gets new rows).
CREATE TABLE steps (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id             uuid        NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    stage              text        NOT NULL,
    stage_index        integer     NOT NULL,
    step_id            text        NOT NULL,
    item_index         integer,
    visit              integer     NOT NULL CHECK (visit > 0),
    step_key           uuid        NOT NULL UNIQUE,
    module             text        NOT NULL,
    status             text        NOT NULL CHECK (status IN ('pending', 'dispatched', 'running', 'waiting', 'succeeded', 'failed', 'skipped', 'cancelled')),
    current_attempt_id uuid,
    output             jsonb,
    error              jsonb,
    waiting            jsonb,                               -- {reason, expected_by, message, since}
    wait_total         interval    NOT NULL DEFAULT '0',
    wait_deadline      timestamptz,
    started_at         timestamptz,
    finished_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX steps_visit_idx ON steps (run_id, step_id, coalesce(item_index, -1), visit);
CREATE INDEX steps_wait_deadline_idx ON steps (wait_deadline) WHERE status = 'waiting';

CREATE TABLE step_attempts (
    id              uuid PRIMARY KEY,
    step_row        bigint      NOT NULL REFERENCES steps (id) ON DELETE CASCADE,
    run_id          uuid        NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    step_key        uuid        NOT NULL,
    module          text        NOT NULL,
    number          integer     NOT NULL CHECK (number > 0),
    status          text        NOT NULL CHECK (status IN ('scheduled', 'dispatched', 'running', 'waiting', 'succeeded', 'failed', 'timed_out', 'lost', 'cancelled')),
    request         jsonb       NOT NULL,                  -- the immutable step.requested payload
    timeout_seconds integer     NOT NULL,
    lease_seconds   integer     NOT NULL,
    not_before      timestamptz NOT NULL DEFAULT now(),
    dispatched_at   timestamptz,
    started_at      timestamptz,
    deadline        timestamptz,
    not_after       timestamptz,
    lease_until     timestamptz,
    waiting_since   timestamptz,
    expected_by     timestamptz,
    waiting_seq     integer     NOT NULL DEFAULT 0,
    confirmed_at    timestamptz,
    confirmed_until timestamptz,
    confirm_count   integer     NOT NULL DEFAULT 0,
    progress        double precision,
    replica_id      text,
    error           jsonb,
    finished_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (step_row, number)
);
CREATE INDEX step_attempts_lease_idx ON step_attempts (lease_until) WHERE status = 'running';
CREATE INDEX step_attempts_not_before_idx ON step_attempts (not_before) WHERE status = 'scheduled';
CREATE INDEX step_attempts_dispatched_idx ON step_attempts (dispatched_at) WHERE status = 'dispatched';
CREATE INDEX step_attempts_confirmed_idx ON step_attempts (confirmed_until)
    WHERE confirmed_at IS NOT NULL AND status IN ('running', 'waiting');

-- Cumulative usage per attempt (the core keeps the component-wise maximum of the reports).
CREATE TABLE usage (
    attempt_id uuid PRIMARY KEY REFERENCES step_attempts (id) ON DELETE CASCADE,
    run_id     uuid        NOT NULL,
    pipeline   text        NOT NULL,
    stage      text        NOT NULL,
    step_id    text        NOT NULL,
    module     text        NOT NULL,
    cost_usd   numeric(14, 6) NOT NULL DEFAULT 0,
    tokens     jsonb       NOT NULL DEFAULT '{}',
    day        date        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX usage_run_idx ON usage (run_id);
CREATE INDEX usage_pipeline_day_idx ON usage (pipeline, day);

CREATE TABLE context_snapshots (
    run_id      uuid        NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    after_stage integer     NOT NULL,
    visit       integer     NOT NULL,
    sha256      text        NOT NULL,
    size        integer     NOT NULL,
    body        jsonb,
    blob_ref    jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, after_stage, visit)
);

CREATE TABLE blobs (
    uri          text PRIMARY KEY,
    run_id       uuid        NOT NULL,
    attempt_id   uuid,
    sha256       char(64)    NOT NULL,
    size         bigint      NOT NULL,
    content_type text,
    name         text,
    keep         boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);
CREATE INDEX blobs_run_idx ON blobs (run_id);

-- Live replicas reported by module.online / module.heartbeat.
CREATE TABLE module_replicas (
    module            text        NOT NULL,
    replica_id        text        NOT NULL,
    status            text        NOT NULL CHECK (status IN ('up', 'unhealthy', 'offline')),
    reason            text,
    contract_version  text,
    sdk               jsonb,
    version           text,
    concurrency       integer,
    kinds             jsonb,
    min_budget_usd    numeric(14, 6),
    in_flight         integer     NOT NULL DEFAULT 0,
    online_at         timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (module, replica_id)
);

CREATE TABLE module_tokens (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    module       text        NOT NULL,
    token_hash   bytea       NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    revoked_at   timestamptz,
    last_used_at timestamptz
);
CREATE INDEX module_tokens_module_idx ON module_tokens (module);

CREATE TABLE dead_letters (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id     uuid,
    type           text,
    exchange       text        NOT NULL,
    routing_key    text        NOT NULL,
    source_queue   text        NOT NULL,
    source_module  text,
    reason         text        NOT NULL,
    death_count    integer     NOT NULL DEFAULT 1,
    headers        jsonb       NOT NULL DEFAULT '{}',
    body           bytea       NOT NULL,
    first_death_at timestamptz,
    received_at    timestamptz NOT NULL DEFAULT now(),
    status         text        NOT NULL DEFAULT 'parked' CHECK (status IN ('parked', 'redelivered', 'discarded')),
    acted_at       timestamptz,
    acted_by       text,
    comment        text
);
CREATE INDEX dead_letters_status_idx ON dead_letters (status, received_at DESC);

-- Timeline of runs and source of the SSE stream (retention by the janitor).
CREATE TABLE events (
    id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at      timestamptz NOT NULL DEFAULT now(),
    type    text        NOT NULL,
    run_id  uuid,
    data    jsonb       NOT NULL
);
CREATE INDEX events_run_idx ON events (run_id, id) WHERE run_id IS NOT NULL;
CREATE INDEX events_at_idx ON events (at);

CREATE TABLE outbox (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id       uuid        NOT NULL,
    type             text        NOT NULL,
    exchange         text        NOT NULL,
    routing_key      text        NOT NULL,
    priority         smallint    NOT NULL DEFAULT 0,
    body             jsonb       NOT NULL,
    status           text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'dead')),
    publish_attempts integer     NOT NULL DEFAULT 0,
    last_error       text,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    locked_by        text,
    locked_until     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz
);
-- A notification goes to every subscriber with the same message_id, so the key includes the route.
CREATE UNIQUE INDEX outbox_message_route_idx ON outbox (message_id, exchange, routing_key);
CREATE INDEX outbox_pending_idx ON outbox (next_attempt_at, id) WHERE status = 'pending';

CREATE TABLE inbox (
    consumer    text        NOT NULL,
    message_id  uuid        NOT NULL,
    type        text        NOT NULL,
    outcome     text        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);
CREATE INDEX inbox_received_idx ON inbox (received_at);

ALTER TABLE audit ADD COLUMN comment text;

-- +goose Down
ALTER TABLE audit DROP COLUMN comment;
DROP TABLE inbox;
DROP TABLE outbox;
DROP TABLE events;
DROP TABLE dead_letters;
DROP TABLE module_tokens;
DROP TABLE module_replicas;
DROP TABLE blobs;
DROP TABLE context_snapshots;
DROP TABLE usage;
DROP TABLE step_attempts;
DROP TABLE steps;
DROP TABLE pipeline_admission;
DROP TABLE run_queue;
DROP TABLE runs;
