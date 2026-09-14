-- Phase 2: venue-template writes (used by scripts/seed-venue) and the
-- catalog/availability/pricing read path. Hold/release/confirm CAS queries
-- land in Phase 3 alongside internal/inventory.

-- name: CreateVenue :one
INSERT INTO venues (name, city) VALUES ($1, $2)
RETURNING *;

-- name: CreateSection :one
INSERT INTO sections (venue_id, name, tier, display_order, closed)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateSeatRow :one
INSERT INTO seat_rows (section_id, label, display_order)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateSeat :one
INSERT INTO seats (row_id, seat_label, x_coord, y_coord)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetVenue :one
SELECT * FROM venues WHERE venue_id = $1;

-- ListVenueSeatsOrdered drives both cmd/event-publisher's ordinal
-- assignment and layout.json/seats.bin rendering from the SAME walk, in the
-- SAME order (section.display_order, row.display_order, seat_label) — so
-- the DB's ordinal assignment and the static layout files can never
-- disagree (docs/plan.md "Event publish pipeline").
-- name: ListVenueSeatsOrdered :many
SELECT
    s.section_id, s.name AS section_name, s.tier AS section_tier,
    s.display_order AS section_display_order, s.closed AS section_closed,
    r.row_id, r.label AS row_label, r.display_order AS row_display_order,
    st.seat_id, st.seat_label, st.x_coord, st.y_coord
FROM sections s
JOIN seat_rows r ON r.section_id = s.section_id
JOIN seats st ON st.row_id = r.row_id
WHERE s.venue_id = $1
ORDER BY s.display_order, r.display_order, st.seat_label;

-- name: CreateEvent :one
INSERT INTO events (venue_id, artist, title, starts_at, onsale_at, home_region, layout_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: CreateEventPriceTier :exec
INSERT INTO event_price_tiers (event_id, tier, price_cents) VALUES ($1, $2, $3);

-- name: ListEventPriceTiers :many
SELECT * FROM event_price_tiers WHERE event_id = $1 ORDER BY tier;

-- name: MinEventPriceCents :one
SELECT min(price_cents)::int FROM event_price_tiers WHERE event_id = $1;

-- CountAvailableSeats: fine at catalog-listing scale (one query per event in
-- a <=100-row page); revisit with a materialized per-event counter only if
-- this becomes a measured bottleneck.
-- name: CountAvailableSeats :one
SELECT count(*) FROM event_seats WHERE event_id = $1 AND status = 0 AND sellable;

-- name: GetEvent :one
SELECT * FROM events WHERE event_id = $1;

-- name: SetEventOnSale :exec
UPDATE events SET status = 'ON_SALE' WHERE event_id = $1;

-- ListEvents: simple keyset pagination by event_id, optional trigram search
-- over title/artist (pg_trgm GIN-accelerated ILIKE — docs/plan.md decision #8).
-- name: ListEvents :many
SELECT * FROM events
WHERE (sqlc.arg(search)::text = '' OR title ILIKE '%' || sqlc.arg(search) || '%' OR artist ILIKE '%' || sqlc.arg(search) || '%')
  AND (sqlc.arg(after_id)::bigint = 0 OR event_id > sqlc.arg(after_id))
ORDER BY event_id
LIMIT sqlc.arg(row_limit);

-- BulkInsertEventSeats: cmd/event-publisher's population step, via pgx's
-- CopyFrom (sqlc :copyfrom) rather than one INSERT per row — the walk is
-- 30,000+ rows for a large venue.
-- name: BulkInsertEventSeats :copyfrom
INSERT INTO event_seats (event_id, seat_id, seat_ordinal, status, sellable, price_cents)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: CountEventSeats :one
SELECT count(*) FROM event_seats WHERE event_id = $1;

-- ListEventSeatsForAvailability: the Phase 2 DB-scan path for
-- GET /events/{id}/availability — Index Only Scan on event_seats_cover_idx.
-- Superseded by the Redis-backed projector path in Phase 7, DB fallback
-- kept for when Redis is cold/down.
-- name: ListEventSeatsForAvailability :many
SELECT seat_ordinal, status, sellable
FROM event_seats
WHERE event_id = $1
ORDER BY seat_ordinal;

-- A section counts as closed for this event only once every seat in it is
-- unsellable — closedSections is denormalized FROM event_seats.sellable,
-- never the other way round (docs/plan.md, fixed gap #2).
-- name: ListClosedSectionsForEvent :many
SELECT s.section_id
FROM sections s
JOIN seat_rows r ON r.section_id = s.section_id
JOIN seats st ON st.row_id = r.row_id
JOIN event_seats es ON es.seat_id = st.seat_id AND es.event_id = $1
GROUP BY s.section_id, s.display_order
HAVING bool_and(NOT es.sellable)
ORDER BY s.display_order;

-- Phase 3: the correctness core (docs/plan.md "The correctness core").
-- Callers MUST pre-sort seat_ids ascending (and fences alongside them for
-- ConfirmSeats) — that ordering is what makes deadlock between two
-- concurrent multi-seat CAS statements structurally impossible, not just
-- unlikely (fixed gap #8).

-- name: GetSeatIDsByOrdinals :many
SELECT seat_id, seat_ordinal FROM event_seats
WHERE event_id = sqlc.arg(event_id) AND seat_ordinal = ANY(sqlc.arg(ordinals)::int[]);

-- AcquireHold: the arbiter. `sellable` gates the CAS so a structurally
-- unsellable seat can never be held; the status predicate is passive
-- expiry — the WHERE clause IS the guarantee, not a timer.
-- name: AcquireHold :many
UPDATE event_seats
   SET status = 1,
       hold_id = sqlc.arg(hold_id), held_by = sqlc.arg(user_sub),
       hold_expires_at = now() + make_interval(secs => sqlc.arg(ttl_seconds)::int),
       hold_price_cents = price_cents,
       fence_token = fence_token + 1, version = version + 1, updated_at = now()
 WHERE event_id = sqlc.arg(event_id) AND seat_id = ANY(sqlc.arg(seat_ids)::bigint[])
   AND sellable
   AND (status = 0 OR (status IN (1, 3) AND hold_expires_at < now()))
RETURNING seat_id, seat_ordinal, fence_token, hold_expires_at, hold_price_cents;

-- ReleaseHold: idempotent by construction — rowcount 0 is success (the
-- hold was already gone), not an error.
-- name: ReleaseHold :execrows
UPDATE event_seats
   SET status = 0, hold_id = NULL, held_by = NULL, hold_expires_at = NULL, hold_price_cents = NULL,
       version = version + 1, updated_at = now()
 WHERE event_id = sqlc.arg(event_id) AND seat_id = ANY(sqlc.arg(seat_ids)::bigint[])
   AND status IN (1, 3) AND hold_id = sqlc.arg(hold_id);

-- ExtendHold: called at payment initiation. rowcount < len(seats) means the
-- hold already lapsed — the saga must abort BEFORE charging.
-- name: ExtendHold :execrows
UPDATE event_seats
   SET status = 3,
       hold_expires_at = greatest(hold_expires_at, now() + make_interval(secs => sqlc.arg(extend_seconds)::int)),
       version = version + 1, updated_at = now()
 WHERE event_id = sqlc.arg(event_id) AND seat_id = ANY(sqlc.arg(seat_ids)::bigint[])
   AND status IN (1, 3) AND hold_id = sqlc.arg(hold_id) AND hold_expires_at > now();

-- ConfirmSeats: the single most important statement in the whole system.
-- fence_token equality (not >=, see fixed gap #3) is the RedLock-question
-- answer — a zombie holder whose hold was reclaimed and re-issued fails
-- here even if it somehow still holds a matching hold_id.
-- Two single-arg unnest()s zipped by WITH ORDINALITY, not the two-array
-- unnest(a, b) form — sqlc's own function catalog doesn't model that
-- overload even though Postgres itself supports it since 9.4.
-- name: ConfirmSeats :execrows
UPDATE event_seats es
   SET status = 2, booking_id = sqlc.arg(order_id), hold_id = NULL,
       held_by = NULL, hold_expires_at = NULL, version = version + 1, updated_at = now()
  FROM unnest(sqlc.arg(seat_ids)::bigint[]) WITH ORDINALITY AS s (seat_id, ord)
  JOIN unnest(sqlc.arg(fences)::bigint[]) WITH ORDINALITY AS f (fence, ord) ON s.ord = f.ord
 WHERE es.event_id = sqlc.arg(event_id) AND es.seat_id = s.seat_id
   AND es.status IN (1, 3) AND es.hold_id = sqlc.arg(hold_id)
   AND es.fence_token = f.fence
   AND es.hold_expires_at > now();

-- name: InsertHoldsAudit :exec
INSERT INTO holds_audit (hold_id, event_id, seat_id, user_sub, outcome, fence_token, expires_at, latency_ms)
VALUES (sqlc.arg(hold_id), sqlc.arg(event_id), sqlc.arg(seat_id), sqlc.arg(user_sub),
        sqlc.arg(outcome), sqlc.arg(fence_token), sqlc.arg(expires_at), sqlc.arg(latency_ms));

-- GetSeatsByHoldID: the lookup GET/DELETE /holds/{id} and POST
-- /holds/{id}/extend need — no separate `holds` table exists, so a hold's
-- seats are found by hold_id alone via event_seats_hold_id_idx.
-- name: GetSeatsByHoldID :many
SELECT event_id, seat_id, seat_ordinal, held_by, hold_price_cents, hold_expires_at, fence_token
FROM event_seats
WHERE hold_id = sqlc.arg(hold_id)
ORDER BY seat_id;

-- Phase 6: the payment saga (docs/plan.md "The saga").

-- name: CreateOrder :one
INSERT INTO orders (order_id, user_sub, event_id, hold_id, seat_ids, amount_cents)
VALUES (sqlc.arg(order_id), sqlc.arg(user_sub), sqlc.arg(event_id), sqlc.arg(hold_id), sqlc.arg(seat_ids), sqlc.arg(amount_cents))
RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders WHERE order_id = sqlc.arg(order_id);

-- name: GetOrderByHoldID :one
SELECT * FROM orders WHERE hold_id = sqlc.arg(hold_id);

-- name: UpdateOrderStatus :exec
UPDATE orders SET status = sqlc.arg(status), updated_at = now() WHERE order_id = sqlc.arg(order_id);

-- name: FailOrder :exec
UPDATE orders SET status = 'FAILED', failure_code = sqlc.arg(failure_code),
                   failure_detail = sqlc.arg(failure_detail), updated_at = now()
WHERE order_id = sqlc.arg(order_id);

-- name: ReallocateOrder :exec
UPDATE orders SET hold_id = sqlc.arg(hold_id), seat_ids = sqlc.arg(seat_ids),
                   reallocated = true, updated_at = now()
WHERE order_id = sqlc.arg(order_id);

-- name: CreateSagaStep :exec
INSERT INTO order_saga_steps (order_id, step) VALUES (sqlc.arg(order_id), sqlc.arg(step));

-- name: UpdateSagaStep :exec
UPDATE order_saga_steps SET state = sqlc.arg(state), last_error = sqlc.arg(last_error),
                             attempts = attempts + 1, updated_at = now()
WHERE order_id = sqlc.arg(order_id) AND step = sqlc.arg(step);

-- name: GetSagaStep :one
SELECT * FROM order_saga_steps WHERE order_id = sqlc.arg(order_id) AND step = sqlc.arg(step);

-- name: ListStuckOrders :many
SELECT order_id FROM orders
WHERE status IN ('PENDING', 'AUTHORIZING', 'CONFIRMING') AND updated_at < now() - make_interval(secs => sqlc.arg(stuck_after_seconds)::int)
ORDER BY created_at
LIMIT sqlc.arg(row_limit);

-- name: InsertPayment :one
INSERT INTO payments (payment_id, order_id, idempotency_key, amount_cents, status)
VALUES (sqlc.arg(payment_id), sqlc.arg(order_id), sqlc.arg(idempotency_key), sqlc.arg(amount_cents), 'PENDING')
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetPaymentByIdempotencyKey :one
SELECT * FROM payments WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: UpdatePaymentStatus :exec
UPDATE payments SET status = sqlc.arg(status), provider_ref = sqlc.arg(provider_ref),
                     last_error = sqlc.arg(last_error), attempts = attempts + 1, updated_at = now()
WHERE payment_id = sqlc.arg(payment_id);

-- name: ListStuckPayments :many
SELECT * FROM payments
WHERE status IN ('PENDING', 'UNKNOWN') AND created_at < now() - make_interval(secs => sqlc.arg(stuck_after_seconds)::int)
ORDER BY created_at
LIMIT sqlc.arg(row_limit);

-- name: InsertRefund :one
INSERT INTO refunds (refund_id, payment_id, provider_ref, amount_cents, status)
VALUES (sqlc.arg(refund_id), sqlc.arg(payment_id), sqlc.arg(provider_ref), sqlc.arg(amount_cents), sqlc.arg(status))
RETURNING *;

-- The money invariant, half 1: a CONFIRMED/TICKETED order with no
-- CAPTURED payment — must always be 0 rows.
-- name: FindOrdersMissingPayment :many
SELECT o.order_id FROM orders o
LEFT JOIN payments p ON p.order_id = o.order_id AND p.status = 'CAPTURED'
WHERE o.status IN ('CONFIRMED', 'TICKETED') AND p.payment_id IS NULL;

-- The money invariant, half 2: a CAPTURED payment with no confirmed order
-- and no completed refund — must always be 0 rows.
-- name: FindCapturedPaymentsMissingOrderOrRefund :many
SELECT p.payment_id FROM payments p
WHERE p.status = 'CAPTURED'
  AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.order_id = p.order_id AND o.status IN ('CONFIRMED', 'TICKETED'))
  AND NOT EXISTS (SELECT 1 FROM refunds r WHERE r.payment_id = p.payment_id AND r.status = 'COMPLETED');

-- name: InsertDomainEvent :exec
INSERT INTO domain_events (event_id, aggregate_id, event_type, schema_version, payload)
VALUES (sqlc.arg(event_id), sqlc.arg(aggregate_id), sqlc.arg(event_type), sqlc.arg(schema_version), sqlc.arg(payload));

-- name: ListUnpublishedDomainEvents :many
SELECT event_id, aggregate_id, event_type, schema_version, payload
FROM domain_events
WHERE published_at IS NULL
ORDER BY outbox_seq
LIMIT sqlc.arg(row_limit);

-- name: MarkDomainEventPublished :exec
UPDATE domain_events SET published_at = now() WHERE event_id = sqlc.arg(event_id);

-- ListExpiredHolds: cmd/hold-reaper's scan target — the ACTIVE release
-- (UX freshness only; passive expiry in the CAS predicate is the actual
-- correctness guarantee, docs/plan.md decision #2).
-- name: ListExpiredHolds :many
SELECT DISTINCT event_id, hold_id
FROM event_seats
WHERE status IN (1, 3) AND hold_expires_at < now() AND hold_id IS NOT NULL;

-- name: ListSeatIDsForHold :many
SELECT seat_id FROM event_seats WHERE event_id = sqlc.arg(event_id) AND hold_id = sqlc.arg(hold_id);

-- ListAvailableForBestAvailable: candidate seats for the contiguous-run
-- scan (internal/inventory.BestAvailable) — row_id is included because
-- ordinals are contiguous ACROSS a whole section, not just within one row
-- (decision #2), so a run must be broken at a row boundary in Go, not just
-- by ordinal adjacency.
-- name: ListAvailableForBestAvailable :many
SELECT es.seat_id, es.seat_ordinal, st.row_id
FROM event_seats es
JOIN seats st ON st.seat_id = es.seat_id
WHERE es.event_id = sqlc.arg(event_id) AND es.status = 0 AND es.sellable
  AND es.price_cents <= sqlc.arg(max_price_cents)
ORDER BY es.seat_ordinal;

-- Phase 7 (internal/projector): domain events not yet applied by THIS
-- consumer. processed_events dedup is scoped to (event_id, consumer_name)
-- (migration 0005's comment), so the projector can replay independently of
-- the EventBridge outbox relay — a crash mid-batch just re-applies from the
-- same point, and the bitset write is itself idempotent (see projector.go).
-- name: ListUnprocessedDomainEvents :many
SELECT de.event_id, de.aggregate_id, de.event_type, de.schema_version, de.payload, de.created_at
FROM domain_events de
WHERE de.event_type IN ('seat.held', 'seat.released', 'seat.booked')
  AND NOT EXISTS (
    SELECT 1 FROM processed_events pe
    WHERE pe.event_id = de.event_id AND pe.consumer_name = sqlc.arg(consumer_name)
  )
-- outbox_seq, NOT created_at (migration 0008): created_at alone is not a
-- strict total order under rapid sequential inserts, and this consumer's
-- correctness genuinely depends on applying transitions in real order —
-- unlike the outbox relay, which doesn't care about delivery order.
ORDER BY de.outbox_seq
LIMIT sqlc.arg(row_limit);

-- name: MarkEventProcessed :exec
INSERT INTO processed_events (event_id, consumer_name)
VALUES (sqlc.arg(event_id), sqlc.arg(consumer_name))
ON CONFLICT DO NOTHING;

-- ListSeatOrdinalsByEvent: the seat_id -> ordinal map the projector caches
-- per event_id (in-memory, rebuilt on restart) — domain event payloads key
-- by seat_id (the internal identity), but every wire structure (bitset,
-- deltas) is ordinal-indexed (docs/plan.md decision #5).
-- name: ListSeatOrdinalsByEvent :many
SELECT seat_id, seat_ordinal FROM event_seats WHERE event_id = sqlc.arg(event_id);

-- name: InsertWSConnection :exec
INSERT INTO ws_connections (connection_id, event_id, user_sub)
VALUES (sqlc.arg(connection_id), sqlc.arg(event_id), sqlc.arg(user_sub));

-- name: UpdateWSConnectionSeq :exec
UPDATE ws_connections SET last_seq_sent = sqlc.arg(last_seq_sent) WHERE connection_id = sqlc.arg(connection_id);

-- name: CloseWSConnection :exec
UPDATE ws_connections SET disconnected_at = now() WHERE connection_id = sqlc.arg(connection_id);

-- Phase 8 (internal/waitingroom): one row per AIMD controller tick — the
-- mechanism that lets "the loop closing on real backpressure" be plotted
-- from real numbers instead of asserted in prose.
-- name: InsertWaitingRoomAudit :exec
INSERT INTO waiting_room_audit
  (event_id, rate, cursor_value, hold_p99_ms, pool_utilization, hold_error_rate,
   red_latency, red_pool, red_errors)
VALUES
  (sqlc.arg(event_id), sqlc.arg(rate), sqlc.arg(cursor_value), sqlc.arg(hold_p99_ms),
   sqlc.arg(pool_utilization), sqlc.arg(hold_error_rate),
   sqlc.arg(red_latency), sqlc.arg(red_pool), sqlc.arg(red_errors));

-- name: ListWaitingRoomAudit :many
SELECT * FROM waiting_room_audit WHERE event_id = sqlc.arg(event_id) ORDER BY id;

-- ListAllEventIDs: cmd/waiting-room-controller's per-tick scan target — at
-- local-dev scale (dozens of events) ticking every event every second is
-- cheap; a real deployment would scope this to events currently on sale.
-- name: ListAllEventIDs :many
SELECT event_id FROM events ORDER BY event_id;
