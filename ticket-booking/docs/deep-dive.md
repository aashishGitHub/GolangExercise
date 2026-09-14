# Deep Dive: Seat Reservation / High-Concurrency Inventory

> Three reading depths per section:
> - 🟢 **Beginner** — intuitive analogies, no jargon
> - 🟡 **Senior** — implementation mechanics, code, tradeoff tables
> - 🔴 **Architect** — failure modes, capacity math, production decisions
>
> Vendor-neutral. Ticketmaster / StubHub / airlines appear only as public examples.
> Every number tagged "illustrative" is a teaching figure with arithmetic shown — verify against your own load tests before quoting it.

---

## Table of Contents

1. [The Core Problem: Uniqueness Under Contention](#1-the-core-problem-uniqueness-under-contention)
2. [Hold vs Booking: The Two-State Model and Redis TTL Holds](#2-hold-vs-booking-the-two-state-model-and-redis-ttl-holds)
3. [Concurrency Control: Optimistic vs Pessimistic vs Redis NX](#3-concurrency-control-optimistic-vs-pessimistic-vs-redis-nx)
4. [Distributed Locks and the RedLock Debate](#4-distributed-locks-and-the-redlock-debate)
5. [The Seat State Machine and Overbooking Prevention](#5-the-seat-state-machine-and-overbooking-prevention)
6. [Payment: ACID Boundary, Idempotency, and the Saga](#6-payment-acid-boundary-idempotency-and-the-saga)
7. [Thundering Herd and the Virtual Waiting Room](#7-thundering-herd-and-the-virtual-waiting-room)
8. [Database Design: Schema, Sharding, and CQRS](#8-database-design-schema-sharding-and-cqrs)
9. [Global Distribution, Dynamic Pricing, and Operations](#9-global-distribution-dynamic-pricing-and-operations)
10. [Pattern Recognition — When and How](#10-pattern-recognition--when-and-how)
11. [Quick Recall Cheat Sheet](#quick-recall-cheat-sheet)

---

## 1. The Core Problem: Uniqueness Under Contention

### 🟢 Beginner — The Last Slice of Cake at a Party

Imagine a birthday party with one special corner slice of cake and fifty kids who all want it. If two kids grab the plate at the same instant, you cannot cut the slice in half and pretend both got "the corner slice" — it is a single, unique thing. Exactly one kid gets it; everyone else has to pick a different slice.

Now compare that to a bowl of identical candies. If two kids reach in at once, no problem — there are hundreds of candies and they are all the same. Nobody cares *which* candy they got.

A seat reservation system is the corner-slice problem, not the candy problem. Seat A14 in Row 3 is unique. Two people cannot sit in it. A shopping cart with 100 identical phone chargers is the candy problem — if you briefly oversell by one, you apologize, backorder, and refund. You can *never* do that with a seat, because there is no "extra chair" to hand out.

---

### 🟡 Senior — Non-Fungible Inventory and the Correctness Boundary

The defining property is **non-fungibility under high concurrency**: a single named resource must be sold to exactly one buyer, even when thousands of writers race for it in the same millisecond.

| Property | Shopping cart | Seat reservation |
|---|---|---|
| Inventory model | Fungible (100 identical widgets) | Non-fungible (seat A14 is unique) |
| Overselling | Tolerable — backorder, apologize, refund | **Never** — you cannot seat two people in one chair |
| Concurrency conflict | Rare, resolvable after the fact | The *default case* during a hot sale |
| Correctness boundary | Eventual (reconcile stock later) | Strong, at write time (before you say "confirmed") |
| Peak shape | Diffuse over hours | A wall of load in the first second |

The mistake candidates make is describing this as a *business rule* ("don't sell more tickets than seats"). At the systems level it is a **mutual-exclusion guarantee over a unique key**, enforced by an *atomic compare-and-set* on each state transition. The business rules (refund policy, resale, transfer) sit on top of that primitive; they never replace it.

```
Overbooking is NOT prevented by "check then set" (two steps):
    if seat.status == 'AVAILABLE':   # read
        seat.status = 'HELD'         # write   <-- the gap here IS the race
It is prevented by making check-and-act ONE atomic operation:
    Redis:  SET seat NX                          (set-if-absent)
    SQL:    UPDATE ... WHERE status='AVAILABLE'  (conditional, check rowcount)
```

---

### 🔴 Architect — CAP Positioning and What You Say in a Review

In a design review, state the position explicitly: **the seat confirmation path chooses consistency over availability (CP), and the browse path chooses availability (AP).** These are two different systems glued together, and conflating them is the number-one architecture error.

```
Illustrative framing for the review whiteboard:

  Browse / seat-map read  →  AP  →  stale-by-seconds is fine, cache aggressively
  Hold / confirm write    →  CP  →  reject a valid buyer before you double-sell

Cost of getting it wrong, in order of severity:
  1. Double-sold seat      → refund + chargeback + trust loss + press  (catastrophic)
  2. Rejected valid buyer  → user retries in a few seconds             (annoying)
  3. Stale availability    → wasted click, revalidated on hold          (invisible)
```

You would always rather produce error #2 or #3 than #1. That ranking drives every locking, failover, and fail-open/fail-closed decision downstream.

**Real-company story.** Amazon's original shopping cart (described in the 2007 Dynamo paper, *Dynamo: Amazon's Highly Available Key-value Store*, DeCandia et al.) deliberately chose availability: an "add to cart" must never fail, even during a partition, and conflicting cart versions are reconciled later (a deleted item can even reappear). That is the correct call for fungible cart contents. It is the *wrong* call for a seat map — you cannot reconcile "two people booked chair A14" after the fact. Same company, opposite consistency choice, because the inventory model is different. Naming this contrast is a strong senior signal.

---

## 2. Hold vs Booking: The Two-State Model and Redis TTL Holds

### 🟢 Beginner — Reserving a Library Book vs Checking It Out

When you *reserve* a library book online, the library sets it aside for you for, say, 3 days. It is not yours yet — if you do not come pick it up, the reservation quietly lapses and the book goes back on the shelf for someone else. No librarian has to chase you.

*Checking out* the book is different: you show your card, it is scanned, and now it is officially on your account until you return it. That record is permanent and survives even if the library's computer reboots.

A seat **hold** is the 3-day reservation: temporary, self-expiring, cheap. A seat **booking** is the checkout: durable, paid, permanent. Splitting these two ideas is the heart of the whole design.

---

### 🟡 Senior — Redis Holds, TTL Semantics, and Safe Release

A **hold** is soft state (TTL-managed, in Redis, cheap to lose). A **booking** is hard state (an ACID row in the durable database). The hold optimizes for latency and self-healing; the booking optimizes for durability and correctness.

```redis
# Acquire: atomic test-and-set with a 10-minute TTL.
# Value = a UNIQUE hold token so we can later release only OUR hold.
SET event:123:seat:A14 "user:456:hold:h789" NX PX 600000
# → "OK"  if the key did not exist (hold acquired)
# → (nil) if the key already exists (seat already held) → return 409 to the client

# Safe release: compare-and-delete via Lua, so we never delete someone else's hold
# if our TTL already expired and the seat was re-held in the meantime.
EVAL "if redis.call('GET', KEYS[1]) == ARGV[1]
        then return redis.call('DEL', KEYS[1])
        else return 0 end" 1 event:123:seat:A14 "user:456:hold:h789"
```

- `NX` = set only if the key does **not** exist → this is the mutex.
- `PX 600000` = expire after 600,000 ms = 10 minutes (600 × 1000).
- The naive alternative — `GET` then `DEL` as two commands — has a race: between your `GET` and your `DEL`, the original hold can expire and a *different* user can re-acquire the seat, and your `DEL` would then wipe *their* hold. The Lua script makes compare-and-delete a single atomic step.

Why Redis and not a `SELECT ... FOR UPDATE` in the primary database for the 10-minute hold window:

| Property Redis gives you | Why it matters for holds |
|---|---|
| Native per-key TTL | Auto-release on abandonment/crash — no cleanup cron |
| Atomic `SET NX` | Test-and-set in one round trip (the mutex primitive) |
| In-memory latency | Sub-millisecond; absorbs flash-sale write rate |
| No long-lived DB locks | Holds do not pin a DB connection/row lock for 10 minutes |

If you held seats with a database row lock for the full checkout, **every open cart would pin one DB connection and one row lock for up to 10 minutes**. Connection pools are sized in the hundreds, not the hundreds of thousands, so a busy sale exhausts the pool and stalls the entire database.

---

### 🔴 Architect — Every Side Effect of Expiry, and the Lazy-Expiry Gotcha

"Release the seat" is only step one. A correct hold expiry is a fan-out of side effects, and missing any of them is a real bug:

```
On hold expiry (TTL fires), the system MUST:
  1. Seat inventory:    hold key gone in Redis → seat is AVAILABLE again.
  2. Availability count: decrement "held" / increment "available" (cache + DB counter).
  3. Seat-map cache:    invalidate/refresh so other users see A14 free.
  4. Waiting room:      if demand-gated, signal that inventory freed up.
  5. User session:      mark checkout EXPIRED; the timer UI shows "hold lost."
  6. In-flight payment: if a payment is mid-flight for this hold, BLOCK/compensate it
                        (see §6) — never let a late payment confirm an expired hold.
  7. Analytics:         emit hold_expired → conversion & abandonment dashboards.
  8. Idempotency:       expire the checkout idempotency key so a fresh attempt is clean.
```

The gotcha: **Redis expiry is not instant.** Redis removes an expired key either lazily (when something next touches the key) or via a background sampler that checks a small random sample of keys roughly ten times per second (documented behavior; the exact sampling constants are Redis-internal). So keyspace-expiry notifications can lag the nominal TTL by a fraction of a second to seconds under load.

```
Design consequence:
  Do NOT rely solely on "the TTL fired" to decide ownership.
  ALWAYS re-validate expiresAt (and the hold token) at CONFIRMATION time in the DB CAS.
  The Redis TTL is an optimization; the DB confirmation is the arbiter (see §5).
```

The countdown the user sees must be derived from the **server's `expiresAt`**, never a client clock. The client timer is cosmetic; the server (Redis TTL + DB check) is the truth.

**Real-company story.** Airlines have run a hold/booking split for decades: a Passenger Name Record (PNR) is created with a **ticketing time limit** — the fare is held but not issued, and if you do not pay by the deadline the airline's system auto-cancels and the seat returns to inventory. This is the same soft-hold-then-durable-booking pattern, just measured in hours or days instead of minutes. The lesson generalizes: the hold is a promise with an expiry; the booking is the settled fact.

---

## 3. Concurrency Control: Optimistic vs Pessimistic vs Redis NX

### 🟢 Beginner — Two Ways to Share One Bathroom

Two roommates share one bathroom. **Pessimistic** locking is putting a lock on the door: you turn the latch, and the other person physically cannot get in until you come out. No conflict is possible, but they stand there waiting.

**Optimistic** locking is a shared sign-in sheet with no door lock: you write your name assuming nobody else is inside, and you only find out there was a clash if, when you open the door, someone is already there — at which point you back off and try again. It works great when clashes are rare, and it wastes a lot of walking-back-and-forth when they are common.

For the last concert ticket, where *everyone* clashes, the door lock (pessimistic) is calmer. For ordinary seats where clashes are rare, the sign-in sheet (optimistic) is faster.

---

### 🟡 Senior — Three Strategies, Same Race, Different Resolution

Two users grab seat A14 in the same microsecond. Both read `A14 = AVAILABLE`, both decide "it is free." Without atomicity, both write `HELD` → double-booking. Each strategy serializes the writers differently.

**(a) Pessimistic — `SELECT ... FOR UPDATE`**
```sql
BEGIN;
SELECT status FROM event_seats
  WHERE event_id=123 AND seat='A14' FOR UPDATE;   -- U1 takes the row lock
-- U2's identical SELECT FOR UPDATE BLOCKS here until U1 commits.
UPDATE event_seats SET status='HELD', hold_id='h789'
  WHERE event_id=123 AND seat='A14';
COMMIT;                                            -- lock released
-- U2 unblocks, re-reads status='HELD' → its logic rejects the hold.
```

**(b) Optimistic — version column (compare-and-swap)**
```sql
-- Both transactions read version=7.
-- U1:
UPDATE event_seats SET status='HELD', version=8
  WHERE event_id=123 AND seat='A14' AND version=7;   -- rows affected = 1 (wins)
-- U2:
UPDATE event_seats SET status='HELD', version=8
  WHERE event_id=123 AND seat='A14' AND version=7;   -- rows affected = 0 (version moved) → loses
-- Application checks rowcount; 0 → conflict → refresh + retry or reject.
```

**(c) Redis `SET NX`**
```redis
# U1:
SET event:123:seat:A14 "hold:U1" NX PX 600000   → OK   (wins)
# U2, microseconds later:
SET event:123:seat:A14 "hold:U2" NX PX 600000   → nil  (loses; key exists)
# Redis executes commands single-threaded, so exactly one NX succeeds.
```

| Strategy | Winner decided by | Loser learns via | Holds a lock during "think time"? |
|---|---|---|---|
| Pessimistic | Row-lock ownership | Blocked, then re-read | Yes (dangerous for long holds) |
| Optimistic | First to bump `version` | `rowcount = 0` | No |
| Redis NX | First `NX` to land | `nil` reply | No (TTL, not a DB lock) |

The **lost-update** problem is what all three prevent. The essential fix is that the write's `WHERE` clause must **re-assert the state the read observed** — `AND status='AVAILABLE'` or `AND version=7`. A blind `UPDATE ... SET status='BOOKED'` with no guard lets the second writer silently clobber the first.

---

### 🔴 Architect — When Optimistic Degrades Worse Than Pessimistic

The counter-intuitive result you must be able to defend: **optimistic locking degrades worse than pessimistic under high contention on the same row.** Each attempt reads, tries the CAS, fails with `rowcount=0`, and retries; the collective throughput collapses into a retry storm (livelock-like), while pessimistic would have quietly queued the writers one at a time.

```
Contention regime:
  Low conflict:  optimistic wins (no locks, high concurrency).
  High conflict: optimistic pays N reads + N failed writes + N retries per 1 success.
                 Pessimistic pays 1 lock-wait queue → linear, predictable latency.

The rule (concrete, not "it depends"):
  Most seats, most of the time (low/medium contention) → OPTIMISTIC or Redis NX.
  One hot row: the last GA ticket, a shared pooled counter → PESSIMISTIC (serialize).
  Hold spanning minutes of user "think time"            → NEITHER DB lock → Redis TTL.
  Final HELD→BOOKED commit of an already-decided seat   → OPTIMISTIC CAS on hold_id.
```

The metric that tells you to switch a hot resource from optimistic to pessimistic is the **update-conflict rate** = writes with `rowcount=0` ÷ total write attempts.

```promql
# Alert when optimistic retries start dominating a hot resource:
rate(seat_update_conflicts_total[1m]) / rate(seat_update_attempts_total[1m]) > 0.2
# > ~20% of writes conflicting (illustrative threshold — tune per workload)
# → route that specific row/pool through a pessimistic lock or a serialized queue.
```

**Real-company story (pattern, not a specific incident).** High-demand "general admission last 100 tickets" pools are the classic optimistic-lock failure. When the pool is a single counter row and 10,000 buyers hit it at once, an optimistic `UPDATE remaining = remaining-1 WHERE remaining > 0 AND version=?` produces a conflict rate near 100% — nearly every attempt fails and retries, and effective throughput craters. Teams that hit this move the hot counter to a serialized path: a single-threaded worker, a Redis `DECR` on an atomic counter, or a pessimistic lock, so the last-N allocation runs one-writer-at-a-time instead of ten-thousand-retriers-at-once.

---

## 4. Distributed Locks and the RedLock Debate

### 🟢 Beginner — The Conch Shell Rule

In *Lord of the Flies*, only the person holding the conch shell is allowed to speak. It is a simple rule for a group with no chairman: whoever holds the one shell has the floor; everyone else waits. A distributed lock is a conch shell for computer processes — only the process holding the lock may touch the protected resource.

The catch: what if the person holding the conch wanders off and falls asleep? Nobody else can ever speak. Real distributed locks solve this with a timer (the shell auto-passes after a while) — but that timer is exactly where the tricky bugs live.

---

### 🟡 Senior — Single-Node Lock, and What Redis Topology Buys You

For seat holds, the "lock" is just the `SET NX PX` from §2 — a per-seat mutex with an auto-expiry. The interesting engineering is what happens when the Redis holding those locks fails while, say, 50,000 seats are held.

| Topology | What happens on crash | Consequence |
|---|---|---|
| **Single Redis** | All 50K hold keys lost (unless AOF/RDB replay) | Every held seat reverts to AVAILABLE. **No double-booking** (DB never said BOOKED), but 50K users lose holds → mass re-contention |
| **Redis Sentinel** (primary + replicas) | Sentinel promotes a replica; holds within the async-replication gap are lost | Failover in seconds; *some* recent holds lost; availability restored |
| **Redis Cluster** | Only slots on the failed shard affected | Blast radius limited to that shard's key range, not all holds |
| **RedLock** (N independent masters, majority acquire) | A single master failing does not lose the lock if a majority still hold it | Higher correctness bar, higher latency and complexity |

The key insight: **holds are soft state.** Losing one is annoying (the user re-selects) but not a correctness violation, because the seat simply returns to AVAILABLE — a state the durable DB always agreed with. That is why most production ticketing systems accept Sentinel or Cluster with async replication rather than paying for RedLock on every hold.

---

### 🔴 Architect — The RedLock Controversy and Fencing Tokens

You should be able to summarize the RedLock debate accurately, because it is a favorite interview probe.

```
The debate (public, well-documented):
  - RedLock is a Redis-authored algorithm for distributed locks across N independent
    masters: acquire the lock on a MAJORITY of them within a time bound.
  - Martin Kleppmann's 2016 critique ("How to do distributed locking") argued RedLock
    is NOT safe as a hard mutex: a GC pause or clock jump can make a client believe it
    still holds a lock after the TTL expired, so two clients can act at once.
  - Redis's author (Salvatore Sanfilippo / antirez) published a rebuttal defending it.
  - The durable takeaway everyone agrees on: if correctness truly depends on the lock,
    you need a FENCING TOKEN — a monotonically increasing number issued with the lock —
    and the protected resource must REJECT any write carrying a stale token.
```

The architectural conclusion for seats: **do not put the overbooking guarantee in the Redis lock at all.** Put it in the DB confirmation CAS, and fence it.

```sql
-- The hold carries a monotonic fencing token; the DB rejects a stale one.
UPDATE event_seats
   SET status='BOOKED', booking_id=:bid
 WHERE event_id=123 AND seat='A14'
   AND status='HELD'
   AND hold_id=:holdId
   AND fence_token >= :expectedFence;   -- reject a write from a "zombie" holder
-- rowcount 1 → booked; rowcount 0 → the hold was stale/stolen → do NOT confirm.
```

Under this design, Redis can lose locks, pause, or clock-skew and the worst outcome is a *double-hold*, never a *double-book*, because the single-owner guarantee lives in the DB row's conditional update. Redis is the fast path; the DB is the source of truth.

**Real-company story.** The Kleppmann-vs-antirez exchange (2016) is real and public; it is the canonical reference for "why a lock service alone is not enough for correctness." The engineering lesson it seeded — *always fence at the resource* — is exactly why mature inventory systems treat Redis holds as an optimization and keep the authoritative mutex as a conditional DB write.

---

## 5. The Seat State Machine and Overbooking Prevention

### 🟢 Beginner — The Meeting Room Whiteboard

A shared meeting room has a little whiteboard on the door with three states: **Free**, **Reserved** (someone booked it for 2pm but is not in yet), and **In Use** (occupied right now). The rule everyone follows: you can only flip Free → Reserved, and Reserved → In Use. You can never jump straight from Free to In Use, and you can never have two names in the Reserved box at once. The whiteboard is the single source of truth for who has the room.

A seat works the same way, with an extra rule: only one pen is allowed to write on the board at a time, so two people cannot both write their name in the "Reserved" box in the same instant.

---

### 🟡 Senior — The State Machine and Its Legal Transitions

Model each seat as a small state machine. Illegal transitions must be *impossible*, not merely discouraged.

```
                 hold (SET NX / CAS)              confirm (DB CAS on hold_id)
   ┌──────────┐ ───────────────────▶ ┌────────┐ ─────────────────────────▶ ┌────────┐
   │AVAILABLE │                      │  HELD  │                            │ BOOKED │
   └──────────┘ ◀─────────────────── └────────┘   (payment succeeded)      └────────┘
        ▲          TTL expiry / release      │                                  │
        │          (compare-and-delete)      │ payment fails / user abandons    │ refund /
        └────────────────────────────────────┘                                 │ cancel
        ▲                                                                       │
        └───────────────────────────────────────────────────────────────────── ┘

  Optional intermediate: HELD → PENDING_PAYMENT (during the charge) so a naive TTL
  expiry cannot hand the seat to another user while money is in flight.

  Legal transitions ONLY:
    AVAILABLE → HELD        (atomic CAS, exactly one winner)
    HELD      → BOOKED      (DB CAS on hold_id, the overbooking arbiter)
    HELD      → AVAILABLE   (TTL expiry or explicit release)
    BOOKED    → AVAILABLE   (refund / cancellation, an admin/compensation path)
  ILLEGAL (must be structurally impossible):
    AVAILABLE → BOOKED      (skips the hold; no mutex was taken)
    HELD(userA) → BOOKED(userB)   (confirming someone else's hold)
```

The overbooking guarantee is one line: the confirming `UPDATE` re-asserts `status='HELD' AND hold_id=:mine`, so for a given seat **at most one writer gets `rowcount=1`**. Everyone else gets `rowcount=0` and is rejected or compensated.

```sql
-- The single most important statement in the whole system:
UPDATE event_seats
   SET status='BOOKED', booking_id=:bid, hold_id=NULL, version=version+1
 WHERE event_id=:eid AND seat=:seat
   AND status='HELD' AND hold_id=:holdId;   -- CAS: is MY hold still the valid one?
-- rowcount == 1 → I booked it.  rowcount == 0 → hold expired/stolen → do NOT confirm.
```

---

### 🔴 Architect — The Expiry-vs-Payment Race and the Overbooking Metric

The nastiest race: user A's hold expires in Redis at the exact millisecond A's payment completes at the provider. Redis frees the seat, user B instantly holds it, then A's payment lands. Two users believe they own A14.

```
Prevention layers (defense in depth):
  1. Transition HELD → PENDING_PAYMENT (or extend the hold) when the charge STARTS,
     so a naive TTL expiry cannot hand the seat to B during A's charge.
  2. The DB confirmation CAS is the sole arbiter:
       UPDATE ... WHERE status='HELD' AND hold_id=:holdA
       rowcount 1 → A booked; B will get 0 on its own confirm.
       rowcount 0 → A's hold was already gone/re-held → A did NOT book.
  3. If A's confirm returns 0 but A was already charged → auto-REFUND A (compensate),
     apologize, optionally offer a comparable seat. Money never sits with no seat.
  4. Fence with a monotonic token (§4) so a zombie/GC-paused holder cannot confirm late.
```

Only one of A/B can win the CAS on A14. Two users can never both hold a *confirmed* row because `status='HELD' AND hold_id=?` matches exactly one writer.

The production signal that overbooking prevention is working — and the alert that it is under attack:

```promql
# CAS-with-rowcount=0 that WOULD have double-booked. A steady low rate is normal
# (it's just losers of legitimate races). A spike means a bug or an attack:
rate(seat_double_book_prevented_total[1m]) > 50    # illustrative — investigate

# A true overbooking (two BOOKED rows for one seat) should be IMPOSSIBLE.
# If this ever fires, it is a Sev-1 correctness breach:
sum(bookings_confirmed) by (event_id, seat_id) > 1  # must always be 0
```

**Illustrative capacity note.** If a hot event has 5,000 buyers racing for a 500-seat front section, roughly 4,500 of those hold/confirm attempts will legitimately lose the CAS. That is not a bug — it is the expected loser count. You size dashboards to expect a high *prevented-double-book* rate during on-sale and alert only on deviations from the modeled shape, not on the raw number.

**Real-company story.** Airlines deliberately *do* overbook fungible economy cabins (they sell more seats than exist, betting on no-shows, and bump passengers when everyone shows up) because a coach seat is treated as fungible and the compensation cost is bounded. But **assigned premium seats and specific reserved seats are never double-sold** — those are non-fungible, and the assignment is an atomic write. The contrast is instructive: overbooking is a *policy* choice available only where inventory is fungible; for named seats the state machine forbids it outright.

---

## 6. Payment: ACID Boundary, Idempotency, and the Saga

### 🟢 Beginner — Paying a Contractor in Milestones

You hire a contractor to renovate a kitchen. You do not hand over all the money and hope; you pay in milestones — deposit, then after cabinets, then after countertops. If the countertop step fails, you do not tear out the cabinets; you have a clear record of what was done and what to refund. Each step is recorded, and each step has a "how to undo it" plan.

A booking is the same. You do not squeeze "reserve the seat," "charge the card," and "issue the ticket" into one magic instant. You do them as ordered steps, you write down what happened after each one, and each step has a matching undo (a refund, a release) in case a later step fails.

---

### 🟡 Senior — The ACID Boundary and the Idempotency Key

You **cannot put the external payment charge inside a database transaction.** The charge is a remote call to a payment provider that can take seconds and cannot be undone by `ROLLBACK`. So the boundary is: keep the *local* writes in one ACID transaction, and treat the *payment* as an external step wrapped in idempotency and a saga.

```sql
-- ACID transaction at CONFIRMATION time, AFTER the payment is authorized/captured:
BEGIN;
  UPDATE event_seats
     SET status='BOOKED', booking_id=:bid, hold_id=NULL, version=version+1
   WHERE event_id=:eid AND seat IN (:seats)
     AND status='HELD' AND hold_id=:holdId;      -- CAS: hold still valid?
  -- assert affected rows == number of seats; else ROLLBACK (hold expired/stolen)
  INSERT INTO bookings (booking_id, user_id, event_id, amount, payment_ref, status)
       VALUES (:bid, :uid, :eid, :amt, :paymentRef, 'CONFIRMED');
  INSERT INTO payments (payment_ref, booking_id, status)
       VALUES (:paymentRef, :bid, 'CAPTURED');
COMMIT;
-- Inside the txn: seat flip + booking row + local payment record. All or nothing.
-- OUTSIDE the txn: the actual charge at the provider (idempotent, see below).
```

Preventing a double charge when a user double-clicks "Pay Now" is an **idempotency key** that is stable across retries of the *same logical payment*:

```typescript
// Deterministic per checkout attempt (NOT per HTTP request) — a double-click
// produces the SAME key, so it maps to ONE charge.
function idempotencyKey(userId: string, orderId: string, holdId: string): string {
  return sha256(`${userId}:${orderId}:${holdId}`);
}

async function pay(order: Order): Promise<PaymentResult> {
  const key = idempotencyKey(order.userId, order.id, order.holdId);

  // Insert-first: a UNIQUE constraint on idempotency_key is the real guard.
  const inserted = await db.query(
    `INSERT INTO payment_attempts (idempotency_key, order_id, status)
     VALUES ($1, $2, 'PENDING')
     ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`, [key, order.id]);

  if (inserted.rowCount === 0) {
    // The first click already owns this key → return its result, do NOT charge again.
    return await getExistingResult(key);
  }

  // We won the race → charge exactly once, passing the SAME key to the provider,
  // which dedupes on its side too (belt and suspenders).
  return await paymentProvider.charge({ amount: order.total /* ... */ },
                                      { idempotencyKey: key });
}
```

Two independent layers protect you: your **DB unique constraint** (`ON CONFLICT DO NOTHING`) and the **provider's own idempotency key**. Both Stripe and Braintree publicly document idempotency-key support for exactly this purpose (verify current field names in their live docs before coding).

---

### 🔴 Architect — The Saga, the 408 Timeout, and the Money Invariant

Model the whole booking as a **saga**: a sequence of local transactions, each with a compensating action. Prefer **orchestration** (a central coordinator) over choreography here — the flow is linear and you want one place to drive compensation.

| Step | Forward action | Compensating action |
|---|---|---|
| 1. Hold | `SET NX` seat in Redis (10-min TTL) | Compare-and-delete the hold |
| 2. Charge | Capture payment (idempotent) | Refund the charge |
| 3. Confirm | `UPDATE seat → BOOKED` (CAS on hold_id) | `UPDATE seat → AVAILABLE` + cancel booking |
| 4. Issue ticket | Generate/deliver the e-ticket | Void/revoke the ticket |

```
Orchestrator (each step idempotent; state persisted after each):
  try:
    hold    = acquireHold(seat)            # comp: releaseHold
    charge  = capture(payment, idemKey)    # comp: refund(charge)
    booking = confirm(seat, hold, charge)  # comp: unconfirm(booking)
    ticket  = issue(booking)               # comp: void(ticket)
    return success(ticket)
  except StepFailure as f:
    for step in reverse(completed_steps):  # compensate in REVERSE order
        step.compensate()
    return failure(f)
```

The **HTTP 408 / network timeout** on the charge is the ambiguous case — the charge may have succeeded, failed, or still be in flight. The rule: **never blindly retry a raw charge.**

```
On 408 / timeout:
  1. Do NOT create a new charge blindly.
  2. Retry with the SAME idempotency key → the provider dedupes: if the first landed,
     you get that same result back (no second charge); if not, this creates exactly one.
  3. OR query status by idempotency key:
       SUCCEEDED → confirm booking
       FAILED    → surface error; keep the hold if TTL remains; let the user retry
       PENDING/UNKNOWN → backoff + poll; do NOT confirm yet
  4. Bound the ambiguity window: if unresolved before the hold TTL, transition the seat
     to PENDING_PAYMENT (so TTL does not release it) or compensate.
```

The invariant you never violate, enforced by a durable **outbox + reconciler**:

```
  MONEY CAPTURED  ⇒  EVENTUALLY (a valid booking)  OR  (a refund).
Never money-with-no-seat. Never a seat-with-no-money.

Reconciler (runs on restart + on a timer):
  1. Find payment_attempts stuck PENDING past a threshold.
  2. Ask the provider for the charge status by idempotency key.
     - CAPTURED and no booking, hold still valid → COMPLETE the booking (idempotent).
     - CAPTURED but hold expired/seat taken     → COMPENSATE: auto-refund + notify.
     - NOT captured                             → safe to cancel the attempt.
  3. Emit an event either way (ticket issued OR refund issued) so the user is made whole.
```

**Named tradeoff — atomicity vs latency (2PC vs saga).** A true two-phase commit spanning your DB and the payment provider would give atomicity, but the coordinator blocks and payment providers do not expose XA transactions. So you accept a saga: no global rollback, eventual correctness via idempotent compensations. This is the standard pattern for microservice payments (see Chris Richardson's *Microservices Patterns* for the canonical write-up).

**Real-company story (pattern).** The "charged but no order" failure is common enough that every serious payments stack builds a reconciliation job for it. The generalizable design — persist intent (an outbox row) *before* charging, carry an idempotency key so the provider can tell you the outcome, and run a reconciler that turns every stuck charge into either a fulfilled order or a refund — is what turns a 3am manual scramble into an automated self-heal. If you cannot describe this loop in an interview, the interviewer assumes you have never operated a payment system in production.

---

## 7. Thundering Herd and the Virtual Waiting Room

### 🟢 Beginner — Opening the Gates at a Stadium

Picture 150,000 fans pressed against a stadium's front doors, and at exactly 10:00 someone unlocks all the doors at once. People are crushed, nobody moves, and the entrance jams — ironically *fewer* people get in than if you had opened the doors in an orderly stream.

Smart venues use turnstiles and a holding area: fans wait in a marshalled line and are let through at the rate the concourse can actually handle. Nobody is crushed, and the flow is steady. A **virtual waiting room** is that holding area for a website — it converts a stampede into a queue.

---

### 🟡 Senior — The Waiting Room Architecture and Fair Positions

A naive on-sale (no queue, direct DB access) collapses:

```
At 10:00:00.000, ~150,000 clients fire "Buy" within the same second:
  - Each triggers seat-map load + availability read + hold attempt → ~3–5 requests each
    → ~500K–750K requests hit the origin in the first second (illustrative fan-out:
       150,000 × ~4 ≈ 600,000).
  - The DB connection pool (say 500 connections) saturates in milliseconds.
  - Hold/confirm queries queue behind locks on the hottest seats (front row).
  - Latency spikes → clients time out → clients RETRY → load multiplies (retry storm).
  - Health checks fail → autoscaler thrashes → cascading failure.
  → The whole service falls over; even users who "got in" error out mid-checkout.
```

The fix is a **stateless front gate** at the edge that admits users at a controlled rate:

```
        150K users hit the sale URL at 10:00:00
                        │
             ┌──────────▼───────────┐
             │  WAITING ROOM (edge)  │  stateless, served from CDN/edge worker
             │  - assigns queue pos  │
             │  - shows "you are #N"  │
             └──────────┬───────────┘
                        │  admits in batches (e.g., 5K/min = f(inventory capacity))
             ┌──────────▼───────────┐
             │   ADMISSION TOKEN     │  signed JWT/HMAC → grants a bounded entry window
             └──────────┬───────────┘
                        │  only admitted users pass
             ┌──────────▼───────────┐
             │ SEAT INVENTORY SVC    │  sees ~5K/min, never 150K at once
             │ (holds, confirm)      │  strong-consistency write path, sharded by event
             └──────────┬───────────┘
                        ▼
                   DB (primary) + Redis holds
```

Assigning **fair, tamper-proof** positions to 150K arrivals in one second:

```
Fairness (order by true arrival):
  Option A — atomic counter:  pos = INCR queue:event:123:counter   # O(1), monotonic
  Option B — sorted set:      ZADD queue:event:123 <arrivalMicros> <userId>
                              rank = ZRANK queue:event:123 <userId> # true-time order

Tamper-proofness:
  - Return a SIGNED token (JWT/HMAC) carrying {userId, eventId, position, issuedAt}.
    The user cannot change position without breaking the signature.
  - Bind the token to the user/session so it cannot be traded or replayed.
  - The server is the sole authority; the client only DISPLAYS the position.
```

| Structure | Gives you | Cost |
|---|---|---|
| `INCR` counter | O(1) atomic position; dead simple | Single hot key; ties by call order only |
| Sorted set (`ZADD`/`ZRANK`) | Ordering by true arrival time; rank queries | More memory; O(log N) ops |

The inventory service is **capacity-planned for the admitted rate, not the arrival rate.** That decoupling is the entire point.

---

### 🔴 Architect — Closed-Loop Admission, Bot Defense, and the 2022 Lesson

The admission rate is a **closed-loop control problem**: admit as fast as the inventory tier can safely absorb, and back off when it strains. Use AIMD (additive-increase, multiplicative-decrease) to keep the DB near a safe utilization target.

```
Base rate (open-loop start):
  admit_rate ≈ safe_inventory_throughput
  e.g., inventory sustains 5,000 holds/sec at p99 < 500ms → start near there.

Close the loop:
  ↓ throttle admission when:
     - inventory p99 latency > SLA (e.g., > 500ms)
     - DB connection-pool utilization > 80%
     - hold→book conversion dropping / hold error rate rising
  ↑ increase admission when:
     - p99 well under SLA AND pool utilization < 60% AND error rate low
     - queue depth large (drain faster while headroom exists)
```

| Signal | Threshold (illustrative) | Action |
|---|---|---|
| Inventory p99 latency | > 500 ms | Reduce admission rate |
| DB pool utilization | > 80% | Reduce admission rate |
| Hold error/timeout rate | > 1% | Reduce admission rate |
| p99 latency | < 250 ms and pool < 60% | Increase admission rate |

Bots must be filtered **at the gate, before the seat-selection layer** — once automated traffic reaches inventory, it is too late:

```
Bot mitigation stack at the waiting room:
  1. Identity friction: account age, verified email/phone, pre-registration
     ("verified fan"-style) to raise the cost of mass fake accounts.
  2. Bot detection: device fingerprinting, behavioral signals, CAPTCHA / proof-of-work
     on suspicious sessions.
  3. Rate limits per identity/IP/ASN + anomaly detection (thousands of positions from
     one ASN in one second → block/deprioritize).
  4. Signed, NON-ENUMERABLE tokens: no guessable sequential IDs to script against.
  5. Managed bot rules / WAF at the edge.
  6. Reserve/prioritize verified-human lanes; deprioritize unverified traffic.
```

**Illustrative admission math for the whiteboard:**
```
Inventory safely sustains 5,000 holds/sec.
Sale has 20,000 seats. If every admitted user takes ~2 seats and converts,
you need ~10,000 successful holds to sell out.
At 5,000 holds/sec that is ~2 seconds of pure inventory work — but you deliberately
admit over MINUTES (say 5,000 users/min) so the DB stays at ~60–70% utilization and
each user gets a calm, responsive checkout instead of a degraded one.
The queue's job is to stretch a 1-second stampede into a several-minute drip.
```

**Real-company story.** Ticketmaster's November 2022 Taylor Swift "Eras Tour" presale is the textbook public failure. Per Ticketmaster's own public statement, the sale drew an extraordinary request volume (their post cited billions of system requests that day, far above prior peaks — treat the exact figure as their reported number). The publicly reported and Senate-hearing-discussed lessons (January 2023 hearing): the virtual queue was under-provisioned for true demand, and bot / unverified automated traffic reached the seat-selection layer. The codified fixes map exactly to this section — **verify humans and filter bots at the gate, capacity-plan the queue for real demand, and never let unverified automated traffic touch inventory.** (Some specifics come from public statements and testimony; treat internal engineering details as reported rather than confirmed.)

---

## 8. Database Design: Schema, Sharding, and CQRS

### 🟢 Beginner — Blueprints vs Tonight's Guest List

A wedding venue has permanent **blueprints**: where the tables and chairs physically sit. That never changes. But for *each* wedding, there is a fresh **guest list / seating chart** saying who sits in which chair *tonight*. You do not scribble tonight's guests onto the permanent blueprint — you print a new seating chart per event, so last week's wedding and this week's do not clash.

A seat database works the same way: one set of tables describes the venue's physical layout (reused forever), and a separate per-event table records the status of each seat *for this show*.

---

### 🟡 Senior — The Schema: Venue-Time vs Event-Time

Two layers: **static physical layout** (venue-owned, reused across events) and **per-event seat state** (the write-path table).

```sql
CREATE TABLE venues (
  venue_id   BIGINT PRIMARY KEY,
  name       TEXT NOT NULL,
  city       TEXT NOT NULL
);
CREATE TABLE sections (
  section_id BIGINT PRIMARY KEY,
  venue_id   BIGINT NOT NULL REFERENCES venues(venue_id),
  name       TEXT NOT NULL               -- 'Floor', 'Balcony', 'Section B'
);
CREATE TABLE seat_rows (
  row_id     BIGINT PRIMARY KEY,
  section_id BIGINT NOT NULL REFERENCES sections(section_id),
  row_label  TEXT NOT NULL               -- 'Row 3'
);
CREATE TABLE seats (                      -- PHYSICAL seat (venue-time, reused per event)
  seat_id    BIGINT PRIMARY KEY,
  row_id     BIGINT NOT NULL REFERENCES seat_rows(row_id),
  seat_label TEXT NOT NULL,              -- 'A14'
  x_coord    INT, y_coord INT            -- for interactive map rendering
);
CREATE TABLE events (
  event_id   BIGINT PRIMARY KEY,
  venue_id   BIGINT NOT NULL REFERENCES venues(venue_id),
  artist     TEXT, starts_at TIMESTAMPTZ
);
CREATE TABLE event_seats (                -- PER-EVENT state (the write-path table)
  event_id    BIGINT NOT NULL REFERENCES events(event_id),
  seat_id     BIGINT NOT NULL REFERENCES seats(seat_id),
  status      SMALLINT NOT NULL,          -- 0=AVAILABLE 1=HELD 2=BOOKED
  hold_id     TEXT,                       -- current hold token (NULL if not held)
  booking_id  BIGINT,
  price_cents INT NOT NULL,
  version     INT NOT NULL DEFAULT 0,     -- optimistic-lock column
  fence_token BIGINT NOT NULL DEFAULT 0,  -- monotonic fence for confirmation (§4)
  PRIMARY KEY (event_id, seat_id)
);
```

Indexes for the seat-map query:
```sql
CREATE INDEX idx_event_seats_map ON event_seats (event_id, status);
-- Covering index so the map scan is index-only (no heap fetches):
CREATE INDEX idx_event_seats_cover
   ON event_seats (event_id, seat_id) INCLUDE (status, price_cents);
-- The composite PK (event_id, seat_id) already co-locates an event's seats
-- and makes the single-seat hold a fast point lookup.
```

Serving an 80,000-seat map under ~200ms: do **not** re-query the DB per viewer.

```
Layered read strategy:
  1. Covering index → index-only range scan (not 80K point lookups).
  2. Cache the rendered map in Redis per event, short TTL (seconds).
       key: seatmap:event:123 → compact seat→status payload.
  3. Serve MOST viewers from cache; on a change, push a DELTA (seat X → HELD) over
     WebSocket/SSE instead of re-sending 80K rows.
  4. Bitmap encoding: 2 bits/seat → 80,000 × 2 / 8 = 20,000 bytes ≈ 20 KB for the
     whole map, vs multi-MB of JSON. Cheap to ship and diff. (Illustrative arithmetic.)
```

Freshness is cosmetic here: the **hold always re-validates against the source of truth** (§5), so a one-second-stale map costs at most a wasted click, never an overbooking.

---

### 🔴 Architect — Sharding, the Hot Shard, and CQRS

**Shard key: `event_id`.** All seats for one event live on one shard, so a multi-seat order for that event is a *single-shard transaction* — you never need a distributed transaction to hold N seats in one order.

```
shard = hash(event_id) % num_shards
  → event 123's 80K seats all on shard 5.
  → A multi-seat hold within event 123 = single-shard transaction. 

What BREAKS with this shard key:
  1. HOT SHARD: one mega-event overloads its single shard while others idle.
     Fix: sub-shard THAT event by SECTION (event:123:sectionA on shard1, sectionB
     on shard2). Seats never cross sections, so the per-seat mutex is intact and load
     spreads. Front it with the virtual queue to cap the admitted rate.
  2. CROSS-EVENT queries ("all events at venue X tonight", "a user's bookings across
     events") now fan out across shards → scatter-gather. Serve those from a separate
     read model, not the sharded inventory DB.
```

| Shard key | Pro | Con |
|---|---|---|
| **event_id (recommended)** | Multi-seat order = single-shard txn; natural isolation | Hot shard for a mega-event |
| seat_id / hash(seat) | Even spread | Multi-seat order spans shards → distributed txn (avoid) |
| venue_id | Co-locates a venue's events | Popular venues become hot; uneven |

Browsing (search by artist/city/date) and booking (point read/write on seat rows) have opposite access patterns — this is textbook **CQRS**. Serve them from different stores and sync one way:

```
BROWSE / SEARCH store:                       BOOK / INVENTORY store:
  - Search engine (Elasticsearch/OpenSearch  - Relational DB (Postgres/MySQL/Spanner)
    or a read-optimized replica)               sharded by event_id
  - Full-text, faceted, geo search           - Strong consistency, atomic CAS, ACID
  - Denormalized, eventually consistent      - Source of truth for seat state

Sync (one-way):  Inventory DB --CDC / event stream (e.g., Debezium → Kafka)--> Search index
```

The search index is eventually consistent — an event can show "available" for a few seconds after it sells out — and that is acceptable *only because* the hold re-validates against the inventory source of truth.

**Real-company story.** Resale marketplaces (StubHub and peers) are a natural CQRS/two-store shape: a massive read-heavy browse/search surface (listings, prices, filters) sits in front of a smaller, correctness-critical transaction path where a specific listing is bought and must not be sold twice. The public-facing lesson generalizes to any high-read/low-write inventory: separate the store that answers "what's out there?" (eventual, cheap to scale) from the store that answers "did I get it?" (strong, guarded), and connect them with a one-way change stream.

---

## 9. Global Distribution, Dynamic Pricing, and Operations

### 🟢 Beginner — One Signup Sheet, Many Doors

Suppose a popular class has one paper signup sheet, but students can enter the building through the front, side, or back door. If you photocopy the sheet and put a copy at each door, two students at different doors can both write their name on "the last spot" — chaos. The safe rule: keep the *one real* signup sheet at a single door, and if you came in another door you walk over to that one door to sign up. Copies at the other doors are fine for *reading* ("is the class full?") but nobody signs the copies.

A globally distributed seat system works the same way: one region owns the authoritative inventory for a given show, and users everywhere read from local copies but write to that one owner.

---

### 🟡 Senior — Home-Region Authority and Price Locking

A seat is a **single global resource**, but users hit **different regional data centers**. You need one authority per seat despite geographic distribution.

| Approach | How it works | Consistency | Cost |
|---|---|---|---|
| **Single home region per event (recommended)** | Each event has an authoritative region; all writes route there, other regions get read replicas | Strong for writes (single writer) | Cross-region write latency for far users |
| Region-partitioned allocation | Pre-allocate seat blocks per region; each writes its own block locally | Strong within a block | Wasteful: one region sells out while another has unsold blocks |
| Globally-distributed strong DB | Spanner/CockroachDB with synchronous cross-region consensus | Strong globally | Highest write latency (cross-region quorum per write) |
| Multi-master + async | Each region writes locally, replicate async | Eventual → **double-booking risk** | Unacceptable for zero-overbooking |

```
Recommended: HOME-REGION authority.
  - The event's inventory has ONE authoritative region (near the venue/demand).
  - Browse/seat-map: served from local read replicas everywhere (eventual, fine).
  - Hold/confirm: routed to the home region → the per-seat mutex stays single-owner.
  - A New York user booking a London show pays a cross-region round trip on the WRITE
    only (illustratively tens of ms); all their READS are local and fast.
```

**Dynamic pricing** (airline-style, demand-driven) forces one new discipline: **lock the price at hold time.**

```
When price = f(demand, remaining inventory, time, tier) and changes in real time:
  1. Pricing service computes and publishes price updates (low-latency, cacheable).
  2. Seat-map read path: push price deltas alongside status deltas over the same
     WebSocket/SSE channel; use short cache TTLs.
  3. Hold step: SNAPSHOT price_cents into the hold record → the user pays what they
     were quoted, not a price that moved mid-checkout.
  4. Payment: charge the LOCKED (held) price; the confirm validates the charge equals
     the held snapshot (reject on drift).
  5. Audit: log every price change (consumer-trust and dispute handling).
```

---

### 🔴 Architect — Failover, Migrations, and the Incident Playbook

**Primary inventory DB fails mid-sale:**
```
Failover:
  1. Detect: replication/health monitor declares the primary down (bounded timeout).
  2. Promote: a synchronous (or lowest-lag) replica becomes primary (managed HA like
     Patroni / Aurora / Cloud SQL automates this in seconds-to-tens-of-seconds).
  3. Redirect: service discovery points writes at the new primary.
  4. Fence the old primary (STONITH) so it cannot accept writes → no split-brain.

In-flight state:
  - HOLDS live in Redis, not the failing DB → they SURVIVE. (Second reason to keep
    holds off the primary DB.)
  - Committed BOOKINGS: durable, present on the promoted replica up to its repl point.
  - Writes in the async-replication gap at crash may be LOST → reconciler + payment
    idempotency recover them (money ⇒ seat-or-refund).
```

**Named tradeoff — RPO vs latency (sync vs async replication).** Synchronous replication gives RPO ≈ 0 (no lost writes on failover) but adds write latency; async is fast but can lose the last few writes. For the payments-adjacent commit of bookings/payments, prefer **synchronous replication** and lean on idempotency + reconciliation for any residual gap.

**Zero-downtime migration on a billion-row table** — never `ALTER TABLE` a billion rows under one lock; use expand → backfill → flip → contract:
```
EXPAND:   add the new column NULLABLE, no table rewrite; new code writes both columns.
BACKFILL: update in throttled batches (e.g., 10K rows/batch with sleeps) to protect
          replication lag and live p99. Tools: gh-ost or pt-online-schema-change (MySQL);
          batched UPDATE + CREATE INDEX CONCURRENTLY (Postgres).
FLIP:     once backfill is verified, switch reads to the new column.
CONTRACT: stop writing the old column; drop it in a later deploy.
```

**Incident: a bad deploy marked seats CONFIRMED without charging** (discovered hours later):
```
1. STOP THE BLEEDING: roll back the deploy (feature flag / previous image).
2. QUANTIFY: SELECT bookings WHERE status=CONFIRMED AND no matching CAPTURED payment
   in the window → the exact blast radius (this is why bookings↔payments must be
   reconcilable by payment_ref).
3. REMEDIATE: collect payment with consent OR cancel+notify, by policy; if the event
   is imminent, honor and eat the cost (brand > short-term revenue). Reconcile inventory.
4. COMMUNICATE proactively (don't let users find out at the gate).
5. PREVENT: a CONTINUOUS RECONCILER that flags any CONFIRMED booking lacking a CAPTURED
   payment within minutes — so the next occurrence is caught in minutes, not hours.
```

The systemic fix is the same money ⇒ seat-or-refund invariant from §6, now enforced as *monitoring*, not just as code.

**Real-company story.** GitHub built and open-sourced **gh-ost** (2016) specifically to run online schema changes on very large MySQL tables without the write-blocking behavior of naive `ALTER TABLE` — it copies to a shadow table and cuts over atomically. It is real, widely used, and the canonical answer to "how do you migrate a billion-row hot table without downtime." Percona's `pt-online-schema-change` predates it and solves the same class of problem with triggers. Naming one of these (and *why* — avoid the long exclusive lock) is a strong operations signal.

---

## 10. Pattern Recognition — When and How

### 🟢 Beginner — Interview Signal Checklist

When you hear these phrases, these mechanisms should appear in your design:

| Interview signal | Response |
|---|---|
| "no double-booking" / "unique seat" | Atomic CAS per transition; DB confirmation is the arbiter |
| "hold the seat for 10 minutes" | Redis `SET NX PX` soft hold; TTL auto-release; DB for the booking |
| "flash sale" / "tickets on sale at 10am" | Virtual waiting room + closed-loop admission control |
| "don't charge twice" | Idempotency key + DB unique constraint + provider idempotency |
| "payment and booking must be consistent" | Saga with compensations; outbox + reconciler; money ⇒ seat-or-refund |
| "bots" / "scalpers" | Verify humans + bot detection AT THE GATE, before inventory |
| "global event, multiple regions" | Home-region write authority; local read replicas; no multi-master |
| "how do you scale the writes" | Shard by event_id; sub-shard hot event by section; queue caps admitted rate |

---

### 🟡 Senior — Decision Map and Differentiating Follow-Ups

```
Locking choice:
  Low/medium contention (most seats)        → optimistic version CAS or Redis NX
  One hot row (last GA ticket, pool counter)→ pessimistic SELECT FOR UPDATE / serialize
  Multi-minute user "think time" hold       → neither DB lock → Redis TTL hold
  Final HELD→BOOKED commit                  → optimistic CAS on hold_id (+ fence token)

Consistency choice:
  Browse / seat map      → AP, eventual, cache + deltas
  Hold / confirm / pay   → CP, strong, atomic CAS on source of truth

Fail-open vs fail-closed on the hold primitive:
  → fail CLOSED (a "try again" beats an overbooking incident); shrink the outage
    window with Sentinel/Cluster failover + a circuit breaker.
```

Follow-up questions that separate a senior answer:
```
1. "Where does the overbooking guarantee actually live?"
   → The DB confirmation CAS (UPDATE ... WHERE status='HELD' AND hold_id=?), fenced.
     Redis is an optimization, not the correctness layer.
2. "What happens if the payment times out (408)?"
   → Never blind-retry; re-send with the same idempotency key or query status.
3. "What's your shard key and what breaks with it?"
   → event_id; breaks cross-event queries and the single hot mega-event (sub-shard by section).
4. "How do you keep the seat map fast at 80K seats?"
   → Covering index + Redis cache + WebSocket delta push + 2-bit bitmap encoding.
```

---

### 🔴 Architect — Anti-Patterns to Name and Avoid

| Anti-pattern | Why it fails | Correct alternative |
|---|---|---|
| Check-then-set in two steps | The gap between read and write is the race → double-book | Single atomic CAS (`SET NX` / `UPDATE ... WHERE status=`) |
| `SELECT FOR UPDATE` held for the full 10-min checkout | Pins a DB connection + row lock per open cart → pool exhaustion | Redis TTL soft hold; DB lock only for the short commit |
| Overbooking guarantee living in Redis alone | GC pause / clock skew / lost lock → double-book | DB confirmation CAS + fencing token; Redis is the fast path |
| Optimistic locking on one hot counter | ~100% conflict rate → retry storm → throughput collapse | Pessimistic lock / serialized worker / atomic `DECR` |
| Payment charge inside the DB transaction | External call can't `ROLLBACK`; coordinator blocks | Saga + idempotency + outbox/reconciler |
| Blind-retry a charge on timeout | Double charge | Same idempotency key or status query |
| Direct DB access at on-sale (no queue) | Thundering herd → pool saturation → cascade (Ticketmaster 2022) | Virtual waiting room + closed-loop admission |
| Guessable sequential seat/hold IDs | Bots enumerate and hold everything (denial-of-inventory) | Random/opaque signed tokens; per-actor hold quotas |
| Multi-master async for global inventory | Concurrent regional writes → double-book | Home-region write authority; local read replicas |
| Naive `ALTER TABLE` on a huge live table | Long exclusive lock → sale outage | gh-ost / pt-osc / expand-backfill-flip-contract |
| Trusting a cached/stale count to authorize a hold | Stale read grants a taken seat | Cache the display; re-validate the hold against source of truth |
| No reconciler for stuck payments | Money captured with no seat, found hours later | Continuous money ⇒ seat-or-refund reconciler |

---

## Quick Recall Cheat Sheet

> Close this file. Answer from memory. Open if stuck.

| Concept | One-line recall |
|---|---|
| Hardest constraint | Zero overbooking under contention → uniqueness-under-concurrency, unlike a fungible cart |
| CAP positioning | Browse = AP (eventual, cached); hold/confirm/pay = CP (strong, atomic CAS) |
| Hold vs booking | Hold = Redis TTL soft state; booking = durable DB row; split for latency vs durability |
| Overbooking primitive | Atomic CAS per transition (`SET NX` / `UPDATE ... WHERE status=` / `version=`) — check-and-act in one op |
| Redis hold command | `SET event:..:seat NX PX 600000`; nil = already held; Lua compare-and-delete to release |
| Why Redis not DB for holds | TTL auto-release + no 10-min DB locks; holds are soft state you can afford to lose |
| Lazy expiry gotcha | Redis expiry lags the TTL (lazy + background sampler) → re-validate `expiresAt` at confirm |
| Redis crash | Holds lost → seats revert to AVAILABLE (no double-book); Sentinel/Cluster limit blast radius |
| Two-users-one-seat | Pessimistic `FOR UPDATE` / optimistic version CAS / Redis `NX` — exactly one writer wins |
| Optimistic vs pessimistic | Optimistic for low contention; pessimistic for one hot row; watch conflict rate (>~20% → switch) |
| Lost update fix | Write's `WHERE` must re-assert the read: `... WHERE status='AVAILABLE'`, check rowcount |
| RedLock debate | Kleppmann 2016 vs antirez: not a hard mutex under GC/clock skew → always fence at the resource |
| Fencing token | Monotonic number issued with the lock; the DB rejects a write carrying a stale token |
| Seat state machine | AVAILABLE→HELD→BOOKED only; illegal jumps (AVAILABLE→BOOKED) must be impossible |
| Confirmation CAS | `UPDATE ... WHERE status='HELD' AND hold_id=?` → rowcount 1 wins, 0 loses (the arbiter) |
| Expiry-vs-payment race | PENDING_PAYMENT + DB CAS decides ownership; late-but-charged loser gets auto-refund |
| ACID boundary | Seat flip + booking + local payment record in one txn; the external charge is a saga step |
| Idempotency key | Deterministic per order+hold; DB unique constraint + provider idempotency key (two layers) |
| Saga | hold / charge / confirm / issue, each with a compensation; orchestrated; idempotent steps |
| 408 timeout | Never blind-retry; re-send with same idempotency key or query status |
| Money invariant | Money captured ⇒ eventually a valid booking OR a refund; enforced by outbox + reconciler |
| Thundering herd | 150K arrivals saturate pool → retry storm → cascade (Ticketmaster Eras Tour, Nov 2022) |
| Virtual queue | Stateless edge gate; admits at inventory's safe rate; DB sees admitted, not arrival |
| Fair queue position | Atomic `INCR` or sorted-set by arrival; signed token so position can't be forged |
| Admission control | Closed loop (AIMD) on p99 / pool / error rate; keep DB ~60–70% utilization |
| Bot mitigation | Verify humans + bot detection AT THE GATE, before inventory (the 2022 lesson) |
| Scale inventory writes | Shard by event_id; sub-shard hot event by section; queue caps admitted rate |
| Schema split | Physical seats (venue-time) + `event_seats` status (event-time); PK `(event_id, seat_id)` |
| Seat map <200ms | Covering index + Redis cache + WebSocket delta push + 2-bit bitmap (~20KB/80K seats) |
| Shard key = event_id | Multi-seat order = single-shard txn; breaks cross-event queries + one hot mega-event |
| Browse vs book stores | Search engine (eventual) via CDC from inventory RDBMS (strong) — CQRS staleness OK |
| Global events | Home-region write authority; local read replicas; consistency over latency (no multi-master) |
| Dynamic pricing | Lock price AT HOLD; charge the held price; push price deltas; revenue vs price-consistency |
| Billion-row migration | Expand → backfill (batched) → flip → contract; gh-ost / pt-osc / CREATE INDEX CONCURRENTLY |
| DB failover | Promote replica, fence old (STONITH); holds survive in Redis; sync-repl for RPO ≈ 0 |
| Confirmed-without-pay incident | Roll back, quantify via reconcile, remediate, add money ⇒ seat-or-refund monitor |

---

## 🔁 Redundancy & Replication — how *this* system does it

> Expands this system's row in the [Redundancy & Replication use-case matrix](../../fundamentals/Use_Cases_for_Redundancy_and_Replication.md) · concept depth: [key-technologies-notes.md §12](../../key-technologies-notes.md) + [sharding-replication](../sharding-replication/). ⚠️ Tech names are illustrative — verify against primary sources.

- **Pattern (TicketMaster):** **synchronous** primary-replica DB pairs + **Raft** distributed reservation locks.
- **Mode:** synchronous — **RPO = 0**.
- **Why here:** a lost write = a double-booked seat = a real-world conflict, so the booking write waits for a replica *and* consensus before confirming. Correctness strictly over latency at the money moment.

---

## 🗄️ Caching Strategy — how *this* system does it

> Expands this system's row in the [Caching use-case matrix](../../fundamentals/Use_Cases_for_Caching.md) (row 17, TicketMaster) · concept depth: [key-technologies-notes.md §22](../../key-technologies-notes.md) + [distributed-caching](../distributed-caching/). ⚠️ Tech names are illustrative — verify against primary sources.

- **Layers:** CDN for the static event page → Redis holding the **seat-map read model**, the **TTL holds** (§2), and waiting-room tokens (§7) → the authoritative booking database.
- **Strategy:** **CQRS.** The seat map clients render is a cached **read model**, projected from the hold/booking event stream and eventually consistent by a few seconds. The **hold itself is not a cache** — a Redis `SET NX` with a TTL is the *authoritative* claim on that seat, which is why §2 and §3 treat it as a concurrency primitive rather than an optimization.
- **Invalidation:** the seat map is invalidated by the event stream as holds and bookings land. The **hold TTL is a business rule** (a 10-minute checkout window), so its expiry is a domain event that releases inventory — not a cache eviction you may tune for hit ratio. Confusing the two is how you end up with seats that are neither held nor sellable.
- **Why here:** the honest framing is *"the seat map may lie; the hold may not."* A stale map shows a seat that's already gone, the user clicks it, and they get a clean rejection at hold time — mildly annoying and entirely acceptable. **Overselling is prevented at the authoritative hold, never by cache freshness**, which is the same separation as the e-commerce stock badge and worth stating in both. The other half of this topic is a caching lesson by negation: the **thundering herd at drop time (§7)** is the one stampede that *no* cache can absorb, because the traffic is overwhelmingly **writes** competing for a scarce resource. That's why the answer is a **virtual waiting room** — admission control — rather than a bigger cache. Knowing when caching is the wrong tool is the senior signal here.

---

## 🔀 Proxies — how *this* system uses them

> Expands this system's row in the [Use Cases for Proxies](../../fundamentals/Use_Cases_for_Proxies.md) matrix · concept depth: [key-technologies-notes.md §4 API Gateway / §5 Load Balancer](../../key-technologies-notes.md) + [api-design](../api-design/). ⚠️ Tech names are illustrative — verify against primary sources.

- **Pattern:** **virtual waiting-room queue proxy** — the edge acts as a gatekeeper during a drop.
- **Layer:** L7 (HTTP gatekeeper).
- **How:** it holds millions of users in a virtual line and **releases a throttled stream** (e.g. 500 req/s) to checkout, shielding the primary DB from collapse; doubles as bot protection.
- **🧭 Recall:** *put everyone in a line and drip a safe rate through to checkout.*
