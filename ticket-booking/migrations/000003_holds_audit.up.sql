-- Every hold attempt, won or lost — the table the "exactly one winner"
-- race tests query directly (docs/plan.md Phase 3 verification). Written
-- OUTSIDE the hold transaction (a lost CAS has no transaction to ride on)
-- so it never adds latency to the hot path.
CREATE TABLE holds_audit (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hold_id     UUID NOT NULL,
    event_id    BIGINT NOT NULL,
    seat_id     BIGINT NOT NULL,
    user_sub    TEXT NOT NULL,
    -- ACQUIRED | LOST_REDIS | LOST_DB_CAS | RELEASED | EXPIRED_RECLAIMED
    -- | CONFIRMED | EXTENDED | STALE_FENCE_REJECTED
    outcome     TEXT NOT NULL,
    fence_token BIGINT,
    expires_at  TIMESTAMPTZ,
    latency_ms  INT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX holds_audit_seat_idx ON holds_audit (event_id, seat_id, created_at);
