-- docs/plan.md: tickets carry a SECOND, independent overbooking tripwire —
-- UNIQUE (event_id, seat_id) WHERE revoked_at IS NULL — even if the CAS in
-- internal/inventory were ever wrong, two live tickets for one seat cannot
-- be inserted. Numbered 0010, not the plan's original 0008: that number
-- went to Phase 7's real domain_events.outbox_seq bug fix instead.
CREATE TABLE tickets (
    ticket_id    UUID PRIMARY KEY,
    order_id     UUID NOT NULL REFERENCES orders (order_id),
    event_id     BIGINT NOT NULL,
    seat_id      BIGINT NOT NULL,
    qr_s3_key    TEXT NOT NULL,
    redeemed_at  TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX tickets_seat_live_uq ON tickets (event_id, seat_id) WHERE revoked_at IS NULL;
CREATE INDEX tickets_order_idx ON tickets (order_id);

-- Reminder one-shots (T-24h/T-2h): dedup so a restarted scheduler never
-- double-sends. One row per (ticket_id, kind) — matches the
-- processed_events per-consumer dedup pattern already used elsewhere.
CREATE TABLE reminders_sent (
    ticket_id  UUID NOT NULL REFERENCES tickets (ticket_id),
    kind       TEXT NOT NULL, -- 'T-24h' | 'T-2h'
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ticket_id, kind)
);
