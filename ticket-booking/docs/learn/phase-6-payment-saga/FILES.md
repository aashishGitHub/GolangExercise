# Phase 6 — Payment Saga: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

> Note: Phase 6 is backend only. No checkout page exists, and no `web/src` file calls `/orders`. The HTTP `Idempotency-Key` header is not implemented; table `idempotency_keys` is created but unused by any Go code.

> Note: the doc comments in [internal/order/order.go](../../../internal/order/order.go) and [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql) draw states `CAPTURED`, `COMPENSATING`, `REALLOCATED`, `REFUNDING` on the order. The code never sets those on `orders.status`. It sets PENDING, AUTHORIZING, CONFIRMING, CONFIRMED, TICKETED, COMPENSATED, FAILED. Code wins. CAPTURED is a payment status only.

## [internal/order/order.go](../../../internal/order/order.go)
- **What it does**: The saga orchestrator. `CreateOrder` records the intent. `RunSaga` drives it to a final or stalled state. `compensate` handles "paid but cannot confirm".
- **Control flow**:
  1. `httpapi.ordersAPI.createOrder` → `Service.CreateOrder`: `q.GetSeatsByHoldID` → owner check (`ErrHoldNotFound`, `ErrNotHoldOwner`) → sum `HoldPriceCents` → `pool.Begin` → `qtx.CreateOrder` + four `qtx.CreateSagaStep` + `events.Publish("order.created")` → commit.
  2. The handler then starts a goroutine calling `Service.RunSaga`. [cmd/saga-worker/main.go](../../../cmd/saga-worker/main.go) and [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go) call it too.
  3. `RunSaga`: `q.GetOrder` → return if status is not PENDING, AUTHORIZING or CONFIRMING.
  4. If PENDING: status → AUTHORIZING, `inv.ExtendHold` (180s). On error: `markStep(FAILED)`, `fail("hold_expired")`, stop. No payment row exists.
  5. `chargeIdempotencyKey` → `q.InsertPayment` (ON CONFLICT DO NOTHING). If no row returned, `q.GetPaymentByIdempotencyKey`.
  6. If payment is PENDING: `pay.Charge` → `q.UpdatePaymentStatus`.
  7. Switch on payment status. FAILED: `fail("payment_declined")`. UNKNOWN: return, leave in AUTHORIZING. CAPTURED: continue.
  8. status → CONFIRMING → `currentFences` → `inv.ConfirmSeats`. Success: CONFIRMED → `issueTickets` (Phase 9, nil-safe) → TICKETED.
  9. Any `ConfirmSeats` error → `compensate`: `inv.BestAvailable` (same max price) → `inv.ConfirmSeats` on new seats → `q.ReallocateOrder` → TICKETED. If that fails: `pay.Refund` → `q.InsertRefund(COMPLETED)` → COMPENSATED.
- **Data flow**: hold id + user sub in → rows in `orders`, `order_saga_steps`, `payments`, maybe `refunds`; `event_seats` flipped to BOOKED by inventory; outbox event `order.created`. Money state lives in `payments.status`, seat state in `event_seats`.
- **How it is tested**: [internal/order/integration_test.go](../../../internal/order/integration_test.go), build tag `integration`, real Postgres and Redis. `TestCreateOrderAndRunSaga_HappyPath`, `TestRunSaga_HoldExpiredBeforeCharge_NoMoneyMoved`, `TestRunSaga_ExpiryAfterCapture_ReallocatesSuccessfully`, `TestRunSaga_ExpiryAfterCapture_RefundsWhenNoSeatsLeft`. The two "after capture" tests charge outside `RunSaga` (helper `chargeOrderDirectly`), then expire the hold with raw SQL. No test for crash-between-steps and none for two concurrent `RunSaga` calls on one order.
- **Read next**: [internal/payment/payment.go](../../../internal/payment/payment.go).

## [internal/payment/payment.go](../../../internal/payment/payment.go)
- **What it does**: `Provider` interface (`Charge`, `Refund`) and `FakeProvider` with `FailRate`, `TimeoutRate`, `AmbiguousRate`. `StatusOf` plays "ask the provider".
- **Control flow**: `order.Service.pay.Charge` → check `captured[key]` (replay returns CAPTURED) → `roll()` against FailRate → AmbiguousRate (sets `captured[key]=true` but returns UNKNOWN) → TimeoutRate (returns UNKNOWN, not captured) → else capture. `reconcile.Service` calls `StatusOf`.
- **Data flow**: idempotency key + amount in → `ChargeResult{ProviderRef, Status}`. State is an in-memory map `captured`, per process. It is not persisted and I see no mutex around it.
- **How it is tested**: [internal/payment/payment_test.go](../../../internal/payment/payment_test.go), plain unit tests (no build tag): `TestFakeProvider_HappyPath`, `TestFakeProvider_ChargeIsIdempotentOnSameKey`, `TestFakeProvider_AlwaysFails`, `TestFakeProvider_AmbiguousChargeActuallyCapturesButReportsUnknown`, `TestFakeProvider_TimeoutNeverCaptures`, `TestFakeProvider_Refund`.
- **Read next**: [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go).

## [internal/reconcile/reconcile.go](../../../internal/reconcile/reconcile.go)
- **What it does**: `RunOnce` resolves stuck payments. `CheckInvariant` counts violations of the two money rules.
- **Control flow**:
  1. [cmd/reconciler/main.go](../../../cmd/reconciler/main.go) calls `tick` on boot and every 60s → `rec.RunOnce` then `rec.CheckInvariant`.
  2. `RunOnce`: `q.ListStuckPayments` (PENDING or UNKNOWN, older than `stuckAfter` = 5 min, limit 100).
  3. Per payment, `checker.StatusOf(idempotencyKey)`. CAPTURED: `UpdatePaymentStatus` → `sagas.RunSaga`. FAILED: `UpdatePaymentStatus(FAILED)` + `FailOrder("payment_declined")`. Otherwise: leave it.
  4. `CheckInvariant`: `q.FindOrdersMissingPayment` and `q.FindCapturedPaymentsMissingOrderOrRefund`. `cmd/reconciler` logs "MONEY INVARIANT VIOLATED" if either count is above zero.
- **Data flow**: stuck `payments` rows + provider answer → payment status updated → saga resumed or order failed. Invariant: two SELECTs in, two counts out. Nothing is auto-fixed by the invariant check.
- **How it is tested**: [internal/reconcile/integration_test.go](../../../internal/reconcile/integration_test.go), build tag `integration`: `TestReconciler_ResolvesAmbiguousPayment` (uses `UPDATE payments SET created_at = now() - interval '10 minutes'` to skip the 5-minute wait) and `TestMoneyInvariant_HoldsAcrossManyOrders` (20 orders, `FailRate` 0.3). Both share ONE `FakeProvider` instance between saga and reconciler.
- **Read next**: [cmd/saga-worker/main.go](../../../cmd/saga-worker/main.go).

> Note: the package comment says the not-captured branch will "fail the order and release the hold", and that either branch "emits an outbox event via order.RunSaga's own Publish calls". In code the FAILED branch calls no `ReleaseHold` and no `events.Publish`; `RunSaga` itself publishes nothing (only `CreateOrder` publishes `order.created`, and `ConfirmSeats` publishes `seat.booked`). The hold is freed later by expiry or the reaper.

## [cmd/saga-worker/main.go](../../../cmd/saga-worker/main.go)
- **What it does**: Catch-up sweep. Resumes orders a crash left mid-flight.
- **Control flow**: `config.Load` → pool, `holdlock`, `inventory.New`, `payment.NewFakeProvider` with config rates → `order.New` → 5s ticker → `q.ListStuckOrders(StuckAfterSeconds: 30, RowLimit: 50)` → `resumeOrder` → `orders.RunSaga`.
- **Data flow**: orders with status PENDING, AUTHORIZING or CONFIRMING and old `updated_at` → `RunSaga` re-reads state and continues.
- **How it is tested**: not tested (thin `main`). The Lambda twin is [cmd/saga-worker-lambda/main.go](../../../cmd/saga-worker-lambda/main.go).
- **Read next**: [internal/httpapi/orders.go](../../../internal/httpapi/orders.go).

## [internal/httpapi/orders.go](../../../internal/httpapi/orders.go)
- **What it does**: `POST /orders` and `GET /orders/{orderID}`, both behind auth (see `NewRouter` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)).
- **Control flow**: `createOrder`: auth claims → decode `{holdId}` → `order.CreateOrder` → map `ErrHoldNotFound` to 410, `ErrNotHoldOwner` to 403, anything else to 500 → `go RunSaga(context.WithoutCancel(ctx), orderID)` → 202 with `pollAfterMs: 500`. `getOrder`: parse id → `q.GetOrder` → 404 if not the owner (hides existence).
- **Data flow**: JSON in → order row → background saga. Output is the order's status, seat ids, amount, `reallocated`, `failureCode`. A duplicate order for one hold hits `orders.hold_id UNIQUE` and surfaces as a generic 500 (MILESTONES calls this a rough edge, not a bug).
- **How it is tested**: no `httpapi` test file. Verified live with curl per MILESTONES Phase 6.
- **Read next**: [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql).

## [migrations/000006_orders_payments.up.sql](../../../migrations/000006_orders_payments.up.sql)
- **What it does**: Creates `orders`, `payments`, `refunds`, `order_saga_steps`, `idempotency_keys`.
- **Control flow**: applied by `make migrate`. Key constraints: `orders UNIQUE (hold_id)`, `payments UNIQUE (idempotency_key)`, partial index `payments_stuck_idx` on PENDING/UNKNOWN.
- **Data flow**: `RunSaga` writes `payments` insert-first. The reconciler's `ListStuckPayments` reads through `payments_stuck_idx`. The invariant queries join `orders`, `payments`, `refunds`.
- **How it is tested**: indirectly by the order and reconcile integration tests.
- **Read next**: [Phase 7 FILES](../phase-7-realtime-ws/FILES.md).
