-- Platform metadata database (controller). Separate from the test application's
-- own database per ADR-004. Append-only events; everything else versioned by run.

CREATE TABLE IF NOT EXISTS scenario_versions (
    version_id      TEXT PRIMARY KEY,
    scenario_id     TEXT NOT NULL,
    body            JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runs (
    run_id              TEXT PRIMARY KEY,
    scenario_id         TEXT NOT NULL,
    version_id          TEXT NOT NULL REFERENCES scenario_versions(version_id),
    idempotency_key     TEXT NOT NULL,
    state               TEXT NOT NULL,
    outcome             TEXT,
    stop_reason         TEXT,
    active_intervention_ids JSONB NOT NULL DEFAULT '[]',
    runs_consumed       INTEGER NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (scenario_id, idempotency_key)
);

-- Append-only: rows are never updated or deleted, matching docs/07-architecture.md.
CREATE TABLE IF NOT EXISTS events (
    event_id            TEXT PRIMARY KEY,
    run_id              TEXT NOT NULL REFERENCES runs(run_id),
    logical_operation   TEXT NOT NULL,
    source              TEXT NOT NULL,
    target              TEXT NOT NULL,
    method              TEXT NOT NULL,
    normalized_path     TEXT NOT NULL,
    attempt             INTEGER NOT NULL,
    parent_event_id     TEXT,
    monotonic_offset_ms BIGINT NOT NULL,
    action_id           TEXT,
    phase               TEXT NOT NULL,
    result              TEXT NOT NULL,
    status_code         INTEGER,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_events_run_id ON events(run_id, created_at);

CREATE TABLE IF NOT EXISTS candidates (
    candidate_id        TEXT PRIMARY KEY,
    reduction_id        TEXT NOT NULL,
    run_id              TEXT NOT NULL REFERENCES runs(run_id),
    intervention_ids    JSONB NOT NULL,
    outcome             TEXT NOT NULL,
    accepted            BOOLEAN NOT NULL DEFAULT false,
    confirmations        INTEGER NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_candidates_reduction_id ON candidates(reduction_id);

CREATE TABLE IF NOT EXISTS assertions (
    run_id          TEXT PRIMARY KEY REFERENCES runs(run_id),
    invariant_id    TEXT NOT NULL,
    expected        TEXT NOT NULL,
    actual          TEXT NOT NULL,
    violated        BOOLEAN NOT NULL,
    infra_error     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per reduction (FR08/NFR06): the last candidate confirmed 3/3, budget consumed
-- and stop reason. Mirrored by store.EnsureSchema for already-initialised volumes.
CREATE TABLE IF NOT EXISTS reductions (
    reduction_id TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL REFERENCES runs(run_id),
    state        TEXT NOT NULL,
    stop_reason  TEXT,
    body         JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
