# Phase 2 — Sync API + DB: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What does "idempotent" mean, and why does sync need it?**
Doing the same thing twice leaves the same result as once. A phone can send a batch, lose the reply, and send it again. Because every record has a client-made UUID and the server does an upsert on that id, the replay changes nothing instead of creating duplicates. Source: `id UUID PRIMARY KEY` in [000001_init_schema.up.sql](../../../migrations/000001_init_schema.up.sql), `UpsertLocation` in [queries.sql](../../../sqlc/queries.sql).

**Q2. What is last-write-wins here?**
When the same id arrives twice, the version with the later `updated_at` wins. The SQL says `WHERE EXCLUDED.updated_at > disaster_locations.updated_at`, so an older or equal timestamp does not overwrite. Source: [sqlc/queries.sql](../../../sqlc/queries.sql).

**Q3. What is the outbox on the phone, and why a separate table?**
It is a Dexie table (`outbox`) listing records still to send: `entityType`, `entityId`, `queuedAt`. It stores ids only, so the record stays in its own table and the outbox stays small. When the server answers, the row is deleted. Source: [schema.ts](../../../web/src/db/schema.ts), `drainRecords` in [syncEngine.ts](../../../web/src/sync/syncEngine.ts).

## L2 Junior

**Q4. Trace `POST /api/sync` from the router to SQL.**
1. [router.go](../../../internal/httpapi/router.go): `verifier.Middleware` verifies the token, then `syncHandler(syncSvc)` runs.
2. [httpapi/sync.go](../../../internal/httpapi/sync.go): decode JSON into `sync.Request` (400 on bad JSON), get `claims.Sub`.
3. [transactional.go](../../../internal/sync/transactional.go): `TxRunner.Sync` begins a transaction and calls `NewService(q).Sync`.
4. [sync.go](../../../internal/sync/sync.go): for each record `apply*` calls `q.Upsert*` ([queries.sql](../../../sqlc/queries.sql)); `pgx.ErrNoRows` means blocked, so it reads the stored row and classifies.
5. Back in `TxRunner`, applied records get a `domain_events` row; `tx.Commit`; JSON response with one outcome per record.

Try it:
```
make compose-up
make migrate
make dev-server
```

**Q5. How does the server turn "blocked" into an outcome?**
`classifyBlocked(incoming, stored, contentEqual)`: incoming older gives `ignored_stale`; equal time and same content gives `applied` (a replay); equal time and different content gives `conflict`. For photos, if the photo id does not exist at all, the block was the 10-photo cap: `limit_exceeded`. Source: [sync.go](../../../internal/sync/sync.go).

**Q6. How does the phone save a record offline?**
`captureLocation` makes a UUID and timestamps, then runs one Dexie transaction over `db.locations` and `db.outbox`, adding the record and its outbox row together. No `fetch`, no token. Source: [capture.ts](../../../web/src/capture/capture.ts).

## L3 Mid

**Q7. Why one atomic upsert instead of lock-then-check in Go?**
"Read the row, compare timestamps, then write" has a gap between read and write where another request can change the row. Putting the compare inside `ON CONFLICT ... DO UPDATE ... WHERE` makes the decision inside one statement, so same-id writes cannot race. It also costs one round trip per record, not two or three. MILESTONES says this was checked in `psql` before any Go was written. Source: [queries.sql](../../../sqlc/queries.sql), package comment in [sync.go](../../../internal/sync/sync.go).

> Note: MILESTONES calls the photo cap "race-free". I am not certain that holds for the cap (see Q11). It holds for same-id writes.

**Q8. Why is an equal timestamp with identical content `applied`, not `conflict`?**
A retry after a lost reply replays the exact same record. Calling that a conflict would raise false alarms on every retry. MILESTONES says this was a gap caught before writing tests. `TestSync_Location_IdempotentReplay` proves the replay path; `TestSync_Location_Conflict` proves that equal timestamp with different content still returns `conflict`. Source: `classifyBlocked` in [sync.go](../../../internal/sync/sync.go).

**Q9. What does each test layer prove?**
- Unit ([sync_test.go](../../../internal/sync/sync_test.go), gomock `MockQuerier`, 8 tests): the Go branching, given what the query returns. They cannot prove the SQL does what the comments claim, because the mock simply returns `pgx.ErrNoRows` on demand.
- Integration ([integration_test.go](../../../internal/sync/integration_test.go), `-tags=integration`, real Postgres): `TestIntegration_FullSyncFlow` runs create, stale resend, fill to 10, 11th rejected, count stays 10. `TestIntegration_TxRunnerEventsOnlyOnApplied` proves events are written only for applied records.
- Not covered: the HTTP handlers, the web client, any concurrent execution. The E2E run in MILESTONES is manual; no Playwright files are committed.

Try it:
```
go test ./internal/sync/...
go test -tags=integration ./internal/sync/...
```
(the second needs `make compose-up` and `make migrate`)

**Q10. Why is rolling back the whole batch on an infrastructure error safe?**
Every upsert is idempotent, so the client re-sends the identical batch and gets the same final state. `TxRunner` needs no record of what half-applied. A business rejection is never an error, only an `Outcome`, so it does not roll back the rest. Source: doc comment on `TxRunner` in [transactional.go](../../../internal/sync/transactional.go).

## L4 Senior

**Q11. Is the 10-photo cap safe under concurrency?**
Not proven. `UpsertPhoto` counts photos in a sub-select. Postgres runs statements at READ COMMITTED by default. I believe two concurrent transactions, each inserting a different new photo into a site that has 9, would both see 9 and both insert, giving 11. Same-id races are fine because `ON CONFLICT` serializes on the row. I have not reproduced this; verify with two `psql` sessions, or add a concurrent integration test. The existing integration test inserts sequentially, so it cannot catch it. Source: [queries.sql](../../../sqlc/queries.sql), [integration_test.go](../../../internal/sync/integration_test.go).

**Q12. What is the cost of trusting client clocks for LWW?**
`updated_at` is supplied by the phone (`new Date().toISOString()` in [capture.ts](../../../web/src/capture/capture.ts)) and compared as-is. A phone with a fast clock wins every clash, and a wrong clock can silently discard a newer edit as `ignored_stale`. The loser gets an outcome but no merge. Fixes (server timestamps, version counters, per-field merge) are not implemented. Also `created_at` comes from the client.

**Q13. What happens if one record in the batch is permanently bad?**
Business conflicts are outcomes, but database errors are not. A site assessment pointing at a missing location, or a photo with a `condition` outside the CHECK, makes the upsert return a non-`ErrNoRows` error. `Sync` returns it, `TxRunner` rolls back, `syncHandler` answers 500. The client throws before `bulkDelete`, so the same batch is retried next time and fails the same way, which blocks every other record in the outbox. Not solved: there is no per-record isolation, no 4xx mapping for constraint errors, and no dead-letter. Source: [sync.go](../../../internal/sync/sync.go), [httpapi/sync.go](../../../internal/httpapi/sync.go), `drainRecords` in [syncEngine.ts](../../../web/src/sync/syncEngine.ts).

**Q14. What breaks at 100x, and what is simply not there?**
- Each record is its own statement (two per record when blocked: the upsert, then a `Get*`), run one after another in one transaction. A big batch holds a transaction and a pool connection for its whole length. There is no batch-size limit, and `syncHandler` decodes the body with no size cap.
- The client sends every pending location and site in one request, with no chunking.
- No input validation beyond DB constraints (names, lat/long ranges, nil UUIDs).
- No ownership check: `GET /api/locations/{id}` and `GET /api/sites/{id}` return any record to any signed-in user.
- Auto-retry is not running: `startAutoSync` has no caller (see the note in [FILES.md](FILES.md)).
- I have no load numbers; MILESTONES gives none for this phase. There is a `scripts/loadtest/main.go` in the repo; I did not read it for this phase.
