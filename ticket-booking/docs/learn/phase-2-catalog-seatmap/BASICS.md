# Phase 2 — Catalog + Seat Map Read Path: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Phase 2 puts events and 30,000 seats into Postgres, builds two static layout files, and lets a client read "which seats are free" cheaply. Nothing is booked yet. Background: [deep-dive.md](../../deep-dive.md) section 8 (database design, "blueprints vs tonight's guest list").

1. **Migration** — A migration is a numbered SQL file that changes the database shape, such as creating a table. Tools run them in order so every machine ends up identical. It is like numbered building-permit steps: foundation first, roof later.
   In this project: [migrations/000001_venue_layout.up.sql](../../../migrations/000001_venue_layout.up.sql)

2. **Venue vs event data** — A venue is the building: sections, rows, seats. It does not change per show. An event is one show at that venue, and each event needs its own per-seat state. It is like a theatre's floor plan (fixed) versus tonight's guest list (new each night).
   In this project: `venues`, `seats` versus `events`, `event_seats` in [migrations/000001_venue_layout.up.sql](../../../migrations/000001_venue_layout.up.sql) and [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql)

3. **Seat ordinal** — An ordinal is a seat's position number in one event: 0, 1, 2, up to N-1, with no gaps. It goes section by section, row by row. The browser and server use it as the seat's identity on the wire. It is like a theatre usher numbering every chair from the first to the last.
   In this project: `seat_ordinal` in [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql)

4. **Seat status vs sellable** — `status` is the seat's life state: 0 available, 1 held, 2 booked, 3 pending payment. `sellable` is a separate yes/no: false means the seat can never be sold, such as a closed section. It is like a chair that is empty (status) versus a chair roped off (not sellable).
   In this project: `StateFromDB` in [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go)

5. **Bit and bitset** — A bit is a single 0 or 1. A bitset packs many small values into bytes. Here each seat uses 2 bits, so one byte holds 4 seats. It is like fitting four small stickers on one postage stamp.
   In this project: `Set` and `Get` in [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go)

6. **Why 2 bits** — Two bits give four values: free (0), held (1), sold (2), unavailable (3). 30,000 seats then fit in 7,500 bytes, which is `ceil(30000/4)`. It is like a traffic light with four colours instead of two.
   In this project: `ByteLen` in [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go)

7. **sqlc** — sqlc reads your SQL queries and writes typed Go functions for them. You write SQL, you get Go code, and a typo fails at generation time. It is like a translator who checks grammar before the letter is sent.
   In this project: [sqlc/queries.sql](../../../sqlc/queries.sql) and [sqlc/sqlc.yaml](../../../sqlc/sqlc.yaml), output in `internal/db`

8. **Index** — An index is a sorted side-structure that lets the database find rows without reading the whole table. It is like a book's index: look up the word, jump to the page.
   In this project: `event_seats_ordinal_uq` in [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql)

9. **Covering index and Index Only Scan** — A covering index also stores extra columns (`INCLUDE (status, sellable, price_cents)`). The database can then answer from the index alone, which is called an Index Only Scan. It is like a recipe card that lists ingredients on the front so you never open the cookbook.
   In this project: `event_seats_cover_idx` in [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql)

10. **Query planner** — The planner is the part of Postgres that picks how to run a query (which index, which scan type). It chooses by guessing costs. It is like a GPS choosing a route from traffic estimates.
    In this project: the `EXPLAIN ANALYZE` evidence in [MILESTONES.md](../../../MILESTONES.md) Phase 2

11. **Planner statistics and ANALYZE** — Statistics are the planner's counts, such as how many rows a table has. `ANALYZE` refreshes them. After a bulk load of 30,000 rows the old count was stale (about 93), so the planner picked a slower plan. It is like a GPS with last year's traffic data.
    In this project: `ANALYZE event_seats` in [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go)

12. **Bulk insert (CopyFrom)** — `CopyFrom` is Postgres's fast way to load many rows in one stream, instead of one `INSERT` per row. It is like moving house with a truck, not a bag per trip.
    In this project: `BulkInsertEventSeats` in [sqlc/queries.sql](../../../sqlc/queries.sql)

13. **Static layout files** — The seat positions never change for a venue version, so they are saved once as `layout.json` (sections and rows) and `seats.bin` (a compact binary list of seat coordinates). They live in object storage, not in the API. It is like printing the floor plan once instead of redrawing it per visitor.
    In this project: `BuildLayout` in [internal/catalog/layout.go](../../../internal/catalog/layout.go)

14. **Object storage (S3 / MinIO)** — Object storage holds files under a bucket and a key, like `venues/1/1/layout.json`. S3 is the AWS service, MinIO is the free local imitation. It is like a left-luggage desk with ticket numbers.
    In this project: `PutObject` in [internal/storage/s3.go](../../../internal/storage/s3.go)

15. **Immutable, versioned path** — The layout version is part of the file path, and the file is served with `Cache-Control: public, max-age=31536000, immutable`. A changed layout gets a new path, so old caches can never be wrong. It is like printing a new edition number instead of editing an old book in people's homes.
    In this project: `prefix` in [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go)

16. **Binary format and little-endian** — `seats.bin` stores numbers as raw bytes. Little-endian means the smallest byte comes first. A fixed header (`SEAT`, version, seat count) lets the reader check what it got. It is like a letter that begins with a label saying what language it is written in.
    In this project: `EncodeSeatsBin` in [internal/catalog/layout.go](../../../internal/catalog/layout.go)

17. **ETag and 304** — An ETag is a fingerprint of a response. The browser sends it back as `If-None-Match`. If nothing changed, the server replies `304 Not Modified` with no body. It is like asking "same menu as yesterday?" and hearing "yes" instead of being handed the whole menu again.
    In this project: `writeAvailability` in [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go)

18. **Trigram search (pg_trgm)** — A trigram is a three-letter chunk of text. Postgres's `pg_trgm` extension indexes these so `ILIKE '%term%'` searches can use an index. It is like finding a book by any three letters you remember from the title.
    In this project: `events_title_trgm_idx` in [migrations/000001_venue_layout.up.sql](../../../migrations/000001_venue_layout.up.sql)

19. **Keyset pagination** — Instead of "skip 40 rows", you say "give me rows after id 40". It stays fast on long lists. The reply carries `nextCursor`. It is like a bookmark, not counting pages from the start every time.
    In this project: `ListEvents` in [sqlc/queries.sql](../../../sqlc/queries.sql)

20. **Fixture / seed script** — A fixture is fake but realistic data for tests and demos. `seed-venue` makes a synthetic venue of 10 sections x 30 rows x 100 seats. It is like a stage set: looks real, built for rehearsal.
    In this project: [scripts/seed-venue/main.go](../../../scripts/seed-venue/main.go)

21. **Latency percentiles (p50, p95)** — p50 is the time half of requests beat. p95 is the time 95 percent beat. Averages hide slow tails, percentiles show them. It is like judging a restaurant by how long the slowest table waits, not the average.
    In this project: the `ab` results and the caveat in [MILESTONES.md](../../../MILESTONES.md) Phase 2

22. **Connection pool** — A pool keeps a fixed set of open database connections that requests borrow. With too few, requests queue. MILESTONES suspects this for the slower p50 at 20 concurrent requests, but did not prove it. It is like a few taxis for many passengers.
    In this project: `pgxpool.New` in [internal/wiring/wiring.go](../../../internal/wiring/wiring.go)

23. **Infrastructure modules** — Terraform modules are reusable bundles of cloud resources. Phase 2 adds `network`, `secrets`, and `database` modules, only checked with `validate`. It is like flat-pack furniture instructions: written, not yet assembled.
    In this project: [infra/terraform/modules/database/main.tf](../../../infra/terraform/modules/database/main.tf)
