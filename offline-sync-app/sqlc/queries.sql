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

-- ===========================================================================
-- Two-way sync: inbound webhooks + SaaS<->SaaS (internal/webhook, internal/twoway)
-- ===========================================================================

-- name: InsertExternalLink :exec
INSERT INTO external_id_map (system, external_id, internal_id)
VALUES ($1, $2, $3)
ON CONFLICT (system, external_id) DO NOTHING;

-- name: GetExternalLink :one
SELECT * FROM external_id_map WHERE system = $1 AND external_id = $2;

-- name: GetExternalLinkByInternal :one
SELECT * FROM external_id_map WHERE system = $1 AND internal_id = $2;

-- name: AdvanceLinkVersion :one
-- Atomic version gate: 0 rows (pgx.ErrNoRows) unless the incoming external
-- version is strictly newer than the last applied one. Also row-locks the
-- link until the surrounding transaction ends.
UPDATE external_id_map SET last_version = sqlc.arg(version)
WHERE system = $1 AND external_id = $2 AND last_version < sqlc.arg(version)
RETURNING *;

-- name: GetWorkItemForUpdate :one
SELECT * FROM work_items WHERE id = $1 FOR UPDATE;

-- name: UpsertWorkItem :one
INSERT INTO work_items (id, title, status, notes, deleted_at)
VALUES (
    sqlc.arg(id), sqlc.arg(title), sqlc.arg(status), sqlc.arg(notes),
    CASE WHEN sqlc.arg(deleted)::bool THEN now() END
)
ON CONFLICT (id) DO UPDATE SET
    title      = EXCLUDED.title,
    status     = EXCLUDED.status,
    notes      = EXCLUDED.notes,
    deleted_at = CASE WHEN sqlc.arg(deleted)::bool THEN COALESCE(work_items.deleted_at, now()) END,
    updated_at = now()
RETURNING *;

-- name: UpdateWorkItemNotes :one
UPDATE work_items SET notes = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: GetSyncPushed :one
SELECT * FROM sync_pushed WHERE system = $1 AND internal_id = $2;

-- name: UpsertSyncPushed :exec
INSERT INTO sync_pushed (system, internal_id, external_version, content_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (system, internal_id) DO UPDATE SET
    external_version = EXCLUDED.external_version,
    content_hash     = EXCLUDED.content_hash,
    pushed_at        = now()
WHERE EXCLUDED.external_version > sync_pushed.external_version;

-- name: GetSyncState :one
SELECT * FROM sync_state WHERE system = $1;

-- name: UpsertSyncState :exec
INSERT INTO sync_state (system, watermark, run_since, next_cursor, run_max)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (system) DO UPDATE SET
    watermark   = EXCLUDED.watermark,
    run_since   = EXCLUDED.run_since,
    next_cursor = EXCLUDED.next_cursor,
    run_max     = EXCLUDED.run_max,
    updated_at  = now();

-- name: InsertWebhookInbox :execrows
-- 1 row = first delivery, 0 rows = duplicate delivery id.
INSERT INTO webhook_inbox (system, delivery_id, body)
VALUES ($1, $2, $3)
ON CONFLICT (system, delivery_id) DO NOTHING;

-- name: ListPendingInbox :many
SELECT delivery_id FROM webhook_inbox
WHERE system = $1 AND processed_at IS NULL AND dead_at IS NULL
ORDER BY received_at, delivery_id
LIMIT $2;

-- name: ClaimInboxRow :one
SELECT * FROM webhook_inbox
WHERE system = $1 AND delivery_id = $2 AND processed_at IS NULL AND dead_at IS NULL
FOR UPDATE SKIP LOCKED;

-- name: MarkInboxProcessed :exec
UPDATE webhook_inbox SET processed_at = now(), outcome = $3
WHERE system = $1 AND delivery_id = $2;

-- name: RecordInboxFailure :one
UPDATE webhook_inbox SET attempts = attempts + 1, last_error = $3
WHERE system = $1 AND delivery_id = $2
RETURNING *;

-- name: MarkInboxDead :exec
UPDATE webhook_inbox SET dead_at = now()
WHERE system = $1 AND delivery_id = $2;

-- name: InsertDeadLetter :exec
INSERT INTO sync_dead_letters (source, system, ref_id, payload, reason, attempts)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListDeadLetters :many
SELECT * FROM sync_dead_letters WHERE system = $1 ORDER BY created_at;
