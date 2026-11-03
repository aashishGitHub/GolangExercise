# Phase 6 — Payment Saga: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

Theory: [deep-dive.md](../../deep-dive.md) section 6. Scope reminder: backend only, no checkout page (see [MILESTONES.md Phase 6](../../../MILESTONES.md)). Also, from the code and not from MILESTONES: no HTTP `Idempotency-Key` header handling, and the `idempotency_keys` table has no Go reader or writer.

## L1 Beginner

**Q1. What is a saga and why not one big database transaction?**
A saga is a series of small committed steps, each with an "undo". The payment charge is a call to an outside provider. A database `ROLLBACK` cannot undo it, so the charge cannot sit inside one transaction. Here the steps are `extend_hold`, `charge`, `confirm`, `issue_ticket`. See `RunSaga` in [internal/order/order.go](../../../internal/order/order.go).

**Q2. What is an idempotency key and what stops a double charge?**
It is a label that is identical on every retry of one payment. `payments.idempotency_key` is `UNIQUE`, and the saga inserts the payment row first with `ON CONFLICT DO NOTHING`. A second attempt finds the existing row and does not charge again. See `chargeIdempotencyKey` in [internal/order/order.go](../../../internal/order/order.go) and [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql).

**Q3. What does the reconciler do?**
Every 60 seconds (and on boot) it looks for payments stuck in PENDING or UNKNOWN for over 5 minutes, asks the provider what really happened, then finishes or fails the order. It also checks the two money rules. See [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go).

## L2 Junior

**Q4. Trace `POST /orders` from request to a TICKETED order.**
`ordersAPI.createOrder` ([internal/httpapi/orders.go](../../../internal/httpapi/orders.go)) → `order.Service.CreateOrder` (one transaction: order row, four saga step rows, `order.created` outbox row) → returns 202 → goroutine `RunSaga` → `inv.ExtendHold` → `InsertPayment` → `pay.Charge` → `UpdatePaymentStatus` → `inv.ConfirmSeats` → `issueTickets` → status TICKETED. The client polls `GET /orders/{id}`.
Try it:
```
go test -tags=integration ./internal/order/... -run TestCreateOrderAndRunSaga_HappyPath -v
```

**Q5. What happens when the provider reports UNKNOWN?**
`RunSaga` hits `case "UNKNOWN"` and returns nil. The order stays AUTHORIZING and nothing is charged again. The saga worker may re-run it, but `RunSaga` sees the payment is not PENDING and returns again. Only the reconciler resolves it, via `StatusOf`. See [internal/order/order.go](../../../internal/order/order.go) and [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go).

**Q6. What does `compensate` do, in order?**
Money is captured but `ConfirmSeats` failed. Step 1: `inv.BestAvailable` for the same quantity at no more than the price paid, then `ConfirmSeats` on those seats, then `ReallocateOrder`, TICKETED. Step 2, only if step 1 fails: `pay.Refund`, `InsertRefund(COMPLETED)`, COMPENSATED. See `compensate` in [internal/order/order.go](../../../internal/order/order.go).

**Q7. How does the fake provider model an ambiguous charge?**
With `AmbiguousRate`, `Charge` records the key in its `captured` map but returns `StatusUnknown`. `StatusOf(key)` then returns CAPTURED. That is the only way to test the reconciler honestly. `TestFakeProvider_AmbiguousChargeActuallyCapturesButReportsUnknown` asserts both halves. See [internal/payment/payment.go](../../../internal/payment/payment.go).
Try it:
```
go test ./internal/payment/... -v
```

## L3 Mid

**Q8. Why insert-first with a unique constraint, rather than "check, then charge"?**
Check-then-charge has a race: two requests both see "no payment" and both charge. A `UNIQUE` constraint makes the database pick one winner atomically. The loser reads the winner's row. The deep-dive calls the provider's own idempotency key a second layer. Here the fake provider mimics that layer only in part: once a key has captured, a replay returns `CAPTURED` instead of charging again. Failed and timeout results are not remembered, so a replay re-rolls and may differ. An ambiguous charge answers `UNKNOWN` first and `CAPTURED` on replay (see `FakeProvider.Charge`). See `InsertPayment` in [sqlc/queries.sql](../../../sqlc/queries.sql) and [deep-dive.md](../../deep-dive.md) section 6.

**Q9. Why try reallocation before refund?**
`script.md` is cited in the code comment: most buyers prefer a nearby seat to a refund. It keeps revenue and avoids refund fees and support load. Cost: the buyer gets different seats without being asked. The search is capped at the same max price (`reallocMaxMult = 1`), so they never pay more. See [internal/order/order.go](../../../internal/order/order.go).

**Q10. Why are refunds a separate table?**
So a captured payment stays queryable as CAPTURED, and the refund is audited independently. The second money invariant is "captured payment with no confirmed order and no completed refund". If a refund overwrote `payments.status`, that check could never find anything. The migration comment names this as fixed gap #11. See [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql).

**Q11. How is the saga tested, and what do the tests prove?**
Integration tests (`-tags=integration`, real Postgres and Redis). Proven: happy path ends TICKETED with 1 captured payment and a BOOKED seat. Hold expired before charge ends FAILED with 0 payment rows. Expiry after capture reallocates (0 refunds, different seat) or refunds when no seat exists (1 completed refund). The reconciler test proves an ambiguous charge stalls in AUTHORIZING and resolves to TICKETED. The invariant test runs 20 orders at `FailRate` 0.3 and finds 0 violations. Not proven: crash between steps, concurrent `RunSaga` on one order, HTTP-level behavior. See [internal/order/integration_test.go](../../../internal/order/integration_test.go) and [internal/reconcile/integration_test.go](../../../internal/reconcile/integration_test.go).
Try it:
```
go test -tags=integration ./internal/reconcile/... -v
```

## L4 Senior

**Q12. What failure modes do you see in the code as written?**
These come from reading the code. I have not reproduced them, so verify before relying on them.
(a) Any `ConfirmSeats` error, including a transient DB error, goes to `compensate`, which can reallocate or refund a customer whose seat was fine.
(b) If the process dies after `ConfirmSeats` commits but before status becomes CONFIRMED, a re-run reads fences as 0 (the hold id is cleared), `ConfirmSeats` fails, and `compensate` runs. That could give the buyer a second seat or a refund while the first stays booked.
(c) The inline goroutine and `saga-worker` can run `RunSaga` on the same order together. There is no row lock. The payment `UNIQUE` key and the seat CAS limit the damage, but order status writes are unguarded.
(d) If `pay.Refund` errors, `RunSaga` returns an error with no refund row and no retry limit.
See [internal/order/order.go](../../../internal/order/order.go).

**Q13. Does the reconciler work across processes?**
I am not certain, and tests do not cover it. `FakeProvider.captured` is an in-memory map. [internal/wiring/wiring.go](../../../internal/wiring/wiring.go) (server), [cmd/saga-worker/main.go](../../../cmd/saga-worker/main.go) and [cmd/reconciler/main.go](../../../cmd/reconciler/main.go) each build their own. So a reconciler process would not see a charge the server process "captured", and `StatusOf` would answer FAILED. The integration tests pass because they share one provider. A real provider keeps this state server-side, so this is a limit of the fake. Also, `reconcile.go`'s comment promises to release the hold on the FAILED branch; the code does not.

**Q14. What breaks at 100x load?**
Reasoned from the code, not measured. `createOrder` spawns one goroutine per order with no bound. The worker sweeps 50 orders per 5s and the reconciler 100 payments per 60s. A real provider needs rate limits, retries and a durable idempotency record. `FakeProvider.captured` is a plain map with no mutex that concurrent `Charge` calls can hit; I have not run `-race` on it.

**Q15. What is NOT solved?**
No checkout UI and no price-change re-confirm (MILESTONES says so). No HTTP `Idempotency-Key` handling: the table exists but nothing uses it. Duplicate-order-per-hold returns a generic 500, not 409. A declined payment fails the order but does not release the hold early. Ticket issuance is best-effort and does not roll back a TICKETED order (comment in `issueTickets`). The invariant check only alerts via a log line; it does not repair anything.
