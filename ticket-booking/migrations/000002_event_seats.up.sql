-- The write-path table and the heart of the schema (docs/plan.md "The
-- correctness core"). Postgres is the seat-state arbiter; every hold/
-- release/confirm CAS statement targets this table and only this table
-- (see internal/inventory's package comment once Phase 3 lands it).

CREATE TABLE event_seats (
    event_id         BIGINT NOT NULL REFERENCES events (event_id),
    seat_id          BIGINT NOT NULL REFERENCES seats (seat_id),
    -- Dense 0..N-1 per event, assigned by cmd/event-publisher in
    -- section -> row -> seat order. THE bitset index and the client-facing
    -- seat identity on every wire format (docs/plan.md decision #5).
    seat_ordinal     INT NOT NULL,
    -- 0 AVAILABLE  1 HELD  2 BOOKED  3 PENDING_PAYMENT
    status           SMALLINT NOT NULL DEFAULT 0,
    -- false = structurally unsellable (closed section, obstructed, kill).
    -- Projects straight to wire state UNAVAILABLE regardless of `status` —
    -- it has no corresponding wire-state source of its own (fixed gap #2).
    sellable         BOOLEAN NOT NULL DEFAULT true,
    hold_id          UUID,
    held_by          TEXT,
    hold_expires_at  TIMESTAMPTZ,
    booking_id       UUID,
    price_cents      INT NOT NULL,
    -- deep-dive.md §9: price locked at hold time.
    hold_price_cents INT,
    version          INT NOT NULL DEFAULT 0,
    -- Monotonic per (event_id, seat_id), bumped by the DB on every Acquire.
    -- Confirm's guard is equality against this, not >= (fixed gap #3).
    fence_token      BIGINT NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, seat_id),
    CONSTRAINT event_seats_booked_has_booking_ck
        CHECK (status <> 2 OR booking_id IS NOT NULL),
    CONSTRAINT event_seats_held_has_hold_ck
        CHECK (status NOT IN (1, 3) OR (hold_id IS NOT NULL AND hold_expires_at IS NOT NULL))
);

CREATE UNIQUE INDEX event_seats_ordinal_uq ON event_seats (event_id, seat_ordinal);

-- Covers GET /events/{id}/availability's DB-scan path (Phase 2) and the
-- Acquire/Confirm CAS statements (Phase 3) — Index Only Scan on
-- (event_id, seat_ordinal) with status/sellable/price_cents piggybacked.
CREATE INDEX event_seats_cover_idx ON event_seats (event_id, seat_ordinal)
    INCLUDE (status, sellable, price_cents);

-- The reaper's scan target (Phase 5) — only ever touches HELD/PENDING_PAYMENT rows.
CREATE INDEX event_seats_expiry_idx ON event_seats (hold_expires_at)
    WHERE status IN (1, 3);
