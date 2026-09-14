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

-- name: GetEvent :one
SELECT * FROM events WHERE event_id = $1;

-- name: SetEventOnSale :exec
UPDATE events SET status = 'ON_SALE' WHERE event_id = $1;

-- ListEvents: simple keyset pagination by event_id, optional trigram search
-- over title/artist (pg_trgm GIN-accelerated ILIKE — docs/plan.md decision #8).
-- name: ListEvents :many
SELECT * FROM events
WHERE ($1::text = '' OR title ILIKE '%' || $1 || '%' OR artist ILIKE '%' || $1 || '%')
  AND ($2::bigint = 0 OR event_id > $2)
ORDER BY event_id
LIMIT $3;

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
GROUP BY s.section_id
HAVING bool_and(NOT es.sellable);
