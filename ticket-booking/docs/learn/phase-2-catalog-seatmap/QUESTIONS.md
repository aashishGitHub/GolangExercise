# Phase 2 — Catalog + Seat Map Read Path: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

## L1 Beginner

**Q1. What is a seat ordinal and why not just use the seat's database ID?**
An ordinal is a dense position 0 to N-1 within one event, in section, row, seat order. It doubles as the index into the bitset and as the seat's identity on the wire. A database ID has gaps and no tie to layout order, so it cannot index a packed array. See `seat_ordinal` in [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql) and `BuildLayout` in [internal/catalog/layout.go](../../../internal/catalog/layout.go).

**Q2. Why does availability use 2 bits per seat?**
Four states fit in 2 bits: free, held, sold, unavailable. 30,000 seats then pack into 7,500 bytes (`ByteLen` = `(count+3)/4`). MILESTONES measured the response at exactly 7,500 bytes. See [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go).

**Q3. What is an ETag and what does `304 Not Modified` save?**
The server sends a fingerprint of the body. The client repeats it in `If-None-Match`. If it still matches, the server sends 304 and no body. In `writeAvailability` the ETag is a quoted SHA-256 of the packed bytes ([internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go)).

Try it:
```
curl -s -D - -o /dev/null localhost:8080/api/v1/events/1/availability
```

**Q4. What is the difference between `status` and `sellable`?**
`status` tracks the seat's lifecycle (0 available, 1 held, 2 booked, 3 pending payment). `sellable=false` marks a seat as structurally unsellable, such as a closed section. `StateFromDB` makes `sellable=false` win and returns Unavailable ([internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go)).

## L2 Junior

**Q5. Trace `GET /api/v1/events/1/availability` from router to bytes.**
`catalogAPI.getAvailability` parses the id. If `c.proj` is set it tries `tryRedisAvailability` (`EnsureBitset`, `SeatCount`). On any error or no projector it runs `ListEventSeatsForAvailability`, builds the bitset with `seatmap.New` plus `seatmap.Set(StateFromDB(...))` per row, then `writeAvailability` adds `ETag` and `X-Seat-Count` and writes the bytes. All in [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go).

**Q6. Trace what `cmd/event-publisher` does, in order.**
`GetVenue`, `CreateEvent`, `ListVenueSeatsOrdered`, `catalog.BuildLayout`, `CreateEventPriceTier` per tier, `BulkInsertEventSeats` (CopyFrom), `ANALYZE event_seats`, upload `layout.json` and `seats.bin`, `SetEventOnSale`. See [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go).

Try it:
```
go run ./scripts/seed-venue
```
```
go run ./cmd/event-publisher -venue-id=1
```

**Q7. Where does the 7-bytes-per-seat size of `seats.bin` come from?**
`EncodeSeatsBin` writes a 12-byte header, then `x` (int16), `y` (int16), `seatNumber` (uint16), and `tierIdx` (uint8) per seat: 2+2+2+1 = 7 bytes. For 30,000 seats that is 12 + 210,000 = 210,012 bytes, which matches the size in MILESTONES. See [internal/catalog/layout.go](../../../internal/catalog/layout.go).

**Q8. How does the client turn a seat's `tierIdx` into a price?**
Through `LayoutMeta.Tiers`, which lists tier names in the same first-seen order as `tierIdx`. The client then looks up the price by name in `GET .../pricing`. Array position would be wrong because the pricing endpoint orders tiers alphabetically (`ORDER BY tier` in [sqlc/queries.sql](../../../sqlc/queries.sql)). See `LayoutMeta` in [internal/catalog/layout.go](../../../internal/catalog/layout.go).

## L3 Mid

**Q9. Why serve layout as static immutable files instead of an API response?**
Seat positions do not change per request. A versioned path (`venues/{id}/{version}/layout.json`) plus `Cache-Control: ... immutable` lets browsers and a CDN cache it forever, and the API only serves the small, changing availability bitset. The cost is that the layout version in the path and in the bitset must match, or seats render wrongly. The plan treats this skew as a correctness hazard ([plan.md](../../plan.md), "Static layout format"). Theory: [deep-dive.md](../../deep-dive.md) section 8 and the Caching Strategy section.

**Q10. What did the `EXPLAIN ANALYZE` evidence prove, and what did it not?**
It proved that after `ANALYZE event_seats` the planner used an Index Only Scan on `event_seats_cover_idx` with Heap Fetches 0 (3.625 ms against 10.403 ms for the bitmap plan, per MILESTONES). It did not prove production latency: it is one local Postgres with one event's rows. Index definition: [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql).

**Q11. Why does the publisher run `ANALYZE` itself?**
After a 30,000-row `CopyFrom` the planner's estimate was stale (93 rows against 30,000), so it chose a Bitmap Heap Scan plus Sort. Autovacuum's analyze threshold is a percentage of the table, which one bulk-loaded event rarely crosses. One explicit `ANALYZE` after the load fixes it. See the comment in [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go).

**Q12. What do the unit tests in this phase prove, and where is the gap?**
`seatmap_test.go` proves the bit packing and the status mapping; `layout_test.go` proves header bytes, ordinals equal walk position, and closed-flag propagation ([internal/seatmap/seatmap_test.go](../../../internal/seatmap/seatmap_test.go), [internal/catalog/layout_test.go](../../../internal/catalog/layout_test.go)). Gap: the Go encoder and the Go test share one author and one package. The web test in Phase 4 mirrors the encoder in TypeScript rather than reading Go-written bytes, so only the Playwright E2E (real `seats.bin` from MinIO) proves the browser parses the real file. No golden byte vector is shared between Go and TS. Handlers, SQL, and the publisher have no Go tests.

## L4 Senior

**Q13. What breaks about `listEvents` as the catalog grows?**
For each event it runs two extra queries, `MinEventPriceCents` and `CountAvailableSeats`, ignoring their errors (`minPrice, _ :=`). That is up to 100 x 2 queries per page, and `CountAvailableSeats` counts rows in `event_seats` each time. The SQL comment in [sqlc/queries.sql](../../../sqlc/queries.sql) says to revisit with a materialized counter only if measured as a bottleneck. It was not measured here. Also, a failed count silently shows as 0.

**Q14. What happens if Redis is cold or down on the availability path?**
`tryRedisAvailability` returns not-ok and the handler scans all of the event's `event_seats` rows (30,000 in the fixture) for every request. Correctness holds because Postgres is the arbiter, but read load shifts onto Postgres. In a stampede that fallback would be the expensive path. The ETag only saves bandwidth, because the handler still builds the bitset before comparing the ETag. I did not measure this. See [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go).

**Q15. Is `cmd/event-publisher` safe to retry?**
No, despite its package comment. Every run calls `CreateEvent`, so a retry makes a second event. The steps (insert event, tiers, seats, upload, flip status) are not one transaction, so a crash leaves a DRAFT event with partial data. MILESTONES admits re-run safety was not tested. A fix would key the job on `event_id` and check `events.status`, which is what [plan.md](../../plan.md) describes. See [cmd/event-publisher/main.go](../../../cmd/event-publisher/main.go).

**Q16. What does 100x scale break, and what does this phase not solve?**
Not measured, so treat these as hypotheses. The p50 spread in MILESTONES (95 ms at 20 concurrent against 25 ms at 5, with DB execution at about 3.6 ms) points to connection-pool queuing, on a single local Postgres under `go run`, so it is not a capacity claim. At 100x seats the bitset grows linearly (about 750 KB for 3 million seats by the same 2-bit math) and the DB-scan fallback grows with it. Not solved in this phase: real-time updates (Phase 7), cache invalidation, per-section filtering, layout-version migration for a live event, and any measurement against real AWS. Layout metadata is a rectangle per section, an honest simplification noted in [internal/catalog/layout.go](../../../internal/catalog/layout.go).
