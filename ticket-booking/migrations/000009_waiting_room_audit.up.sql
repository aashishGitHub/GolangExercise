-- Every AIMD controller tick appends one row here (docs/plan.md "Waiting
-- room"): the loop closing on real backpressure must be PLOTTED from real
-- numbers, not asserted in prose. red_* flags record which of the three
-- inputs (if any) triggered a multiplicative-decrease tick, so a postmortem
-- can see WHY the rate dropped, not just that it did.
CREATE TABLE waiting_room_audit (
    id                  BIGSERIAL PRIMARY KEY,
    event_id            BIGINT NOT NULL,
    rate                DOUBLE PRECISION NOT NULL, -- admissions/sec after this tick
    cursor_value        BIGINT NOT NULL,            -- {event:E}:cursor after this tick
    hold_p99_ms         DOUBLE PRECISION NOT NULL,
    pool_utilization    DOUBLE PRECISION NOT NULL,   -- AcquiredConns / MaxConns, 0..1
    hold_error_rate     DOUBLE PRECISION NOT NULL,   -- 0..1 over the tick's window
    red_latency         BOOLEAN NOT NULL,
    red_pool            BOOLEAN NOT NULL,
    red_errors          BOOLEAN NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX waiting_room_audit_event_idx ON waiting_room_audit (event_id, created_at);
