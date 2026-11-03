# Phase 5 — Expiry + Outbox + Reaper: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Scope: [MILESTONES.md, Phase 5](../../../MILESTONES.md). Theory: [deep-dive.md](../../deep-dive.md) section 2 ("Every Side Effect of Expiry").

1. **hold** — A hold is a short reservation of a seat while the buyer pays. It is a row state in `event_seats`, not a separate table. Analogy: a coat on a theatre seat. In this project: [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

2. **hold expiry** — A hold has a deadline (`hold_expires_at`). After it, the seat must be sellable again. The default TTL (time to live) is 600 seconds from `HOLD_TTL_SECONDS`. Analogy: a restaurant gives your table away if you are 15 minutes late. In this project: [internal/config/config.go](../../../internal/config/config.go).

3. **passive expiry** — The seat is not "cleaned" when the deadline passes. Instead every booking statement asks "is this seat free, OR is its hold already past its deadline?" and takes the seat if so. Nothing has to run for this to be correct. Analogy: nobody removes the coat; the next person just sits, because the sign says the time is up. In this project: the `AcquireHold` query in [sqlc/queries.sql](../../../sqlc/queries.sql) (`hold_expires_at < now()` in its `WHERE`).

4. **active expiry** — A background job that actually flips expired seats back to AVAILABLE. This is only for freshness: other screens see the seat as free sooner. It is not needed for correctness. Analogy: a cleaner who clears abandoned coats so the room looks right. In this project: [internal/reaper/reaper.go](../../../internal/reaper/reaper.go).

5. **reaper** — The "reaper" is the active-expiry job. It finds expired holds and releases each one by calling the same `ReleaseHold` every other caller uses. Analogy: a ticket inspector walking the aisle. In this project: [cmd/hold-reaper/main.go](../../../cmd/hold-reaper/main.go) (runs every 3 seconds).

6. **side effect** — Releasing a seat is not enough. Other things must also happen: counts change, caches refresh, the user's page shows "hold lost", analytics are told. Each of those is a side effect. Analogy: when a guest checks out, you also update the cleaning list and the billing. See [deep-dive.md](../../deep-dive.md) section 2 for the full list of eight. In this project: the mapping table in [MILESTONES.md Phase 5](../../../MILESTONES.md).

7. **transaction** — A group of database writes that either all happen or none do. Analogy: a bank transfer. Money leaving one account and arriving in another must be one step. In this project: `AcquireHold`, `ReleaseHold` and `ConfirmSeats` each open one in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

8. **dual-write problem** — If you write to the database and then call another system (a message bus), the process can crash between the two. Then the bus never hears about a change that did happen. Analogy: you pay the shop but the receipt printer jams, so there is no record. In this project: this is exactly what the outbox below avoids.

9. **transactional outbox** — Instead of calling the bus directly, write the "event" as a row in your own database, inside the same transaction as the real change. A separate job sends it later. Either both the change and the event exist, or neither does. Analogy: put the letter in the outbox tray in the same motion as filing the form; the post person collects it later. In this project: [internal/events/events.go](../../../internal/events/events.go) (`Publish`) and table `domain_events` in [migrations/000005_domain_events.up.sql](../../../migrations/000005_domain_events.up.sql).

10. **domain event** — A small record saying "something real happened", such as `seat.held`, `seat.released`, `seat.booked`. Other parts of the system react to it. Analogy: a note on the kitchen pass: "table 4 ordered". In this project: written by `AcquireHold`, `ReleaseHold`, `ConfirmSeats` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

11. **aggregate_id** — The id of the thing an event is about. Here a seat's id is `"eventId:seatId"` (for example `1:701`), so the column is TEXT, not UUID. Analogy: a street address needs a street and a number. In this project: [migrations/000005_domain_events.up.sql](../../../migrations/000005_domain_events.up.sql).

12. **relay** — The job that reads unsent outbox rows (`published_at IS NULL`) and sends them to the message bus, then marks them sent. Analogy: the post person emptying the outbox tray. In this project: [internal/outbox/relay.go](../../../internal/outbox/relay.go) (`Relay.RunOnce`), run by [cmd/outbox-relay/main.go](../../../cmd/outbox-relay/main.go) every 2 seconds.

13. **EventBridge** — An AWS message bus. You send it events; it routes them to subscribers. Locally the project uses `moto` (a fake AWS server) for it. Analogy: a post office sorting room. In this project: the `Publisher` interface and `PutEvents` call in [internal/outbox/relay.go](../../../internal/outbox/relay.go).

14. **at-least-once delivery** — The relay may send the same event twice (for example it crashes after sending, before marking sent). It will never silently drop one. So receivers must cope with duplicates. Analogy: a courier who re-delivers if you did not sign. In this project: the comment on `RunOnce` in [internal/outbox/relay.go](../../../internal/outbox/relay.go).

15. **idempotent** — Doing it twice has the same effect as doing it once. Releasing a hold that is already gone is a harmless no-op and emits no new event. Analogy: pressing the lift button twice. In this project: `ReleaseHold` (only publishes when rowcount > 0) in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

16. **rowcount** — How many rows an `UPDATE` changed. `ReleaseHold` uses it to know if it really released something. 0 means "already gone", which counts as success. In this project: the `ReleaseHold :execrows` query in [sqlc/queries.sql](../../../sqlc/queries.sql).

17. **FailedEntryCount** — EventBridge can answer "OK" for the call but reject individual events inside it. The relay checks this count, not just "no Go error", before marking a row sent. Analogy: the courier says "delivered" but one parcel was refused at the door. In this project: [internal/outbox/relay.go](../../../internal/outbox/relay.go).

18. **outbox_seq** — A number (`BIGSERIAL`, an auto-increasing counter) given to each outbox row at insert time. Sorting by timestamp alone is not a reliable order for rows inserted microseconds apart. Analogy: a ticket number at the deli counter beats "who looked like they came first". In this project: [migrations/000008_domain_events_outbox_seq.up.sql](../../../migrations/000008_domain_events_outbox_seq.up.sql). It was added in Phase 7, but the relay's query orders by it now.

19. **processed_events** — A table that lets each consumer remember which events it has already handled, so duplicates are skipped. It is created in this phase and first used by the projector in Phase 7. Analogy: a tick list at the door. In this project: [migrations/000005_domain_events.up.sql](../../../migrations/000005_domain_events.up.sql).

20. **ticker loop and Lambda twin** — Locally, `cmd/*` programs loop on a timer. In AWS the same code runs as a Lambda (a short-lived function) invoked on a schedule. Both call the same function, so there is one copy of the logic. Analogy: the same recipe cooked at home or in a restaurant kitchen. In this project: [cmd/hold-reaper-lambda/main.go](../../../cmd/hold-reaper-lambda/main.go) and [cmd/outbox-relay-lambda/main.go](../../../cmd/outbox-relay-lambda/main.go).
