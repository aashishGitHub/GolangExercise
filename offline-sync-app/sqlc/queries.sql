-- name: GetLocation :one
SELECT * FROM disaster_locations WHERE id = $1;

-- name: UpsertLocation :one
-- Atomic LWW upsert: applies iff new, or existing row is strictly older.
-- Returns 0 rows (pgx.ErrNoRows) when blocked — the service falls back to
-- GetLocation to classify ignored-stale vs. conflict (equal timestamps).
INSERT INTO disaster_locations (id, name, latitude, longitude, created_at, updated_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.updated_at > disaster_locations.updated_at
RETURNING *;

-- name: GetSiteAssessment :one
SELECT * FROM site_assessments WHERE id = $1;

-- name: ListSiteAssessmentsByLocation :many
SELECT * FROM site_assessments WHERE location_id = $1 ORDER BY created_at;

-- name: UpsertSiteAssessment :one
-- Same atomic LWW pattern as UpsertLocation.
INSERT INTO site_assessments (id, location_id, name, notes, created_at, updated_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    notes = EXCLUDED.notes,
    updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.updated_at > site_assessments.updated_at
RETURNING *;

-- name: CountPhotosBySite :one
SELECT count(*) FROM photos WHERE site_assessment_id = $1;

-- name: GetPhoto :one
SELECT * FROM photos WHERE id = $1;

-- name: ListPhotosBySite :many
SELECT * FROM photos WHERE site_assessment_id = $1 ORDER BY created_at;

-- name: InsertDomainEvent :one
INSERT INTO domain_events (aggregate_type, aggregate_id, event_type, schema_version, payload, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListUnpublishedEvents :many
SELECT * FROM domain_events WHERE published_at IS NULL ORDER BY occurred_at LIMIT $1;

-- name: MarkEventPublished :exec
UPDATE domain_events SET published_at = now() WHERE id = $1;

-- name: IsEventProcessed :one
SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1 AND consumer_name = $2);

-- name: MarkEventProcessed :exec
INSERT INTO processed_events (event_id, consumer_name) VALUES ($1, $2)
ON CONFLICT (event_id, consumer_name) DO NOTHING;

-- name: UpsertLocationStats :exec
INSERT INTO location_stats (location_id, good_count, moderate_count, bad_count, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (location_id) DO UPDATE SET
    good_count     = EXCLUDED.good_count,
    moderate_count = EXCLUDED.moderate_count,
    bad_count      = EXCLUDED.bad_count,
    updated_at     = now();

-- name: GetLocationStats :one
SELECT * FROM location_stats WHERE location_id = $1;

-- name: CountPhotosByConditionForLocation :many
SELECT p.condition, count(*) AS count
FROM photos p
JOIN site_assessments s ON s.id = p.site_assessment_id
WHERE s.location_id = $1
GROUP BY p.condition;

-- name: InsertWSConnection :exec
INSERT INTO ws_connections (connection_id, user_sub, location_id) VALUES ($1, $2, $3);

-- name: DeleteWSConnection :exec
DELETE FROM ws_connections WHERE connection_id = $1;

-- name: ListUnprocessedEventsForConsumer :many
-- Idempotency lives here: an event already recorded in processed_events for
-- this consumer_name is excluded by the LEFT JOIN, so a redelivered event
-- (EventBridge/SQS are at-least-once) is simply never selected again.
SELECT de.*
FROM domain_events de
LEFT JOIN processed_events pe ON pe.event_id = de.id AND pe.consumer_name = $1
WHERE pe.event_id IS NULL AND de.event_type = ANY(sqlc.arg(event_types)::text[])
ORDER BY de.occurred_at
LIMIT $2;

-- name: ListWSConnectionsByLocation :many
SELECT * FROM ws_connections WHERE location_id = $1;

-- name: UpsertPhoto :one
-- Atomic LWW upsert *and* the <=10-photos-per-site rule in one round trip:
-- the source SELECT only yields a row when the photo already exists (update
-- path, uncapped) or the site is still under the cap (insert path) — so a
-- new photo past the cap contributes no row, the INSERT is a no-op, and
-- ON CONFLICT never fires. That makes the cap check and the id-conflict
-- check race-free without an explicit transaction or row lock: the whole
-- decision happens inside one statement.
INSERT INTO photos (id, site_assessment_id, s3_key, latitude, longitude, condition, created_at, updated_at, created_by)
SELECT
    sqlc.arg(id)::uuid,
    sqlc.arg(site_assessment_id)::uuid,
    sqlc.arg(s3_key)::text,
    sqlc.arg(latitude)::float8,
    sqlc.arg(longitude)::float8,
    sqlc.arg(condition)::text,
    sqlc.arg(created_at)::timestamptz,
    sqlc.arg(updated_at)::timestamptz,
    sqlc.arg(created_by)::text
WHERE EXISTS (SELECT 1 FROM photos WHERE id = sqlc.arg(id))
   OR (SELECT count(*) FROM photos WHERE site_assessment_id = sqlc.arg(site_assessment_id)) < 10
ON CONFLICT (id) DO UPDATE SET
    s3_key = EXCLUDED.s3_key,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    condition = EXCLUDED.condition,
    updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.updated_at > photos.updated_at
RETURNING *;
