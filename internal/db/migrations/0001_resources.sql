-- Resources (Module, Pipeline) with their revision history, settings and the audit log.

-- +goose Up
CREATE TABLE resources (
    kind             text        NOT NULL CHECK (kind IN ('Module', 'Pipeline')),
    name             text        NOT NULL,
    current_revision integer     NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, name)
);

-- Revisions survive deletion of the resource, so a re-created resource continues its numbering.
CREATE TABLE resource_revisions (
    kind       text        NOT NULL,
    name       text        NOT NULL,
    revision   integer     NOT NULL CHECK (revision > 0),
    spec       jsonb       NOT NULL,
    yaml       text        NOT NULL,
    spec_hash  text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    applied_by text        NOT NULL,
    source     text        NOT NULL CHECK (source IN ('cli', 'ui', 'bootstrap', 'api')),
    PRIMARY KEY (kind, name, revision)
);

ALTER TABLE resources
    ADD CONSTRAINT resources_current_revision_fk
    FOREIGN KEY (kind, name, current_revision) REFERENCES resource_revisions (kind, name, revision);

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit (
    id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at      timestamptz NOT NULL DEFAULT now(),
    actor   text        NOT NULL,
    action  text        NOT NULL,
    target  text        NOT NULL,
    details jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_at_idx ON audit (at DESC);

-- +goose Down
DROP TABLE audit;
DROP TABLE settings;
DROP TABLE resources;
DROP TABLE resource_revisions;
