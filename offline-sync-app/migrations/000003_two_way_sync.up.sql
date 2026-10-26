CREATE TABLE work_items (
    id         UUID PRIMARY KEY,
    title      TEXT NOT NULL,
    status     TEXT NOT NULL,
    notes      TEXT NOT NULL DEFAULT '',
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Entity-agnostic on purpose: no FK to work_items. last_version is the
-- highest external version applied for this link; it is the version gate.
CREATE TABLE external_id_map (
    system       TEXT NOT NULL,
    external_id  TEXT NOT NULL,
    internal_id  UUID NOT NULL,
    last_version BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (system, external_id),
    UNIQUE (system, internal_id)
);

-- Echo memory: what the reverse loader last wrote to the external system.
CREATE TABLE sync_pushed (
    system           TEXT NOT NULL,
    internal_id      UUID NOT NULL,
    external_version BIGINT NOT NULL,
    content_hash     TEXT NOT NULL,
    pushed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (system, internal_id)
);

-- Forward extractor checkpoint. next_cursor <> '' means a run is in flight
-- and run_since/run_max belong to it.
CREATE TABLE sync_state (
    system      TEXT PRIMARY KEY,
    watermark   TIMESTAMPTZ,
    run_since   TIMESTAMPTZ,
    next_cursor TEXT NOT NULL DEFAULT '',
    run_max     TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Inbox: the primary key is the dedupe key and the row is the raw event, so
-- "seen this delivery" and "raw event persisted" cannot disagree.
CREATE TABLE webhook_inbox (
    system       TEXT NOT NULL,
    delivery_id  TEXT NOT NULL,
    body         BYTEA NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts     INT NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT '',
    outcome      TEXT NOT NULL DEFAULT '',
    processed_at TIMESTAMPTZ,
    dead_at      TIMESTAMPTZ,
    PRIMARY KEY (system, delivery_id)
);
CREATE INDEX webhook_inbox_pending_idx ON webhook_inbox (received_at)
    WHERE processed_at IS NULL AND dead_at IS NULL;

CREATE TABLE sync_dead_letters (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source     TEXT NOT NULL CHECK (source IN ('inbox', 'loader')),
    system     TEXT NOT NULL,
    ref_id     TEXT NOT NULL,
    payload    BYTEA NOT NULL,
    reason     TEXT NOT NULL,
    attempts   INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
