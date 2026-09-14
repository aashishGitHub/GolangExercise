//go:build integration

//	Run: docker compose up -d postgres minio && migrate -path migrations up \
//	       && go test -tags=integration ./internal/ticketing/...
package ticketing

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/db"
	"ticketing/internal/storage"
)

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

const testTicketsBucket = "ticketing-tickets"

func newTestSetup(t *testing.T) (*pgxpool.Pool, db.Querier, *Service) {
	t.Helper()
	dsn := envOr("DATABASE_URL", "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable")
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	q := db.New(pool)

	s3, err := storage.New(context.Background(),
		envOr("S3_ENDPOINT", "http://localhost:9000"), envOr("AWS_REGION", "us-east-1"),
		envOr("S3_ACCESS_KEY", "localdevuser"), envOr("S3_SECRET_KEY", "localdevpassword"))
	if err != nil {
		t.Fatalf("s3 client: %v", err)
	}

	svc := New(q, s3, NewHMACSigner("integration-test-secret"), testTicketsBucket)
	return pool, q, svc
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// seedConfirmedOrder creates a real event, seats, a real orders row, and
// marks the seats BOOKED with booking_id = that order — the state
// IssueTicketsForOrder expects (mirrors what internal/order's saga leaves
// behind at TICKETED, without running the whole saga just to get there).
func seedConfirmedOrder(t *testing.T, pool *pgxpool.Pool, seatCount int) (eventID int64, orderID uuid.UUID, seatIDs []int64) {
	t.Helper()
	ctx := context.Background()

	err := pool.QueryRow(ctx,
		`INSERT INTO events (venue_id, artist, title, starts_at, onsale_at)
		 VALUES (1, 'Ticketing Test', 'Ticketing Test', now() + interval '1 day', now())
		 RETURNING event_id`).Scan(&eventID)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// seat_id must be a real row in `seats` (event_seats.seat_id has a real
	// FK, unlike event_id which is a fresh row per test) — venue_id=1's
	// seed run produced ids 1..30000 (Phase 2). A time-based offset per
	// call avoids colliding with another concurrently-running test's
	// event_seats row for the same seat_id at a different event_id
	// (allowed by the schema, but pointless to risk when picking a fresh
	// range is free).
	base := 1 + (time.Now().UnixNano() % (30000 - int64(seatCount)))
	seatIDs = make([]int64, seatCount)
	for i := 0; i < seatCount; i++ {
		seatIDs[i] = base + int64(i)
	}

	orderID = uuid.New()
	holdID := uuid.New()
	mustExec(t, pool,
		`INSERT INTO orders (order_id, user_sub, event_id, hold_id, seat_ids, amount_cents, status)
		 VALUES ($1, 'ticketing-test-user', $2, $3, $4, 1000, 'TICKETED')`,
		orderID, eventID, holdID, seatIDs)
	// A real CAPTURED payment alongside the TICKETED order — required so
	// this test's fixture doesn't itself violate internal/reconcile's
	// global "TICKETED order must have a CAPTURED payment" invariant scan
	// while other packages' tests run against the same shared Postgres.
	// This was a REAL bug on the first pass here: a bare TICKETED order
	// with no payment row failed TestReconciler_* and
	// TestMoneyInvariant_HoldsAcrossManyOrders in internal/reconcile,
	// which scan orders/payments globally, not per-test.
	mustExec(t, pool,
		`INSERT INTO payments (payment_id, order_id, idempotency_key, amount_cents, status)
		 VALUES ($1, $2, $3, 1000, 'CAPTURED')`,
		uuid.New(), orderID, "ticketing-test-idempotency-"+orderID.String())

	for i, seatID := range seatIDs {
		mustExec(t, pool,
			`INSERT INTO event_seats (event_id, seat_id, seat_ordinal, status, sellable, price_cents, booking_id)
			 VALUES ($1, $2, $3, 2, true, 1000, $4)`,
			eventID, seatID, i, pgtype.UUID{Bytes: orderID, Valid: true})
	}

	// Clean up so this test's fixtures never linger for a LATER, unrelated
	// package's invariant scan either.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tickets WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM event_seats WHERE event_id = $1`, eventID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM payments WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM orders WHERE order_id = $1`, orderID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM events WHERE event_id = $1`, eventID)
	})

	return eventID, orderID, seatIDs
}

func TestIssueTicketsForOrder_RendersRealQRImages(t *testing.T) {
	pool, q, svc := newTestSetup(t)
	ctx := context.Background()
	eventID, orderID, seatIDs := seedConfirmedOrder(t, pool, 2)

	tickets, err := svc.IssueTicketsForOrder(ctx, eventID, orderID)
	if err != nil {
		t.Fatalf("IssueTicketsForOrder: %v", err)
	}
	if len(tickets) != len(seatIDs) {
		t.Fatalf("issued %d tickets, want %d", len(tickets), len(seatIDs))
	}

	for _, ticket := range tickets {
		got, err := q.GetTicket(ctx, ticket.TicketID)
		if err != nil {
			t.Fatalf("GetTicket: %v", err)
		}
		if got.QrS3Key == "" {
			t.Fatal("expected a non-empty qr_s3_key")
		}

		// "Not just 'a file exists'" — download the actual object and
		// decode it as a real PNG, proving real image bytes landed in
		// MinIO, not a stub.
		obj, err := svc.s3.PresignedGetURL(ctx, testTicketsBucket, got.QrS3Key, 60_000_000_000)
		if err != nil {
			t.Fatalf("presign: %v", err)
		}
		body := fetchURL(t, obj)
		img, err := png.Decode(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("decode PNG for ticket %s: %v (got %d bytes)", ticket.TicketID, err, len(body))
		}
		if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
			t.Fatalf("decoded QR image has zero dimensions: %v", img.Bounds())
		}
	}
}

func TestRedeem_HappyPathThenDuplicateRejected(t *testing.T) {
	pool, _, svc := newTestSetup(t)
	ctx := context.Background()
	eventID, orderID, _ := seedConfirmedOrder(t, pool, 1)

	tickets, err := svc.IssueTicketsForOrder(ctx, eventID, orderID)
	if err != nil {
		t.Fatalf("IssueTicketsForOrder: %v", err)
	}
	token, err := encodeQR(svc.signer, qrPayload{
		TicketID: tickets[0].TicketID, OrderID: orderID, EventID: eventID, SeatID: tickets[0].SeatID,
	})
	if err != nil {
		t.Fatalf("encodeQR: %v", err)
	}

	if err := svc.Redeem(ctx, token); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	err = svc.Redeem(ctx, token)
	if err == nil {
		t.Fatal("second redeem of the same ticket should fail")
	}
	if err.Error() != ErrAlreadyRedeemed.Error() {
		t.Fatalf("second redeem error = %v, want ErrAlreadyRedeemed", err)
	}
}

func TestRedeem_TamperedTokenRejected(t *testing.T) {
	pool, _, svc := newTestSetup(t)
	ctx := context.Background()
	eventID, orderID, _ := seedConfirmedOrder(t, pool, 1)

	tickets, err := svc.IssueTicketsForOrder(ctx, eventID, orderID)
	if err != nil {
		t.Fatalf("IssueTicketsForOrder: %v", err)
	}
	token, err := encodeQR(svc.signer, qrPayload{
		TicketID: tickets[0].TicketID, OrderID: orderID, EventID: eventID, SeatID: tickets[0].SeatID,
	})
	if err != nil {
		t.Fatalf("encodeQR: %v", err)
	}
	tampered := token[:len(token)-3] + "XXX"
	if err := svc.Redeem(ctx, tampered); err != ErrBadSignature && err != ErrMalformed {
		t.Fatalf("Redeem(tampered) = %v, want ErrBadSignature or ErrMalformed", err)
	}
}

func fetchURL(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}
