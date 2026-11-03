# Phase 3 — Hold / Release / Confirm (CAS): Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

This is the keystone phase: how the system makes sure one seat is never given to two people. Background theory: [deep-dive.md](../../deep-dive.md) sections 2 (hold vs booking), 3 (concurrency control), 4 (distributed locks, fencing), 5 (seat state machine).

1. **Hold vs booking** — A hold is a temporary reservation: "this seat is yours for a few minutes while you pay". A booking is permanent after payment. Seat `status` values are 0 available, 1 held, 2 booked, 3 pending payment. It is like a library: reserving a book (hold) versus checking it out (booking).
   In this project: `AcquireHold` and `ConfirmSeats` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

2. **TTL (time to live)** — TTL is how long something stays valid before it expires. Holds last `HOLD_TTL_SECONDS`, which defaults to 600. After that the seat can be taken again. It is like a parking ticket that runs out.
   In this project: `HoldTTLSeconds` in [internal/config/config.go](../../../internal/config/config.go)

3. **Race condition** — A race condition is when the result depends on which of two things happens first. Two buyers click the same seat in the same millisecond: who wins? Without care, both could. It is like two people grabbing the last cookie at once.
   In this project: `TestAcquireHold_ExactlyOneWinnerUnderRace` in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go)

4. **Double-booking** — Double-booking means the same seat sold to two people. This project's whole goal is to make that impossible. It is like two guests handed the same hotel room key.
   In this project: the package comment of [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

5. **Compare-and-swap (CAS)** — CAS means "change this only if it still looks like I expect, and tell me if it did". In SQL it is an `UPDATE ... WHERE seat is still free`. The check and the change happen as one step, so nobody can sneak in between. It is like signing a visitor book only if the line is still blank.
   In this project: `AcquireHold` query in [sqlc/queries.sql](../../../sqlc/queries.sql)

6. **Row count** — After an `UPDATE`, the database tells you how many rows changed. If you asked to change 3 seats and only 2 changed, someone else got one. The code reads that number to decide win or lose. It is like counting how many of your three parcels the courier actually took.
   In this project: `len(rows) != len(seatIDs)` in `AcquireHold`, [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

7. **Optimistic vs pessimistic locking** — Pessimistic locking locks the thing first, then works. Optimistic locking works first and checks at the moment of writing whether anything changed. This project uses the optimistic style with a SQL `WHERE` check (a CAS). It is like a toilet with a lock on the door (pessimistic) versus walking in and apologising if it is occupied (optimistic).
   In this project: [sqlc/queries.sql](../../../sqlc/queries.sql). Theory: [deep-dive.md](../../deep-dive.md) section 3.

8. **Transaction and rollback** — A transaction groups several database steps so they all happen or none do. Rollback undoes everything done so far. A multi-seat hold is all-or-nothing. It is like a shopping basket that is cancelled if one item is out of stock.
   In this project: `s.pool.Begin(ctx)` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

9. **Passive expiry** — Passive expiry means an expired hold is not removed by a timer. Instead, the next `UPDATE` simply treats "held but past its time" as free in its `WHERE` clause. So correctness never depends on a background job running. It is like a restaurant giving your table away when you are 20 minutes late, without anyone having to phone you.
   In this project: `hold_expires_at < now()` in `AcquireHold`, [sqlc/queries.sql](../../../sqlc/queries.sql)

10. **Redis SET NX PX** — `SET key value NX PX 600000` means "set this key only if it does not exist (NX), and delete it after 600000 milliseconds (PX)". The first caller wins. It is a very quick "first come" test. It is like a bathroom "occupied" sign that falls off after ten minutes.
    In this project: `Acquire` in [internal/holdlock/holdlock.go](../../../internal/holdlock/holdlock.go)

11. **Contention filter vs arbiter** — A filter quickly turns away most losers cheaply. An arbiter is the final judge whose decision counts. Here Redis is the filter and Postgres is the arbiter, so Redis can be wrong, empty, or down without causing double-booking. It is like a bouncer (filter) versus the ticket scanner (arbiter).
    In this project: package comment of [internal/holdlock/holdlock.go](../../../internal/holdlock/holdlock.go)

12. **Lua compare-and-delete** — Redis can run a tiny Lua script as one uninterruptible step. The release script deletes the key only if its value is still my hold ID. That stops me from deleting someone else's newer hold. It is like returning a coat only if the tag matches your ticket.
    In this project: `releaseScript` in [internal/holdlock/holdlock.go](../../../internal/holdlock/holdlock.go)

13. **Fence token** — A fence token is a number that goes up by one each time a seat is held. Confirm must present the exact token it was given. A holder whose hold expired and was reissued carries an old number and is refused. It is like a numbered queue ticket: yesterday's ticket does not work today.
    In this project: `fence_token` in `ConfirmSeats`, [sqlc/queries.sql](../../../sqlc/queries.sql)

14. **Zombie holder** — A zombie is a client or worker that is still running after its hold already expired, such as a paused process that wakes up late. It still thinks it owns the seat. Fence tokens and `hold_id` checks stop it. It is like a person arriving with an old reservation after the table was reseated.
    In this project: `TestStaleFenceRejected` in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go)

15. **Idempotent release** — Idempotent means doing it twice has the same effect as once. Releasing a hold that is already gone returns success (row count 0 is not an error). It is like pressing an elevator button that is already lit.
    In this project: `ReleaseHold` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

16. **Deadlock and sorted IDs** — A deadlock is when two operations each wait for something the other holds, forever. If every multi-seat request locks seats in ascending ID order, they cannot wait on each other in a circle. It is like two people always taking the left fork first.
    In this project: `sortDedup` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

17. **Fail-open degrade** — Fail-open means that when the helper (Redis) is down, the system skips it and carries on with the arbiter (Postgres) instead of refusing all requests. It is slower, not wrong. It is like a broken turnstile that staff wave people through while a guard checks tickets by hand.
    In this project: `ErrUnavailable` in [internal/holdlock/holdlock.go](../../../internal/holdlock/holdlock.go) and the `break` in `AcquireHold`

18. **HTTP status codes 409, 410, 422** — 409 Conflict means "you lost a race, try different seats". 410 Gone means "that hold no longer exists". 422 Unprocessable means "your request is invalid", such as a duplicate seat. Each tells the client what to do next. It is like different stamps on a rejected form.
    In this project: `writeInventoryError` in [internal/httpapi/holds.go](../../../internal/httpapi/holds.go)

19. **Sentinel error** — A sentinel error is a named, fixed error value like `ErrSeatTaken` that callers compare against with `errors.Is`. `errors.As` pulls out richer data, like which seats conflicted. It is like a colour-coded warning light instead of a vague "something is wrong".
    In this project: `ErrSeatTaken`, `ConflictError` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

20. **Interface and test double** — An interface lists the methods something must have. `Locker` lists Redis's two methods, so tests can pass a fake instead of real Redis. gomock generates fake database objects the same way. It is like a stunt double standing in for an actor.
    In this project: `Locker` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go) and [internal/inventory/mocks/querier_mock.go](../../../internal/inventory/mocks/querier_mock.go)

21. **Unit vs integration test (build tag)** — A unit test runs alone with no outside services. An integration test uses the real Postgres and Redis. Go's build tag `//go:build integration` keeps the second kind out of a plain `go test ./...`. It is like testing a car engine on a bench versus on the road.
    In this project: `make test` versus `make test-integration` in [Makefile](../../../Makefile)

22. **Race test with a barrier** — To prove only one winner, the test starts 200 goroutines (Go's lightweight threads) that all wait on one channel, then releases them at once. It repeats 20 times because a race test that passes once proves little. It is like starting 200 runners with one starting gun.
    In this project: `TestAcquireHold_ExactlyOneWinnerUnderRace` in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go)

23. **Best available** — "Best available" finds a run of adjacent seats in the same row and holds them. If someone steals one, it tries the next run, up to 3 tries. It is like asking the cinema for three seats together and the clerk trying the next row if one is gone.
    In this project: `BestAvailable` and `findContiguousRuns` in [internal/inventory/best_available.go](../../../internal/inventory/best_available.go)

24. **Audit log** — An audit log is an append-only record of what happened, such as `ACQUIRED` or `LOST_DB_CAS`. Here a row is written for every outcome, outside the main transaction and best-effort: if the audit insert itself fails, the error is only logged, so it never slows the hot path. It is like a shop's CCTV: it records, it does not decide.
    In this project: `holds_audit` in [migrations/000003_holds_audit.up.sql](../../../migrations/000003_holds_audit.up.sql)

25. **Outbox row in the same transaction** — When a seat is held, the code also writes an event row ("seat.held") inside the same transaction. Either both are saved or neither. A later phase publishes those rows. It is like writing a note in the order book and the delivery slip with one pen stroke.
    In this project: `events.Publish` calls in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go) (publishing itself is Phase 5)
