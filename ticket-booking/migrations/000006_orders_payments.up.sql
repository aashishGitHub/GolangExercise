-- The saga's data model (docs/plan.md "The saga"). orders.status IS the
-- saga state machine:
--   PENDING -> AUTHORIZING -> CAPTURED -> CONFIRMING -> CONFIRMED -> TICKETED
--                  |              |           |
--                  |              |           +-> COMPENSATING -> REALLOCATED (->CONFIRMED)
--                  |              |                             -> REFUNDING -> COMPENSATED
--                  |              +-> FAILED (confirm never attempted, no money)
--                  +-> FAILED (declined / hold_expired, no money)
--   CANCELLED <- user cancel before CAPTURED

CREATE TABLE orders (
    order_id      UUID PRIMARY KEY,
    user_sub      TEXT NOT NULL,
    event_id      BIGINT NOT NULL,
    hold_id       UUID NOT NULL,
    seat_ids      BIGINT[] NOT NULL,
    amount_cents  INT NOT NULL,
    currency      TEXT NOT NULL DEFAULT 'USD',
    status        TEXT NOT NULL DEFAULT 'PENDING',
    reallocated   BOOLEAN NOT NULL DEFAULT false,
    failure_code   TEXT,
    failure_detail TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (hold_id) -- one order per LIVE hold, structurally
);
CREATE INDEX orders_user_idx ON orders (user_sub, created_at DESC);

-- payments is a separate table from refunds (docs/plan.md, fixed gap #11):
-- a captured payment's row stays queryable as CAPTURED and a refund is
-- independently auditable, rather than a status flip that would make the
-- reconciler's "captured with no refund" check unreachable dead logic.
CREATE TABLE payments (
    payment_id      UUID PRIMARY KEY,
    order_id        UUID NOT NULL REFERENCES orders (order_id),
    idempotency_key TEXT NOT NULL,
    provider_ref    TEXT,
    amount_cents    INT NOT NULL,
    -- PENDING | CAPTURED | FAILED | UNKNOWN (ambiguous timeout — deep-dive.md §6)
    status          TEXT NOT NULL,
    attempts        INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (idempotency_key) -- deep-dive.md §6's real guard: insert-first ON CONFLICT DO NOTHING
);
CREATE INDEX payments_stuck_idx ON payments (created_at) WHERE status IN ('PENDING', 'UNKNOWN');

CREATE TABLE refunds (
    refund_id    UUID PRIMARY KEY,
    payment_id   UUID NOT NULL REFERENCES payments (payment_id),
    provider_ref TEXT,
    amount_cents INT NOT NULL,
    status       TEXT NOT NULL, -- PENDING | COMPLETED | FAILED
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE order_saga_steps (
    order_id   UUID NOT NULL REFERENCES orders (order_id),
    step       TEXT NOT NULL, -- extend_hold | charge | confirm | issue_ticket
    state      TEXT NOT NULL DEFAULT 'PENDING', -- PENDING | DONE | COMPENSATED | FAILED
    attempts   INT NOT NULL DEFAULT 0,
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (order_id, step)
);

-- HTTP-level idempotency (docs/plan.md "Idempotency-Key required") —
-- distinct from payments.idempotency_key, which dedupes the payment
-- specifically; this dedupes the REQUEST.
CREATE TABLE idempotency_keys (
    key             TEXT PRIMARY KEY,
    user_sub        TEXT NOT NULL,
    route           TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    response_status INT,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);
