# Phase 6 — Payment Saga: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Scope: [MILESTONES.md, Phase 6](../../../MILESTONES.md). Theory: [deep-dive.md](../../deep-dive.md) section 6 (ACID boundary, idempotency, saga, money invariant).

> What was built: the backend only. That is the order tables, the fake payment provider, the saga orchestrator, the saga worker, the reconciler, and `POST /orders` plus `GET /orders/{id}`.
> What was NOT built: the resumable checkout React page. No file under `web/src` calls the order routes. Also not built: an HTTP `Idempotency-Key` header. The table `idempotency_keys` exists in [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql), but no Go code reads or writes it (I grepped for it).

1. **order** — An order is the buyer's request to turn a hold into a paid booking. One hold can have only one order (`orders.hold_id` is `UNIQUE`). Analogy: a ticket stub that links your coat on the seat to your payment. In this project: [internal/order/order.go](../../../internal/order/order.go) (`CreateOrder`).

2. **saga** — A long job made of small steps, where each step is its own database commit. There is no giant all-or-nothing transaction, because a payment provider cannot join your database transaction. Analogy: renovating a kitchen in paid stages instead of paying everything up front. See [deep-dive.md](../../deep-dive.md) section 6. In this project: `RunSaga` in [internal/order/order.go](../../../internal/order/order.go).

3. **orchestration** — One coordinator (here `RunSaga`) calls each step in order and decides what to do on failure. The other style, choreography, lets services react to each other's events with no boss. Analogy: a conductor versus a jam session. In this project: [internal/order/order.go](../../../internal/order/order.go).

4. **saga steps** — This saga has four: `extend_hold`, `charge`, `confirm`, `issue_ticket`. Each gets a row in `order_saga_steps` with a state (PENDING, DONE, COMPENSATED, FAILED). Analogy: a checklist pinned to the job. In this project: `CreateOrder` in [internal/order/order.go](../../../internal/order/order.go) and the table in [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql).

5. **order status** — The order's `status` column is the saga's state machine. The statuses the code really sets are PENDING, AUTHORIZING, CONFIRMING, CONFIRMED, TICKETED, COMPENSATED and FAILED. Analogy: the stage markers on a parcel tracker. In this project: [internal/order/order.go](../../../internal/order/order.go).

6. **extend hold** — Before any money moves, the saga pushes the hold's deadline out by 180 seconds (`extendBuffer`). If the hold is already gone, the saga stops and nothing is charged. Analogy: asking the restaurant to keep your table while you finish paying. In this project: `ExtendHold` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go), called from `RunSaga`.

7. **idempotency key** — A label that is the same for every retry of the same payment. The payment table has a `UNIQUE` constraint on it, so a second attempt cannot create a second charge. Here it is a SHA-256 hash of user, order and hold. Analogy: a cheque number; the bank ignores a second cheque with the same number. In this project: `chargeIdempotencyKey` in [internal/order/order.go](../../../internal/order/order.go).

8. **insert-first** — Write the payment row before calling the provider, using `INSERT ... ON CONFLICT DO NOTHING`. Whoever inserts first owns the charge. A second caller finds the row and does not charge again. Analogy: writing your name on the sign-up sheet before you walk in. In this project: the `InsertPayment` query in [sqlc/queries.sql](../../../sqlc/queries.sql).

9. **payment provider (fake)** — The project's stand-in for Stripe-like services. You can make it fail, time out, or be ambiguous with `FailRate`, `TimeoutRate`, `AmbiguousRate`. Its `Charge` replays a captured key as `CAPTURED` without charging again; failed and timeout results are not remembered. Analogy: a driving simulator with a "make it rain" button. In this project: [internal/payment/payment.go](../../../internal/payment/payment.go).

10. **ambiguous outcome (UNKNOWN)** — When a charge times out you do not know if the money moved. The saga records UNKNOWN and stops. It never guesses and never charges again with a new key. Analogy: you hand over cash and the shop's lights go out; you do not pay a second time, you check the till. In this project: the `case "UNKNOWN"` branch in [internal/order/order.go](../../../internal/order/order.go).

11. **confirm** — The step that turns the held seat into a booked seat, using the same compare-and-swap (CAS: update only if the row is still in the state I expect) from Phase 3. It also checks the fence token and that the hold has not expired. Analogy: stamping the seat as sold only if your coat is still on it. In this project: `ConfirmSeats` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

12. **compensation** — If a later step fails after an earlier one succeeded, run an "undo". Here: money is captured but confirm fails. Undo means first try a different seat, and only refund if that fails. Analogy: if the hotel lost your room, they offer another room before giving the money back. In this project: `compensate` in [internal/order/order.go](../../../internal/order/order.go).

13. **reallocation** — Compensation option one. `BestAvailable` finds seats at no higher than the price paid, confirms them, and updates the order (`reallocated = true`). In this project: `compensate` in [internal/order/order.go](../../../internal/order/order.go).

14. **refund** — Compensation option two. A refund is its own table row (`refunds`), not a status change on the payment. That keeps the captured payment visible for audits. In this project: `InsertRefund` in [sqlc/queries.sql](../../../sqlc/queries.sql), and the table in [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql).

15. **reconciler** — A background checker that finds payments stuck in PENDING or UNKNOWN for more than 5 minutes (`stuckAfter`), asks the provider what really happened, and finishes the job. It runs on start and then every 60 seconds. Analogy: the accountant who matches the till to the receipts at closing time. In this project: [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go), run by [cmd/reconciler/main.go](../../../cmd/reconciler/main.go).

16. **money invariant** — A rule that must always hold: never a seat with no captured payment, and never captured money with neither a confirmed order nor a completed refund. Two SQL queries look for violations; both must return 0 rows. Analogy: the books must balance. In this project: `CheckInvariant` in [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go).

17. **202 Accepted and polling** — `POST /orders` answers `202` right away with the order id and `pollAfterMs: 500`. The saga runs in a background goroutine (a lightweight Go thread). The client then asks `GET /orders/{id}` until the status settles. Analogy: you get a pager at a restaurant. In this project: [internal/httpapi/orders.go](../../../internal/httpapi/orders.go).

18. **inline fast path plus catch-up sweep** — The saga normally runs right after `POST /orders`. A separate worker every 5 seconds picks up orders that a crash left half-done (status PENDING, AUTHORIZING or CONFIRMING, not updated for 30 seconds). Same shape as the Phase 5 outbox and reaper. Analogy: a shop assistant who serves you now, and a manager who checks the queue later. In this project: [cmd/saga-worker/main.go](../../../cmd/saga-worker/main.go).

19. **re-entrant step** — `RunSaga` can be called again and again on the same order. Each time it looks at the order's current status and the payment row before acting. That is why a crash and retry is safe. In this project: the first lines of `RunSaga` in [internal/order/order.go](../../../internal/order/order.go).

20. **order.created event** — `CreateOrder` writes the order, the four step rows, and an `order.created` outbox event in one transaction. This reuses the Phase 5 outbox. In this project: `CreateOrder` in [internal/order/order.go](../../../internal/order/order.go).
