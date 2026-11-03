# Phase 2 — Sync API + DB: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

> Note: MILESTONES describes `internal/sync` as the thing `POST /api/sync` calls. In the code, the router builds `sync.NewTxRunner(pool)`, which wraps `Service` in one transaction and writes `domain_events` rows (added in Phase 7). `Service` itself is unchanged and is what the Phase 2 tests exercise.

> Note: MILESTONES says the sync engine has "auto-retry" that "resets on `online` event". `startAutoSync` exists in [syncEngine.ts](../../../web/src/sync/syncEngine.ts), but nothing in `web/src` calls it. Only the "Sync now" button in `CapturePanel.tsx` runs `drainOutbox`. So auto-retry is written but not wired.

> Note: no Playwright files are committed. The E2E run in MILESTONES was a manual one-time check. There are no JavaScript unit tests either.

## [migrations/000001_init_schema.up.sql](../../../migrations/000001_init_schema.up.sql)

- **What it does**: Creates `disaster_locations`, `site_assessments`, `photos`, each with a client-generated `UUID PRIMARY KEY`, `created_at`, `updated_at`, and `created_by` (the Cognito `sub`). Also enables `pgcrypto`.
- **Control flow**:
  1. `make migrate` runs `migrate ... -path migrations up`.
  2. golang-migrate applies files in number order; the `.down.sql` drops the three tables.
  3. [sqlc/sqlc.yaml](../../../sqlc/sqlc.yaml) reads this folder as its `schema`, so the Go types come from these tables.
- **Data flow**: DDL → tables. Foreign keys: `site_assessments.location_id` → locations; `photos.site_assessment_id` → site assessments. `photos.condition` has a `CHECK` limited to `good`, `moderate`, `bad`.
- **How it is tested**: indirectly by [integration_test.go](../../../internal/sync/integration_test.go) (`-tags=integration`), which needs `make compose-up` and `make migrate` first.
- **Read next**: [sqlc/queries.sql](../../../sqlc/queries.sql).

## [sqlc/queries.sql](../../../sqlc/queries.sql)

- **What it does**: The source of truth for every SQL statement. Phase 2's key ones: `UpsertLocation`, `UpsertSiteAssessment`, `UpsertPhoto`, `GetLocation`, `GetSiteAssessment`, `GetPhoto`, `ListSiteAssessmentsByLocation`, `ListPhotosBySite`, `CountPhotosBySite`. (The file also holds later-phase queries.)
- **Control flow**:
  1. `make sqlc-generate` runs `sqlc generate` in `sqlc/`.
  2. It writes `internal/db/queries.sql.go` and `querier.go` (the `Querier` interface). Do not edit those.
  3. `UpsertLocation` is `INSERT ... ON CONFLICT (id) DO UPDATE ... WHERE EXCLUDED.updated_at > disaster_locations.updated_at RETURNING *`. When the `WHERE` is false, no row is returned and pgx gives `pgx.ErrNoRows`.
  4. `UpsertPhoto` selects its values only `WHERE EXISTS (photo with this id) OR (count of photos for the site) < 10`. A new photo past the cap yields no row, so the insert never runs.
- **Data flow**: parameters (including `created_by` = actor) → one statement → row returned (applied) or no row (blocked). On conflict the update does not touch `created_by` or `created_at`.
- **How it is tested**: unit tests mock these calls; the real SQL runs in `TestIntegration_FullSyncFlow` (create, stale resend, fill to 10, 11th rejected, count stays 10).
- **Read next**: [internal/sync/sync.go](../../../internal/sync/sync.go).

## [internal/sync/sync.go](../../../internal/sync/sync.go)

- **What it does**: `Service.Sync` applies a batch of locations, site assessments, and photos and labels each with an `Outcome`.
- **Control flow**:
  1. Caller: `TxRunner.Sync` (see next section) calls `NewService(q).Sync(ctx, actor, req)`.
  2. `Sync` loops locations, then site assessments, then photos, calling `applyLocation`, `applySiteAssessment`, `applyPhoto`.
  3. Each `apply*` calls its `q.Upsert*`. No error means `OutcomeApplied`.
  4. If `errors.Is(err, pgx.ErrNoRows)` the write was blocked. It calls `q.Get*` and `classifyBlocked(incoming, stored, contentEqual)`.
  5. `classifyBlocked`: newer incoming returns `applied` (a rare race); equal timestamp with equal content returns `applied` (idempotent replay); equal timestamp with different content returns `conflict`; older returns `ignored_stale`.
  6. For photos, if `GetPhoto` itself returns `pgx.ErrNoRows`, the photo never existed, so it was blocked by the cap: `OutcomeLimitExceeded`.
  7. Any other error aborts the batch and is returned wrapped (infrastructure failure, not an outcome).
- **Data flow**: `Request` JSON → `db.Upsert*Params` (times via `toTimestamptz`) → `Response` with one `RecordResult{ID, Outcome}` per input, in order.
- **How it is tested**: unit, gomock `MockQuerier` in [sync_test.go](../../../internal/sync/sync_test.go): `TestSync_Location_Applied`, `TestSync_Location_IgnoredStale`, `TestSync_Location_Conflict`, `TestSync_Location_IdempotentReplay`, `TestSync_Photo_Applied`, `TestSync_Photo_LimitExceeded`, `TestSync_Photo_IgnoredStale`, `TestSync_InfraErrorAborts`. Integration (`-tags=integration`): `TestIntegration_FullSyncFlow` in [integration_test.go](../../../internal/sync/integration_test.go). The unit tests prove the classification logic; the integration test proves the SQL really blocks stale writes and enforces the cap.
- **Read next**: [internal/sync/transactional.go](../../../internal/sync/transactional.go).

## [internal/sync/transactional.go](../../../internal/sync/transactional.go)

- **What it does**: `TxRunner` runs a whole batch in one transaction and writes a `domain_events` row per applied record (the outbox write). It is the `Syncer` the HTTP handlers use.
- **Control flow**:
  1. `NewRouter` calls `sync.NewTxRunner(beginner)`; the `*pgxpool.Pool` satisfies `Beginner`.
  2. `TxRunner.Sync` calls `Begin`, then `defer tx.Rollback(ctx)` (a no-op after commit).
  3. `db.New(tx)` gives a `Queries` bound to the transaction; `NewService(q).Sync(...)` runs.
  4. For each result that is `OutcomeApplied`, `events.Publish(ctx, q, events.Event{...})` inserts a row in the same transaction. Ignored, conflict, and limit outcomes publish nothing.
  5. `tx.Commit`.
- **Data flow**: `Request` → upserts + `domain_events` rows → one commit → `Response`. Any infrastructure error rolls back the whole batch.
- **How it is tested**: integration only, `TestIntegration_TxRunnerEventsOnlyOnApplied` in [integration_test.go](../../../internal/sync/integration_test.go) (event row exists after an applied write, not added after a stale one).
- **Read next**: [internal/httpapi/sync.go](../../../internal/httpapi/sync.go).

## [internal/httpapi/sync.go](../../../internal/httpapi/sync.go)

- **What it does**: `syncHandler` is the `POST /api/sync` HTTP handler. It decodes JSON, calls the `Syncer`, and encodes the response.
- **Control flow**:
  1. Route: `r.Post("/api/sync", syncHandler(syncSvc))` inside the verified group in [router.go](../../../internal/httpapi/router.go).
  2. Decode body into `sync.Request`; bad JSON gives `400 invalid request body`.
  3. `auth.FromContext` supplies `claims.Sub`; `svc.Sync(ctx, claims.Sub, req)` runs.
  4. Error gives `500 sync failed`; otherwise JSON `Response`.
  5. This closes the MILESTONES Phase 1 item "`created_by` stamped from JWT `sub`": `claims.Sub` becomes `actor`, which becomes `created_by`.
- **Also read**: [locations.go](../../../internal/httpapi/locations.go) (`GET /api/locations/{id}`) and [sites.go](../../../internal/httpapi/sites.go) (`GET /api/sites/{id}`). Both parse the id with `uuid.Parse` (400), call `q.Get*` (404 on `pgx.ErrNoRows`), list children, and return JSON; other errors give 500. They do not compare `created_by` to the caller.
- **Data flow**: JSON → `sync.Request` → `Response` JSON. Go nil slices encode as `null`, which is why the web client uses `?? []`.
- **How it is tested**: untested. No `_test.go` in `internal/httpapi`; the 400/404/500 mapping was checked manually per MILESTONES.
- **Read next**: [web/src/db/schema.ts](../../../web/src/db/schema.ts).

## [web/src/db/schema.ts](../../../web/src/db/schema.ts)

- **What it does**: The Dexie database `fieldsync`, its record types (shaped like the server's JSON), and the `outbox` table.
- **Control flow**:
  1. `export const db = new FieldSyncDB()` is imported by capture, sync, and UI code.
  2. The constructor declares `this.version(1).stores({...})`: `locations`, `siteAssessments`, `photos`, `photoBlobs`, `outbox` (`++id` auto-increment).
- **Data flow**: records are plain objects with ISO-string timestamps; `OutboxEntry` stores only `entityType`, `entityId`, `queuedAt`, not a copy of the record.
- **How it is tested**: untested.
- **Read next**: [web/src/capture/capture.ts](../../../web/src/capture/capture.ts).

## [web/src/capture/capture.ts](../../../web/src/capture/capture.ts)

- **What it does**: Offline writes: `captureLocation`, `captureSiteAssessment`, `capturePhoto`. No network, no auth.
- **Control flow**:
  1. `CapturePanel` calls `captureLocation({name, latitude, longitude})`.
  2. It builds a record with `crypto.randomUUID()` and `createdAt = updatedAt = now`.
  3. `db.transaction('rw', db.locations, db.outbox, ...)` adds the record and an `outbox` row together, so neither lands alone.
- **Data flow**: form input → Dexie row + outbox row → later read by `drainRecords` in [syncEngine.ts](../../../web/src/sync/syncEngine.ts).
- **How it is tested**: untested. Manual run only (MILESTONES).
- **Read next**: [web/src/sync/syncEngine.ts](../../../web/src/sync/syncEngine.ts).

## [web/src/sync/syncEngine.ts](../../../web/src/sync/syncEngine.ts)

- **What it does**: Sends outbox entries to the server and removes the ones the server answered.
- **Control flow**:
  1. `CapturePanel.handleSyncNow` calls `drainOutbox()`.
  2. `drainRecords` loads outbox entries of type `location` or `siteAssessment`, resolves them from Dexie (`resolveLocations`, `resolveSiteAssessments`), gets the token (`getIdToken` via `fetchAuthSession`), and `fetch`es `POST /api/sync` with `Authorization: Bearer`.
  3. Non-2xx throws and leaves the outbox untouched. On 2xx it does `db.outbox.bulkDelete(...)` and `summarizeRecords`.
  4. `drainPhotos` handles photo entries (Phase 3).
  5. `startAutoSync` (2 s initial backoff, doubled on failure, 60 s cap, reset on the `online` event) is defined but not called anywhere.
- **Data flow**: outbox ids → Dexie records → `{locations, siteAssessments}` JSON → outcomes → outbox rows deleted → `DrainSummary` counts.
- **How it is tested**: untested in code (no JS test runner). MILESTONES records a manual Playwright run: capture offline, sync refused before login, then login → both records applied, outbox 0.
- **Read next**: [Phase 3 FILES](../phase-3-photos-s3/FILES.md).
