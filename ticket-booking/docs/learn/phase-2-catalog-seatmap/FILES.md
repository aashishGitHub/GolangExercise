# Phase 2 — Catalog + Seat Map Read Path: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Test picture for this phase: unit tests exist for `internal/seatmap` and `internal/catalog` only. No test covers the HTTP handlers (`internal/httpapi/catalog.go`), `cmd/event-publisher`, `scripts/seed-venue`, or the SQL. Those were verified by hand (psql, `curl`, `ab`), output in [MILESTONES.md](../../../MILESTONES.md).

> Note: `getAvailability` in the code now reads Redis first and falls back to the DB scan. MILESTONES Phase 2 describes the DB-scan-only version. The code shown below is the current one (Phase 7 upgrade, per the code comment).

## [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql)

- **What it does**: creates `event_seats`, the per-event, per-seat state table, plus its indexes. The venue tables (`venues`, `sections`, `seat_rows`, `seats`), `events`, `event_price_tiers`, and the trigram indexes are in [migrations/000001_venue_layout.up.sql](../../../migrations/000001_venue_layout.up.sql).
- **Control flow**:
  1. `make migrate` runs golang-migrate (`migrate ... up`) against `migrations/`.
  2. 000001 runs first, then 000002.
  3. `event_seats` has primary key `(event_id, seat_id)` and a unique index `event_seats_ordinal_uq` on `(event_id, seat_ordinal)`.
  4. `event_seats_cover_idx` is `(event_id, seat_ordinal) INCLUDE (status, sellable, price_cents)`.
  5. Two CHECK constraints: a BOOKED seat must have `booking_id`; a HELD or PENDING_PAYMENT seat must have `hold_id` and `hold_expires_at`.
- **Data flow**: SQL text → schema in Postgres. Later phases write `status`, `hold_id`, `fence_token`. Phase 2 only inserts (status 0) and reads.
- **How it is tested**: no schema test. Verified by `EXPLAIN ANALYZE` in MILESTONES (Index Only Scan on `event_seats_cover_idx`, Heap Fetches 0, after `ANALYZE`). The integration tests of later phases (build tag `integration`) run against the migrated schema.
- **Read next**: [sqlc/queries.sql](../../../sqlc/queries.sql).

## [sqlc/queries.sql](../../../sqlc/queries.sql)

- **What it does**: every SQL statement the Go code uses, annotated with `-- name: X :one|:many|:exec|:copyfrom`. [sqlc/sqlc.yaml](../../../sqlc/sqlc.yaml) points schema at `../migrations` and output at `../internal/db` with `emit_interface: true`.
- **Control flow**:
  1. `make sqlc-generate` runs `sqlc generate` inside `sqlc/`.
  2. It produces `internal/db/queries.sql.go`, `models.go`, `querier.go`, `copyfrom.go`.
  3. Phase 2 queries: `CreateVenue`, `CreateSection`, `CreateSeatRow`, `CreateSeat`, `GetVenue`, `ListVenueSeatsOrdered`, `CreateEvent`, `CreateEventPriceTier`, `ListEventPriceTiers`, `MinEventPriceCents`, `CountAvailableSeats`, `GetEvent`, `SetEventOnSale`, `ListEvents`, `BulkInsertEventSeats`, `ListEventSeatsForAvailability`, `ListClosedSectionsForEvent`.
- **Data flow**: SQL + migrations → generated typed Go methods on `db.Queries` and the `db.Querier` interface → callers (`catalog`, handlers, `event-publisher`).
- **How it is tested**: generated code is not tested directly. The `Querier` interface is what lets later phases mock the DB with gomock (see [internal/inventory/mocks/querier_mock.go](../../../internal/inventory/mocks/querier_mock.go)).
- **Read next**: [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go).

## [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go)

- **What it does**: the 2-bit-per-seat bitset codec.
- **Control flow**:
  1. `New(count)` allocates `ByteLen(count)` = `(count+3)/4` zero bytes (all FREE).
  2. `Set(packed, ordinal, state)` clears 2 bits at byte `ordinal>>2`, shift `(ordinal&3)*2`, then ORs the new state.
  3. `Get` reads the same bits.
  4. `StateFromDB(dbStatus, sellable)` maps DB values to wire `State`: `!sellable` gives Unavailable; 0 gives Free; 1 and 3 give Held; 2 gives Sold; anything else gives Unavailable.
- **Data flow**: DB row (`status`, `sellable`) → `StateFromDB` → `Set` into a `[]byte` → written as the response body. No I/O in this package.
- **How it is tested**: unit, no build tag, [internal/seatmap/seatmap_test.go](../../../internal/seatmap/seatmap_test.go): `TestByteLen`, `TestSetGet_AllOrdinalsInByte`, `TestSetGet_RoundTripAllOrdinals`, `TestSet_DoesNotDisturbNeighbors`, `TestNew_AllFree`, `TestStateFromDB`. They prove the packing is correct and neighbours are not corrupted. Because both `Set` and `Get` live in this file, a shared bug in the bit math could hide from round-trip tests. The web port ([web/src/seatmap/model/bitset.ts](../../../web/src/seatmap/model/bitset.ts), Phase 4) has its own mirror tests, but neither side checks a shared golden byte sequence.
- **Read next**: [internal/catalog/layout.go](../../../internal/catalog/layout.go).

## [internal/catalog/layout.go](../../../internal/catalog/layout.go)

- **What it does**: builds `layout.json` metadata and the `seats.bin` bytes, and the per-ordinal seat/tier/closed arrays, all in one pass.
- **Control flow**:
  1. `cmd/event-publisher` calls `BuildLayout(venueID, layoutVersion, rows)` with rows from `ListVenueSeatsOrdered`.
  2. It loops once. A change in `SectionID` starts a new `SectionMeta`, a change in `RowID` starts a new `RowMeta`. Loop index `ord` is the seat ordinal.
  3. It records x, y, `seatLabelToNumber(...)`, tier index (first-seen order), seat id, tier name, closed flag.
  4. A second loop computes a bounding-rectangle polygon per section.
  5. `EncodeSeatsBin(x, y, seatNumber, tierIdx, layoutVersion)` writes a 12-byte header (`SEAT`, format version 1, layout version, seat count) then four columns.
  6. `Meta.Tiers[idx]` records tier names by `tierIdx`, so the client maps tier by name, not array position.
- **Data flow**: `[]db.ListVenueSeatsOrderedRow` → `Built{Meta, SeatsBin, SeatID, Tier, SectionClosed}`. Seat ordinal equals slice index.
- **How it is tested**: unit, [internal/catalog/layout_test.go](../../../internal/catalog/layout_test.go): `TestEncodeSeatsBin_HeaderAndRoundTrip` (magic, version, length, a negative int16 round-trip), `TestSeatLabelToNumber`, `TestBuildLayout_OrdinalsAndCounts` (8-seat fixture: counts, contiguous section ranges, ordinal equals walk position, closed flags). The decode is checked inside the test, not with an independent decoder. The web test `seatIndex.test.ts` (Phase 4) re-implements the encoder in TypeScript, it does not read bytes written by Go. Only the Playwright E2E fetches the real `seats.bin` from MinIO.
- **Read next**: [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go).

> Note: the `Tiers` field and its reason were added in Phase 4 (MILESTONES bug 2). It is in the code now.

## [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go)

- **What it does**: a command-line job that turns a venue into a sellable event: creates the event, price tiers, `event_seats` rows, the two layout files, and flips status to ON_SALE.
- **Control flow**:
  1. Parse flags (`-venue-id` required, `-artist`, `-title`, `-days-until`, `-floor-price-cents`, `-lower-price-cents`, `-upper-price-cents`).
  2. `config.Load()`, `pgxpool.New`, `storage.New`.
  3. `q.GetVenue`, then `q.CreateEvent` (a new event every run).
  4. `q.ListVenueSeatsOrdered`, then `catalog.BuildLayout`.
  5. `q.CreateEventPriceTier` once per distinct tier.
  6. `q.BulkInsertEventSeats` (pgx `CopyFrom`) with ordinal = loop index.
  7. `pool.Exec(ctx, "ANALYZE event_seats")`.
  8. `s3.PutObject` for `layout.json` and `seats.bin` at `venues/{venueID}/{layoutVersion}/`, cache header `public, max-age=31536000, immutable`.
  9. `q.SetEventOnSale`.
- **Data flow**: venue rows → one ordered walk → (a) `event_seats` rows in Postgres, (b) `layout.json` and `seats.bin` in the layouts bucket, (c) `events.status = 'ON_SALE'`.
- **How it is tested**: untested (no test file). Manual run in MILESTONES: 30,000 rows inserted, 24,830-byte `layout.json`, 210,012-byte `seats.bin`, wall time 1.6s on that run.
- **Read next**: [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go).

> Note: the package comment says "Idempotent on event_id: re-running ... is a no-op". The code does not do that. Step 3 always creates a new event, and the steps are not wrapped in one transaction. A second run publishes a second event, and a crash after step 6 leaves a DRAFT event with seats. MILESTONES itself says re-run safety was "not yet re-run-tested". The code wins.

## [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go)

- **What it does**: the open (no token) read routes: list events, one event, availability, pricing, venue layout redirect.
- **Control flow** (availability, the main read path):
  1. chi routes `GET /api/v1/events/{eventID}/availability` to `catalogAPI.getAvailability`.
  2. Parse `eventID`.
  3. If a projector is wired, `tryRedisAvailability` calls `proj.EnsureBitset` and `proj.SeatCount`. Any error means fall through.
  4. Fallback: `q.ListEventSeatsForAvailability`. Zero rows gives 404. Else `seatmap.New(len(rows))` and one `seatmap.Set(..., StateFromDB(...))` per row.
  5. `writeAvailability` computes `ETag` = quoted SHA-256 of the packed bytes. If `If-None-Match` equals it, reply 304. Else 200 with `Content-Type: application/octet-stream`, `X-Seat-Count`, `ETag`, and `X-Seatmap-Version` (Redis path only).
  6. Other handlers: `listEvents` (keyset cursor, `limit` 1 to 100, default 20, calls `MinEventPriceCents` and `CountAvailableSeats` once per event), `getEvent` (builds `layoutUrl` via `publicURL`), `getPricing` (`Cache-Control: max-age=60, stale-while-revalidate=300`), `getVenueLayout` (302 redirect to the layout object).
- **Data flow**: `event_seats` rows (or Redis bitset) → packed 2-bit bytes → binary HTTP body. Nothing is written.
- **How it is tested**: no Go test for these handlers. Verified by `curl` and `ab` in MILESTONES (7,500 bytes for 30,000 seats, 304 on matching ETag, pg_trgm hit and miss).
- **Read next**: [scripts/seed-venue/main.go](../../../scripts/seed-venue/main.go).

## [scripts/seed-venue/main.go](../../../scripts/seed-venue/main.go)

- **What it does**: inserts a synthetic venue (default 10 sections x 30 rows x 100 seats) for backend and frontend tests.
- **Control flow**:
  1. Flags: `-name`, `-city`, `-sections`, `-rows`, `-seats-per-row`, `-dsn` (default from `DATABASE_URL`).
  2. `q.CreateVenue`, then nested loops call `q.CreateSection` (via `tierFor`), `q.CreateSeatRow`, `q.CreateSeat`.
  3. `tierFor`: first fifth of sections are `floor`, the middle are `lower`, the rest `upper`, and the last section is `upper` and `closed`.
- **Data flow**: flags → rows in `venues`, `sections`, `seat_rows`, `seats` (one insert per seat, no CopyFrom).
- **How it is tested**: untested. Used as the fixture for integration tests and E2E.
- **Read next**: [infra/terraform/modules/database/main.tf](../../../infra/terraform/modules/database/main.tf), then [Phase 3 FILES](../phase-3-hold-cas/FILES.md).

## [infra/terraform/modules/database/main.tf](../../../infra/terraform/modules/database/main.tf)

- **What it does**: HCL for an Aurora Postgres cluster. The `network` and `secrets` modules sit beside it. Written only, never applied from this project.
- **Control flow**: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf) calls `module "network"`, `module "secrets"`, then `module "database"`, passing their outputs (VPC, subnets, security groups, secret ARN). Resources include `aws_rds_cluster`, writer and reader `aws_rds_cluster_instance`, `aws_db_proxy`.
- **Data flow**: module variables → resource definitions → `terraform validate` result. No state.
- **How it is tested**: only `terraform validate` (CI job `terraform-validate`, `make tf-validate`). Nothing proves it works on real AWS.
- **Read next**: [Phase 3 FILES](../phase-3-hold-cas/FILES.md).
