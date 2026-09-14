-- Transactional outbox + audit log in one: every domain write inserts a row
-- here in the same transaction as the upsert. The outbox-relay re-scans
-- published_at IS NULL and PutEvents to EventBridge — no CDC/Debezium needed
-- at this scale (see docs/study-the-basic-requirements-md-and-sleepy-lovelace.md).
CREATE TABLE domain_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL,
    schema_version INT NOT NULL DEFAULT 1,
    payload        JSONB NOT NULL,
    actor_sub      TEXT NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ
);

-- Partial index: the relay's poll query only ever looks at unpublished rows.
CREATE INDEX domain_events_unpublished_idx ON domain_events (occurred_at) WHERE published_at IS NULL;

-- Consumer-side idempotency: EventBridge/SQS are at-least-once, so every
-- consumer checks (event_id, consumer_name) here before applying a side
-- effect.
CREATE TABLE processed_events (
    event_id      UUID NOT NULL,
    consumer_name TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer_name)
);

-- dashboard-aggregator's materialized output — Phase 4's dashboard read path
-- recomputes this client-side from Dexie; this is the server-side equivalent
-- so a future cross-device/reporting view doesn't need to re-scan photos.
CREATE TABLE location_stats (
    location_id    UUID PRIMARY KEY REFERENCES disaster_locations(id),
    good_count     INT NOT NULL DEFAULT 0,
    moderate_count INT NOT NULL DEFAULT 0,
    bad_count      INT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Real AWS: DynamoDB (connection churn shouldn't hit Aurora). Local dev: no
-- DynamoDB emulator survived the LocalStack-license discovery in Phase 3, so
-- this lives in Postgres locally — functionally equivalent for our purposes,
-- revisit if connection volume ever makes that meaningfully wrong.
CREATE TABLE ws_connections (
    connection_id TEXT PRIMARY KEY,
    user_sub      TEXT NOT NULL,
    location_id   UUID NOT NULL,
    connected_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
