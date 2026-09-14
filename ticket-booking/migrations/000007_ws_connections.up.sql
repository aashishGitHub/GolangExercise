-- WS connection registry (docs/plan.md decision #7): Postgres, not
-- DynamoDB, mirroring the sibling project's deliberate drop of a
-- connections table for a low-scale local stack. Revisited in the Phase 12
-- spike, where DynamoDB's high-churn per-connection TTL write pattern is a
-- genuinely stronger argument than it was in the sibling.
CREATE TABLE ws_connections (
    connection_id  UUID PRIMARY KEY,
    event_id       BIGINT NOT NULL,
    user_sub       TEXT NOT NULL,
    connected_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seq_sent  BIGINT NOT NULL DEFAULT 0,
    disconnected_at TIMESTAMPTZ
);
CREATE INDEX ws_connections_event_idx ON ws_connections (event_id) WHERE disconnected_at IS NULL;
