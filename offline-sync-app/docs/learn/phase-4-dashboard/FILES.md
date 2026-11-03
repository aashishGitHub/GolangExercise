# Phase 4 — Dashboard: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Testing note for the whole phase: there are no automated tests for any file below. `web/package.json` has no test script and no `*.test.*` file exists under `web/`. The Go read handlers have no `_test.go` in `internal/httpapi/`. MILESTONES.md records a manual browser E2E (1/1/1 breakdown before sync, same after sync + pull, no console errors).

> Note: MILESTONES says "pull-on-login". In the code, `pullLocation` is called from one place only: the button handler `handlePull` in `LocationDashboard.tsx`. Nothing calls it on login. The code wins: pull is manual.

## [web/src/dashboard/LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx)

- **What it does**: Shows a stacked bar and legend of Good/Moderate/Bad photo counts for one location, plus a "Pull latest from server" button.
- **Control flow**:
  1. `App` renders `<LocationDashboard locationId=... />` once `CapturePanel` reports a location id.
  2. `useLiveQuery` runs its async function: `db.siteAssessments.where('locationId').equals(locationId)`, then `db.photos.where('siteAssessmentId').anyOf(siteIds)`.
  3. The result is three `filter(...).length` counts. Dexie re-runs the function when those tables change.
  4. Render: if `total === 0` show "no photos synced for this location yet", else the bar and the `<ul>` legend.
  5. Button click calls `handlePull`, which sets `pulling`, awaits `pullLocation(locationId)`, and stores any error in `pullError`.
- **Data flow**: Dexie rows (sites, photos) in → counts by `condition` → inline-style `width` percentages on `div`s. Nothing is persisted here. The pull writes to Dexie, and the live query re-draws from that.
- **How it is tested**: Not tested automatically. Verified by hand in a browser (MILESTONES Phase 4).
- **Read next**: [pull.ts](../../../web/src/sync/pull.ts)

## [web/src/sync/pull.ts](../../../web/src/sync/pull.ts)

- **What it does**: `pullLocation` copies one location, its sites and their photos from the server into Dexie.
- **Control flow**:
  1. Called by `handlePull` in `LocationDashboard.tsx`.
  2. `fetchAuthSession()` (from `aws-amplify/auth`) gets the ID token. No token throws `not authenticated`.
  3. `GET /api/locations/{id}` with `Authorization: Bearer ...`. Non-2xx throws `pull location failed`.
  4. `db.locations.put(...)`.
  5. For each entry in `location.siteAssessments ?? []`: `GET /api/sites/{id}`, then `db.siteAssessments.put(...)`, then `db.photos.bulkPut(...)`.
- **Data flow**: Server JSON (`ServerLocation`, `ServerSite`, `ServerPhoto`) → field-by-field copy into the Dexie record shapes → upsert by `id`. It never deletes rows and never touches `outbox` or `photoBlobs`. Photo image bytes are not pulled, only metadata including `s3Key`.
- **How it is tested**: Not tested automatically. Manual E2E only.
- **Read next**: [locations.go](../../../internal/httpapi/locations.go)

## [web/src/db/schema.ts](../../../web/src/db/schema.ts)

- **What it does**: Defines the Dexie database `fieldsync`: record types and the five tables `locations`, `siteAssessments`, `photos`, `photoBlobs`, `outbox`.
- **Control flow**:
  1. Imported as `db` by `LocationDashboard.tsx`, `pull.ts` and `syncEngine.ts`.
  2. `FieldSyncDB` constructor calls `this.version(1).stores({...})` to declare indexes.
  3. The two indexes the dashboard relies on are `siteAssessments: 'id, locationId, updatedAt'` and `photos: 'id, siteAssessmentId, updatedAt'`.
- **Data flow**: Types only describe shapes. The `Condition` type (`'good' | 'moderate' | 'bad'`) is what the dashboard counts by. Comments in the file say the record shapes mirror the server's sync wire shape.
- **How it is tested**: Not tested. It is exercised only by running the app.
- **Read next**: [pull.ts](../../../web/src/sync/pull.ts) if you came here first, otherwise [sites.go](../../../internal/httpapi/sites.go)

## [web/src/App.tsx](../../../web/src/App.tsx)

- **What it does**: Holds `locationId` state and mounts the dashboard only after a location exists.
- **Control flow**:
  1. `CapturePanel` gets `onLocationCaptured={setLocationId}`.
  2. After `captureLocation(...)`, `CapturePanel.handleCaptureLocation` calls `onLocationCaptured?.(record.id)`.
  3. `{locationId && <LocationDashboard locationId={locationId} />}` renders the dashboard.
- **Data flow**: A location id string moves up from `CapturePanel` to `App`, then down into the dashboard as a prop.
- **How it is tested**: Not tested.
- **Read next**: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx)

> Note: the dashboard only appears for a location captured in this page session. A reload clears `locationId` state, so the dashboard is gone until you capture again. I did not find code that restores it from Dexie.

## [internal/httpapi/locations.go](../../../internal/httpapi/locations.go)

- **What it does**: `getLocationHandler` serves `GET /api/locations/{id}`: the location plus its site assessments.
- **Control flow**:
  1. `NewRouter` in [router.go](../../../internal/httpapi/router.go) mounts it inside the group that uses `verifier.Middleware`.
  2. Parse `{id}` with `uuid.Parse`; bad id returns 400.
  3. `q.GetLocation`; `pgx.ErrNoRows` returns 404, other errors 500.
  4. `q.ListSiteAssessmentsByLocation`.
  5. Encode `locationResponse` (embedded `db.DisasterLocation` plus `siteAssessments`).
- **Data flow**: Path id → two sqlc queries (`SELECT * FROM disaster_locations WHERE id = $1`, `SELECT * FROM site_assessments WHERE location_id = $1 ORDER BY created_at`) → JSON using the `json:` tags in `internal/db/models.go`.
- **How it is tested**: No test for this handler. `internal/sync/sync_test.go` uses a mock of `GetLocation`, but that is the sync service's stale/conflict check, not this handler.
- **Read next**: [sites.go](../../../internal/httpapi/sites.go)

## [internal/httpapi/sites.go](../../../internal/httpapi/sites.go)

- **What it does**: `getSiteHandler` serves `GET /api/sites/{id}`: the site plus its photos.
- **Control flow**: Same shape as `getLocationHandler`: `uuid.Parse` → `q.GetSiteAssessment` → `q.ListPhotosBySite` → encode `siteResponse`.
- **Data flow**: Path id → `SELECT * FROM site_assessments WHERE id = $1` and `SELECT * FROM photos WHERE site_assessment_id = $1 ORDER BY created_at` → JSON with `photos` array.
- **How it is tested**: No test for this handler.
- **Read next**: [../phase-5-iac-local-deploy/BASICS.md](../phase-5-iac-local-deploy/BASICS.md)

> Note: neither handler compares the caller's `sub` with `created_by`. Any signed-in user who knows a location id can read it. This matches the phase scope (read path), but it is not an access-control design.
