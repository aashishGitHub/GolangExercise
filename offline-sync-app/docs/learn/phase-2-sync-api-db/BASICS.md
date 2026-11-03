# Phase 2 — Sync API + DB: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 2 is the heart of the app. The phone saves data locally, then sends it to the server in batches. The server must accept it safely even if the same batch arrives twice, or two phones edit the same record.

1. **Offline-first** — The app writes to the phone first and talks to the server later. The user never waits for a network. Think of writing in a notebook and typing it up when you get home. In this project: [web/src/capture/capture.ts](../../../web/src/capture/capture.ts).

2. **IndexedDB and Dexie** — IndexedDB is a database built into every browser. Dexie is a small library that makes it easy to use. Think of a locker inside the browser that survives page reloads. In this project: [web/src/db/schema.ts](../../../web/src/db/schema.ts) (`class FieldSyncDB extends Dexie`).

3. **Outbox (local Dexie outbox)** — The outbox is a table of "things I still need to send". Each row names a record (type and id). When the server confirms, the row is removed. Think of a tray of letters waiting for the mail truck. In this project: the `outbox` table in [schema.ts](../../../web/src/db/schema.ts); rows added in [capture.ts](../../../web/src/capture/capture.ts).

4. **Drain** — To drain the outbox means to send everything in it and clear what was sent. Think of emptying the tray into the mail truck. In this project: `drainOutbox` in [web/src/sync/syncEngine.ts](../../../web/src/sync/syncEngine.ts).

5. **Client-generated ID (UUID/GUID)** — A UUID is a long random ID, like `3f2a…`. The phone makes it before the server ever sees the record, so no server round trip is needed. Collisions are practically impossible. Think of a bar code printed on the parcel before it ships. In this project: `crypto.randomUUID()` in [capture.ts](../../../web/src/capture/capture.ts); `id UUID PRIMARY KEY` in [migrations/000001_init_schema.up.sql](../../../migrations/000001_init_schema.up.sql).

6. **Idempotent** — An action is idempotent if doing it twice gives the same result as doing it once. If the phone loses the reply and re-sends, nothing doubles. Think of pressing a lift button twice: the lift still comes once. In this project: the upsert plus the equal-timestamp, equal-content branch of `classifyBlocked` in [internal/sync/sync.go](../../../internal/sync/sync.go) (tested by `TestSync_Location_IdempotentReplay`).

7. **Upsert** — Upsert means "insert, or update if the row already exists". SQL spells it `INSERT ... ON CONFLICT (id) DO UPDATE`. Think of a form that says "add me, or fix my entry if I am already listed". In this project: `UpsertLocation` in [sqlc/queries.sql](../../../sqlc/queries.sql).

8. **Last-write-wins (LWW)** — When two versions of a record clash, the one with the newer `updated_at` wins. The older one is dropped. Think of two people editing a sticky note: the latest ink stays. In this project: `WHERE EXCLUDED.updated_at > disaster_locations.updated_at` in [queries.sql](../../../sqlc/queries.sql).

9. **Outcome** — For each record, the server answers one word: `applied`, `ignored_stale`, `conflict`, or `limit_exceeded`. The phone treats any of them as final. Think of a stamp on each letter: accepted, too old, clash, or full. In this project: `Outcome` constants in [sync.go](../../../internal/sync/sync.go).

10. **Stale** — A stale write is one whose `updated_at` is older than what the server already has. It is ignored. Think of a weather report from yesterday arriving after today's. In this project: `OutcomeIgnoredStale`, test `TestSync_Location_IgnoredStale`.

11. **Conflict** — A conflict is two edits with the same timestamp but different content. Time cannot pick a winner, so the server reports it. Think of two people writing different words at the exact same second. In this project: `OutcomeConflict`, test `TestSync_Location_Conflict`.

12. **Atomic** — Atomic means it happens all at once, or not at all, with nothing in between. One SQL statement is atomic. Think of a coin drop in a vending machine: it either goes in or it does not. In this project: the single-statement upserts in [queries.sql](../../../sqlc/queries.sql).

13. **Race condition** — A race is when two things run at the same time and the result depends on who is faster. "Read, then check, then write" in app code is open to races. Think of two people grabbing the last seat. In this project: the reason the code uses one atomic upsert instead of lock-then-check (see the doc comment at the top of [sync.go](../../../internal/sync/sync.go)).

14. **Primary key, foreign key, CHECK** — A primary key is the unique ID of a row. A foreign key says "this id must exist in that other table". A CHECK limits allowed values. Think of an ID card, a "must be a real customer" rule, and a "only these three choices" rule. In this project: `REFERENCES disaster_locations(id)` and `CHECK (condition IN ('good','moderate','bad'))` in [000001_init_schema.up.sql](../../../migrations/000001_init_schema.up.sql).

15. **Index** — An index is a lookup shortcut for a column, like the index at the back of a book. Without it the database reads every row. In this project: `site_assessments_location_id_idx` and `photos_site_assessment_id_idx` in [000001_init_schema.up.sql](../../../migrations/000001_init_schema.up.sql).

16. **Connection pool** — Opening a database connection is slow. A pool keeps several open and lends them out. Think of a taxi rank. In this project: `pgxpool.New` in [cmd/server/main.go](../../../cmd/server/main.go).

17. **Transaction** — A transaction groups several SQL steps so they all succeed or all roll back. Think of a bank transfer: debit and credit together, or neither. In this project: `TxRunner.Sync` in [internal/sync/transactional.go](../../../internal/sync/transactional.go).

18. **sqlc and the Querier interface** — sqlc turns each SQL query into a typed Go function. It also writes a `Querier` interface listing them all. Code depends on the interface, so tests can swap in a fake. In this project: [sqlc/queries.sql](../../../sqlc/queries.sql) → `internal/db/querier.go`.

19. **Mock (gomock)** — A mock is a fake that returns what you tell it and checks it was called as expected. It lets you test Go logic without a database. Think of a stunt double. In this project: `MockQuerier` in [internal/sync/mocks/querier_mock.go](../../../internal/sync/mocks/querier_mock.go), used by [sync_test.go](../../../internal/sync/sync_test.go).

20. **Integration test and build tag** — An integration test uses the real database. A build tag (`//go:build integration`) keeps it out of normal runs; you ask for it with `-tags=integration`. Think of a dress rehearsal with the real stage. In this project: [internal/sync/integration_test.go](../../../internal/sync/integration_test.go); run with `make test-integration`.

21. **Status codes** — HTTP replies carry a number. 400 means "your request is malformed", 404 means "not found", 500 means "the server failed". Think of a post office stamping why a parcel came back. In this project: `getLocationHandler` in [internal/httpapi/locations.go](../../../internal/httpapi/locations.go).

22. **camelCase JSON** — JSON field names like `updatedAt` (not `updated_at`) are what the browser expects. sqlc writes the Go struct tags in that style. In this project: `json_tags_case_style: "camel"` in [sqlc/sqlc.yaml](../../../sqlc/sqlc.yaml).

23. **Exponential backoff** — After a failure, wait a bit, then wait twice as long next time, up to a cap. It avoids hammering a server that is down. Think of calling a busy friend: wait 2 minutes, then 4, then 8. In this project: `startAutoSync` in [syncEngine.ts](../../../web/src/sync/syncEngine.ts) (2 s start, 60 s cap).

24. **≤10 photos per site** — A business rule: one site assessment may hold at most 10 photos. The database enforces it inside the upsert, not in a separate step. In this project: `UpsertPhoto` in [queries.sql](../../../sqlc/queries.sql).
