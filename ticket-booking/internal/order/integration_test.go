//go:build integration

//	Run: docker compose up -d postgres redis && migrate -path migrations up \
//	       && go test -tags=integration ./internal/order/...
package order

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/payment"
)

const testVenueID = int64(1) // seeded by Phase 2's scripts/seed-venue run

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := envOr("DATABASE_URL", "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable")
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestSetup(t *testing.T, pay *payment.FakeProvider) (*pgxpool.Pool, db.Querier, *inventory.Service, *Service) {
	t.Helper()
	pool := newTestPool(t)
	q := db.New(pool)
	lock := holdlock.New(envOr("REDIS_ADDR", "localhost:6379"))
	t.Cleanup(func() { _ = lock.Close() })
	inv := inventory.New(pool, q, lock, 10*time.Minute, 8)
	svc := New(pool, q, inv, pay)
	return pool, q, inv, svc
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func seedTestEvent(t *testing.T, pool *pgxpool.Pool, venueID int64) int64 {
	t.Helper()
	var eventID int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (venue_id, artist, title, starts_at, onsale_at)
		 VALUES ($1, 'Order Test', 'Order Test', now() + interval '1 day', now())
		 RETURNING event_id`, venueID).Scan(&eventID)
	if err != nil {
		t.Fatalf("seed test event: %v", err)
	}
	return eventID
}

func seedTestEventSeats(t *testing.T, pool *pgxpool.Pool, eventID int64, seatIDs []int64) {
	t.Helper()
	for i, seatID := range seatIDs {
		mustExec(t, pool,
			`INSERT INTO event_seats (event_id, seat_id, seat_ordinal, status, sellable, price_cents)
			 VALUES ($1, $2, $3, 0, true, 1000)`, eventID, seatID, i)
	}
}

func TestCreateOrderAndRunSaga_HappyPath(t *testing.T) {
	pool, _, inv, svc := newTestSetup(t, payment.NewFakeProvider())
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(500)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "order-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}

	orderID, err := svc.CreateOrder(ctx, hold.HoldID, "order-test-user")
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.RunSaga(ctx, orderID); err != nil {
		t.Fatalf("RunSaga: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE order_id=$1`, orderID).Scan(&status); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if status != "TICKETED" {
		t.Fatalf("order status = %q, want TICKETED", status)
	}

	var seatStatus int16
	if err := pool.QueryRow(ctx, `SELECT status FROM event_seats WHERE event_id=$1 AND seat_id=$2`, eventID, seatID).Scan(&seatStatus); err != nil {
		t.Fatalf("query seat status: %v", err)
	}
	if seatStatus != 2 {
		t.Fatalf("seat status = %d, want 2 (BOOKED)", seatStatus)
	}

	var paymentCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id=$1 AND status='CAPTURED'`, orderID).Scan(&paymentCount); err != nil {
		t.Fatalf("query payment count: %v", err)
	}
	if paymentCount != 1 {
		t.Fatalf("captured payment count = %d, want 1", paymentCount)
	}
}

func TestRunSaga_HoldExpiredBeforeCharge_NoMoneyMoved(t *testing.T) {
	pool, q, inv, svc := newTestSetup(t, payment.NewFakeProvider())
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(501)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "order-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}
	orderID, err := svc.CreateOrder(ctx, hold.HoldID, "order-test-user")
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	// Force the hold to already be expired BEFORE RunSaga's extend_hold
	// step — must abort before any charge is attempted.
	mustExec(t, pool, `UPDATE event_seats SET hold_expires_at = now() - interval '1 minute' WHERE event_id=$1 AND seat_id=$2`, eventID, seatID)

	if err := svc.RunSaga(ctx, orderID); err != nil {
		t.Fatalf("RunSaga: %v", err)
	}

	ord, err := q.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if ord.Status != "FAILED" {
		t.Fatalf("order status = %q, want FAILED", ord.Status)
	}
	if ord.FailureCode.String != "hold_expired" {
		t.Fatalf("failure code = %q, want hold_expired", ord.FailureCode.String)
	}

	var paymentCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id=$1`, orderID).Scan(&paymentCount); err != nil {
		t.Fatalf("query payment count: %v", err)
	}
	if paymentCount != 0 {
		t.Fatalf("payment count = %d, want 0 — no money should have moved", paymentCount)
	}
}

func TestRunSaga_ExpiryAfterCapture_ReallocatesSuccessfully(t *testing.T) {
	pool, q, inv, svc := newTestSetup(t, payment.NewFakeProvider())
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(502)
	otherSeatID := int64(503) // a free seat for reallocation to land on
	seedTestEventSeats(t, pool, eventID, []int64{seatID, otherSeatID})

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "order-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}
	orderID, err := svc.CreateOrder(ctx, hold.HoldID, "order-test-user")
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	// Manually drive the saga up through "charge captured", THEN expire
	// the hold before letting confirm run — simulates money captured but
	// the hold lapsing in the gap (docs/plan.md race matrix row 6).
	if err := stepThroughChargeOnly(t, svc, ctx, orderID); err != nil {
		t.Fatalf("step through charge: %v", err)
	}
	mustExec(t, pool, `UPDATE event_seats SET hold_expires_at = now() - interval '1 minute' WHERE event_id=$1 AND seat_id=$2`, eventID, seatID)

	if err := svc.RunSaga(ctx, orderID); err != nil {
		t.Fatalf("RunSaga (compensation run): %v", err)
	}

	ord, err := q.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if ord.Status != "TICKETED" {
		t.Fatalf("order status = %q, want TICKETED (reallocation should have succeeded)", ord.Status)
	}
	if !ord.Reallocated {
		t.Fatal("order.Reallocated = false, want true")
	}
	if len(ord.SeatIds) != 1 || ord.SeatIds[0] == seatID {
		t.Fatalf("order seat_ids = %v, want a DIFFERENT seat than the original %d", ord.SeatIds, seatID)
	}

	var refundCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refunds WHERE payment_id IN (SELECT payment_id FROM payments WHERE order_id=$1)`, orderID).Scan(&refundCount); err != nil {
		t.Fatalf("query refund count: %v", err)
	}
	if refundCount != 0 {
		t.Fatalf("refund count = %d, want 0 — reallocation should have avoided a refund entirely", refundCount)
	}
}

func TestRunSaga_ExpiryAfterCapture_RefundsWhenNoSeatsLeft(t *testing.T) {
	pool, q, inv, svc := newTestSetup(t, payment.NewFakeProvider())
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(504)
	// Deliberately no other free seats at this price for this event — so
	// BestAvailable must fail and the saga falls through to refund.
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "order-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}
	orderID, err := svc.CreateOrder(ctx, hold.HoldID, "order-test-user")
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	if err := stepThroughChargeOnly(t, svc, ctx, orderID); err != nil {
		t.Fatalf("step through charge: %v", err)
	}
	mustExec(t, pool, `UPDATE event_seats SET hold_expires_at = now() - interval '1 minute' WHERE event_id=$1 AND seat_id=$2`, eventID, seatID)

	if err := svc.RunSaga(ctx, orderID); err != nil {
		t.Fatalf("RunSaga (compensation run): %v", err)
	}

	ord, err := q.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if ord.Status != "COMPENSATED" {
		t.Fatalf("order status = %q, want COMPENSATED (no seats to reallocate to)", ord.Status)
	}

	var refundCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refunds WHERE payment_id IN (SELECT payment_id FROM payments WHERE order_id=$1) AND status='COMPLETED'`, orderID).Scan(&refundCount); err != nil {
		t.Fatalf("query refund count: %v", err)
	}
	if refundCount != 1 {
		t.Fatalf("completed refund count = %d, want 1", refundCount)
	}
}

// stepThroughChargeOnly runs RunSaga once, which — for a completely fresh
// order against a still-valid hold — always proceeds all the way through
// confirm/issue_ticket in one call. To test "money captured, THEN the hold
// expires before confirm", we need charge to land while the hold is still
// valid but stop before this same call reaches confirm. Simplest reliable
// way: call the package-private charge step directly isn't exposed, so
// instead we exploit that RunSaga is idempotent per step — call it, then
// verify charge landed via the payments table, accepting that in the
// non-expired case this single call will ALSO confirm+ticket; the two
// expiry tests above therefore expire the hold in a small race window
// using a synchronous pre-check instead. See below for the actual
// mechanism used.
func stepThroughChargeOnly(t *testing.T, svc *Service, ctx context.Context, orderID uuid.UUID) error {
	t.Helper()
	// Because RunSaga runs synchronously to completion in one call when
	// nothing is expired, the only reliable way to land "captured, not yet
	// confirmed" is to charge OUTSIDE RunSaga using the same idempotency
	// scheme, then let RunSaga's own re-entrant charge step find it
	// already CAPTURED (the ON CONFLICT DO NOTHING replay path) and
	// proceed straight to confirm — which is exactly RunSaga's real
	// behavior on a resumed order, not a test-only shortcut.
	return chargeOrderDirectly(ctx, svc, orderID)
}

func chargeOrderDirectly(ctx context.Context, svc *Service, orderID uuid.UUID) error {
	ord, err := svc.q.GetOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	idempotencyKey := chargeIdempotencyKey(ord.UserSub, orderID, ord.HoldID)
	payRow, err := svc.q.InsertPayment(ctx, db.InsertPaymentParams{
		PaymentID: uuid.New(), OrderID: orderID, IdempotencyKey: idempotencyKey, AmountCents: ord.AmountCents,
	})
	if err != nil {
		return fmt.Errorf("insert payment: %w", err)
	}
	result, err := svc.pay.Charge(ctx, idempotencyKey, ord.AmountCents)
	if err != nil {
		return fmt.Errorf("charge: %w", err)
	}
	if err := svc.q.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
		PaymentID: payRow.PaymentID, Status: string(result.Status), ProviderRef: toPgText(result.ProviderRef),
	}); err != nil {
		return fmt.Errorf("update payment status: %w", err)
	}
	if result.Status != payment.StatusCaptured {
		return fmt.Errorf("test setup expected a captured charge, got %v", result.Status)
	}
	_ = svc.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: orderID, Status: "AUTHORIZING"})
	return nil
}
