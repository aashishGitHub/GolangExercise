-- Transactional outbox (docs/plan.md decision #2's mechanism): every
-- inventory-package write that actually changes seat state gets exactly one
-- row here, in the SAME transaction as the CAS — a crash between commit and
-- EventBridge publish leaves a row with published_at IS NULL for the relay
-- to pick up (never a lost event, never a duplicate DB write).
CREATE TABLE domain_events (
    event_id       UUID PRIMARY KEY,
    -- TEXT, not UUID: aggregates are a mix of BIGINT (event_seats,
    -- keyed by event_id:seat_id) and UUID (orders, holds) — a deliberate
    -- deviation from the sibling project's UUID-only aggregate_id.
    aggregate_id   TEXT NOT NULL,
    event_type     TEXT NOT NULL, -- e.g. "seat.held", "seat.released", "seat.booked"
    schema_version INT NOT NULL DEFAULT 1,
    payload        JSONB NOT NULL,
    published_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX domain_events_unpublished_idx ON domain_events (created_at) WHERE published_at IS NULL;

-- Per-consumer idempotency — dedup is scoped to (event_id, consumer_name),
-- not global, so N independent consumers can each process the same event
-- exactly once without coordinating with each other.
CREATE TABLE processed_events (
    event_id      UUID NOT NULL,
    consumer_name TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer_name)
);
