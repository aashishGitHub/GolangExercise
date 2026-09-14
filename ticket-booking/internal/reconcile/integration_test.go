//go:build integration

//	Run: docker compose up -d postgres redis && migrate -path migrations up \
//	       && go test -tags=integration ./internal/reconcile/...
package reconcile

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/order"
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

func newTestSetup(t *testing.T, pay *payment.FakeProvider) (*pgxpool.Pool, db.Querier, *inventory.Service, *order.Service) {
	t.Helper()
	pool := newTestPool(t)
	q := db.New(pool)
	lock := holdlock.New(envOr("REDIS_ADDR", "localhost:6379"))
	t.Cleanup(func() { _ = lock.Close() })
	inv := inventory.New(pool, q, lock, 10*time.Minute, 8)
	svc := order.New(pool, q, inv, pay)
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
		 VALUES ($1, 'Reconcile Test', 'Reconcile Test', now() + interval '1 day', now())
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

func TestReconciler_ResolvesAmbiguousPayment(t *testing.T) {
	pay := payment.NewFakeProvider()
	pay.AmbiguousRate = 1.0 // every charge reports UNKNOWN but actually captures
	pool, q, inv, svc := newTestSetup(t, pay)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(505)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "recon-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}
	orderID, err := svc.CreateOrder(ctx, hold.HoldID, "recon-test-user")
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := svc.RunSaga(ctx, orderID); err != nil {
		t.Fatalf("RunSaga: %v", err)
	}

	// The order should be stuck in AUTHORIZING — RunSaga refuses to guess
	// on an UNKNOWN charge.
	ord, err := q.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if ord.Status != "AUTHORIZING" {
		t.Fatalf("order status = %q, want AUTHORIZING (stuck on an ambiguous charge)", ord.Status)
	}

	// Make it look "stuck long enough" for the reconciler's window.
	mustExec(t, pool, `UPDATE payments SET created_at = now() - interval '10 minutes' WHERE order_id=$1`, orderID)

	rec := New(q, pay, pay, svc)
	resolved, err := rec.RunOnce(ctx)
	if err != nil {
		t.Fatalf("reconciler RunOnce: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("resolved = %d, want 1", resolved)
	}

	ord, err = q.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("get order after reconcile: %v", err)
	}
	if ord.Status != "TICKETED" {
		t.Fatalf("order status after reconcile = %q, want TICKETED", ord.Status)
	}

	seatWithNoMoney, moneyWithNoSeat, err := rec.CheckInvariant(ctx)
	if err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	if seatWithNoMoney != 0 {
		t.Errorf("seatWithNoMoney = %d, want 0", seatWithNoMoney)
	}
	if moneyWithNoSeat != 0 {
		t.Errorf("moneyWithNoSeat = %d, want 0", moneyWithNoSeat)
	}
}

func TestMoneyInvariant_HoldsAcrossManyOrders(t *testing.T) {
	pay := payment.NewFakeProvider()
	pay.FailRate = 0.3
	pool, q, inv, svc := newTestSetup(t, pay)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatIDs := make([]int64, 20)
	for i := range seatIDs {
		seatIDs[i] = int64(600 + i)
	}
	seedTestEventSeats(t, pool, eventID, seatIDs)

	for _, seatID := range seatIDs {
		hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "invariant-test-user")
		if err != nil {
			t.Fatalf("AcquireHold seat %d: %v", seatID, err)
		}
		orderID, err := svc.CreateOrder(ctx, hold.HoldID, "invariant-test-user")
		if err != nil {
			t.Fatalf("CreateOrder seat %d: %v", seatID, err)
		}
		if err := svc.RunSaga(ctx, orderID); err != nil {
			t.Fatalf("RunSaga seat %d: %v", seatID, err)
		}
	}

	rec := New(q, pay, pay, svc)
	seatWithNoMoney, moneyWithNoSeat, err := rec.CheckInvariant(ctx)
	if err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	if seatWithNoMoney != 0 {
		t.Errorf("seatWithNoMoney = %d, want 0 (across 20 orders, 30%% decline rate)", seatWithNoMoney)
	}
	if moneyWithNoSeat != 0 {
		t.Errorf("moneyWithNoSeat = %d, want 0", moneyWithNoSeat)
	}
}
