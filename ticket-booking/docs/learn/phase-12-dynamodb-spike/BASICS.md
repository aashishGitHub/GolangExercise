# Phase 12 — DynamoDB Spike: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 12](../../../MILESTONES.md) · Background: [dynamodb-comparison.md](../../dynamodb-comparison.md), [deep-dive.md](../../deep-dive.md)

> All numbers come from [dynamodb-comparison.md](../../dynamodb-comparison.md). They were measured against `dynamodb-local` on one laptop. They are not real DynamoDB service numbers and not capacity claims.

1. **Spike** — a small experiment to learn something, not code to ship. This one asks: "what if DynamoDB, not Postgres, decided who gets a seat?" Nothing in `cmd/server` uses it. It is like a test drive, not a purchase.
   In this project: [cmd/dynamo-spike/main.go](../../../cmd/dynamo-spike/main.go)

2. **DynamoDB** — Amazon's key-value and document database. You store items (like rows) in a table and read them by key. It is built to be fast at huge scale. It is like a giant filing cabinet with a label on each folder.
   In this project: [cmd/dynamo-spike/table.go](../../../cmd/dynamo-spike/table.go)

3. **Item and attribute** — an item is one record. Attributes are its named fields, such as `status` or `hold_id`. There is no fixed schema: an item exists once you write it. It is like a card in a card index.
   In this project: `seedSeats` in [cmd/dynamo-spike/table.go](../../../cmd/dynamo-spike/table.go)

4. **Partition key** — the part of the key that decides which storage partition holds the item. Here it is `event_id`. All seats of one event share one partition key value. It is like the street name on an address.
   In this project: `ensureTable` in [cmd/dynamo-spike/table.go](../../../cmd/dynamo-spike/table.go)

5. **Sort key** — the second part of the key. It orders items inside one partition key. Here it is `seat_id`. Together, partition key plus sort key name exactly one item. It is like the house number on that street.
   In this project: `ensureTable` in [cmd/dynamo-spike/table.go](../../../cmd/dynamo-spike/table.go)

6. **Conditional write** — a write that only happens if a rule about the current item is true. If the rule fails, nothing changes. The rule is checked and applied as one atomic step (no one can sneak in between). It is like a vending machine that only drops the snack if the coin is real.
   In this project: `AcquireHoldSingle` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

7. **Condition expression** — the text of that rule. Here: the seat is new, or `FREE`, or `HELD` with an expiry time in the past. In plain words: "take it if nobody validly holds it". It is like a sign: "free seat or reservation lapsed".
   In this project: `ConditionExpression` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

8. **`UpdateItem`** — the DynamoDB call that changes some attributes of one item. With a condition it is the single-seat hold. Think of editing one cell in a spreadsheet, only if the cell currently says "free".
   In this project: `AcquireHoldSingle` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

9. **`ConditionalCheckFailedException`** — the error DynamoDB returns when the rule was false. The code maps it to `ErrConflict`, meaning "seat taken". It is a normal outcome, not a crash. It is like "sorry, that seat is gone".
   In this project: `ErrConflict` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

10. **`TransactWriteItems`** — one call that updates several items all-or-nothing. If one condition fails, none of the changes happen. It is how a 4-seat hold works. It is like a group booking: either all four seats are yours or you get none.
    In this project: `AcquireHoldMulti` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

11. **Atomicity** — "all or nothing, never half". Without it, you could hold 2 of 4 seats and be stuck. It is like a bank transfer that must not take money out without putting it in.
    In this project: post-run check in [MILESTONES.md Phase 12](../../../MILESTONES.md) (four seats, same `hold_id`)

12. **`TransactionCanceledException` and cancellation reasons** — when a transaction fails, DynamoDB lists one reason per item. The code scans them for `ConditionalCheckFailed`. If found, it is a real conflict and is not retried. It is like a rejection letter with a reason per line.
    In this project: `anyConditionFailed` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

13. **Retry with jittered exponential backoff** — if a failure may be temporary, wait, then try again, waiting longer each time (exponential) plus a random amount (jitter) so everyone does not retry at the same instant. The code allows 5 attempts. It is like people leaving a full car park at slightly different moments.
    In this project: `AcquireHoldMulti` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

14. **Throttling** — the service says "too many requests, slow down". It is a temporary condition, so retrying is fine. A real conflict is different: retrying will not change the answer. It is like a busy restaurant versus a table that is truly booked.
    In this project: comment in `AcquireHoldMulti`, [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

15. **Read-time expiry vs TTL** — read-time expiry means the write rule itself checks `hold_expires_at < now`. DynamoDB TTL is a background cleaner that deletes items later and lazily. TTL is fine for tidying but cannot be trusted for correctness. It is like checking the date on milk yourself, not waiting for the shop to remove it.
    In this project: comment on `AcquireHoldSingle` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

16. **`hold_id` and zombie confirm** — each hold gets a unique `hold_id`. Confirm only works if the stored `hold_id` still matches. A "zombie" is a slow client whose hold expired and was taken by someone else. Its old `hold_id` no longer matches, so its confirm fails. It is like a cloakroom ticket for a coat that was already collected.
    In this project: `ConfirmSeats` in [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

17. **Fence token** — a counter that only goes up, used to reject stale actors. Postgres carries one. The spike needs none because `hold_id` in the condition is enough (per the comparison doc). It is like a ticket number at a deli counter.
    In this project: [dynamodb-comparison.md](../../dynamodb-comparison.md), section "The model implemented"

18. **Race test with a barrier** — start 200 goroutines, make them all wait on one channel, then release them at once. They all grab the same seat. Exactly one must win. It repeats 20 rounds, because one pass proves little. It is like a starting gun for a footrace.
    In this project: `runRaceTest` in [cmd/dynamo-spike/race.go](../../../cmd/dynamo-spike/race.go)

19. **Uniform vs hot contention** — `uniform` picks seats spread across the pool. `hot` makes every worker target the same seat or seats. Same idea as Phase 10, but here the harness calls the hold functions directly, not over HTTP. It is like a food court versus one popular stall.
    In this project: `pickSeats` in [cmd/dynamo-spike/loadtest.go](../../../cmd/dynamo-spike/loadtest.go)

20. **Conflict rate** — the share of attempts that lost with "seat taken". In the spike, holds are never released, so the free pool only shrinks. Conflict rate climbs just because seats run out. That is why uniform conflict rates (64.6%, 68.9%) cannot be compared with Phase 10's 14.2%. It is like musical chairs: as chairs vanish, more players lose.
    In this project: `printLoadTestReport` in [cmd/dynamo-spike/loadtest.go](../../../cmd/dynamo-spike/loadtest.go)

21. **Pay-per-request billing and WCU** — pay-per-request means you pay for each call, with nothing to plan in advance (`BillingModePayPerRequest`). WCU is a write capacity unit, the unit DynamoDB charges for writes. The docs say transactions cost 2x the WCU. It is like paying per taxi ride instead of owning a car.
    In this project: `ensureTable` in [cmd/dynamo-spike/table.go](../../../cmd/dynamo-spike/table.go)

22. **`dynamodb-local`** — a free local program that speaks the DynamoDB API. It is good for correctness checks. It has no real partitions or replication, so its speed numbers do not predict the real service. It is like a flight simulator.
    In this project: `dynamodb-local` service in [docker-compose.yml](../../../docker-compose.yml)

23. **Compose profile** — a label on a Docker service so it starts only when you ask. `dynamodb-local` has profile `spike`, so plain `docker compose up -d` skips it. It is like an optional extra on a menu.
    In this project: [docker-compose.yml](../../../docker-compose.yml)

24. **Transactional outbox vs DynamoDB Streams** — in Postgres, the event row is written in the same transaction as the seat change. DynamoDB has no equivalent in one table operation. The usual pattern is Streams plus a separate consumer, which is extra machinery. The comparison doc calls this the largest asymmetry. It is like putting a letter in the outgoing tray as you sign it, versus having a second clerk collect it later.
    In this project: [dynamodb-comparison.md](../../dynamodb-comparison.md), "What transfers and what doesn't"

25. **Methodology caveat** — the spike times only the hold call, while Phase 10 times a full HTTP journey. The numbers are hold-only and not like-for-like. The doc says this plainly. It is like comparing a sprint to a marathon split time.
    In this project: [dynamodb-comparison.md](../../dynamodb-comparison.md), "Load scenarios"
