//go:build integration

//	Run: docker compose up -d postgres redis && migrate -path migrations up \
//	       && go test -tags=integration ./internal/inventory/...
//
// These are the tests that prove docs/plan.md's actual thesis — "a seat is
// never sold twice, under a stampede" — by racing, not by asserting in
// prose. They need a real Postgres (the arbiter) and a real Redis (the
// contention filter), and a venue already seeded by scripts/seed-venue
// (venue_id=1, from Phase 2's own verification run — any venue with at
// least a few hundred real seat_id rows works; these tests create their
// own fresh event_id per (sub)test so they never collide with each other
// or with Phase 2's data).
package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/db"
	"ticketing/internal/holdlock"
)

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

func newRealService(t *testing.T, pool *pgxpool.Pool, holdTTL time.Duration) *Service {
	t.Helper()
	lock := holdlock.New(envOr("REDIS_ADDR", "localhost:6379"))
	t.Cleanup(func() { _ = lock.Close() })
	return New(pool, db.New(pool), lock, holdTTL, 8)
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// seedTestEvent creates a fresh event against an existing venue so each
// (sub)test gets an isolated event_id — no cross-test interference even
// when subtests run the SAME seat_id.
func seedTestEvent(t *testing.T, pool *pgxpool.Pool, venueID int64) int64 {
	t.Helper()
	var eventID int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (venue_id, artist, title, starts_at, onsale_at)
		 VALUES ($1, 'Race Test', 'Race Test', now() + interval '1 day', now())
		 RETURNING event_id`, venueID).Scan(&eventID)
	if err != nil {
		t.Fatalf("seed test event: %v", err)
	}
	return eventID
}

// seedTestEventSeats inserts fresh AVAILABLE event_seats rows reusing real
// seat_id values from an already-seeded venue (satisfies the FK without
// needing a full scripts/seed-venue run per test).
func seedTestEventSeats(t *testing.T, pool *pgxpool.Pool, eventID int64, seatIDs []int64) {
	t.Helper()
	for i, seatID := range seatIDs {
		mustExec(t, pool,
			`INSERT INTO event_seats (event_id, seat_id, seat_ordinal, status, sellable, price_cents)
			 VALUES ($1, $2, $3, 0, true, 1000)`, eventID, seatID, i)
	}
}

const testVenueID = int64(1) // seeded by Phase 2's `go run ./scripts/seed-venue` verification run

// TestAcquireHold_ExactlyOneWinnerUnderRace is the project's thesis,
// proven not asserted: 200 goroutines released from one barrier, racing
// for ONE seat. Repeated 20x as subtests — "a race test that passes once
// proves nothing."
func TestAcquireHold_ExactlyOneWinnerUnderRace(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)

	for run := 0; run < 20; run++ {
		t.Run(fmt.Sprintf("run_%02d", run), func(t *testing.T) {
			t.Parallel()
			eventID := seedTestEvent(t, pool, testVenueID)
			seatID := int64(100)
			seedTestEventSeats(t, pool, eventID, []int64{seatID})

			const n = 200
			var wins, losses, unexpected int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(n)
			for g := 0; g < n; g++ {
				go func(g int) {
					defer wg.Done()
					<-start
					_, err := svc.AcquireHold(context.Background(), eventID, []int64{seatID}, fmt.Sprintf("user-%d", g))
					switch {
					case err == nil:
						atomic.AddInt64(&wins, 1)
					case errors.Is(err, ErrSeatTaken):
						atomic.AddInt64(&losses, 1)
					default:
						t.Logf("unexpected error from goroutine %d: %v", g, err)
						atomic.AddInt64(&unexpected, 1)
					}
				}(g)
			}
			close(start)
			wg.Wait()

			if unexpected != 0 {
				t.Fatalf("unexpected (non-ErrSeatTaken) errors: %d", unexpected)
			}
			if wins != 1 {
				t.Fatalf("wins = %d, want exactly 1", wins)
			}
			if losses != n-1 {
				t.Fatalf("losses = %d, want %d", losses, n-1)
			}

			// Verify directly against the DB — not the service's own
			// return values, which could be self-consistent and still wrong.
			assertExactlyOneHeld(t, pool, eventID, seatID)
		})
	}
}

// TestAcquireHold_ExactlyOneWinnerUnderRace_WithRedisFlushed is the test
// that actually proves Postgres, not Redis, is the arbiter: a background
// goroutine FLUSHALLs every 5ms throughout the whole race, so the Redis
// contention filter is useless — the overwhelming majority of the 200
// goroutines reach the Postgres CAS simultaneously instead of being
// fast-rejected at Redis. Still exactly one winner.
func TestAcquireHold_ExactlyOneWinnerUnderRace_WithRedisFlushed(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)
	rdb := redis.NewClient(&redis.Options{Addr: envOr("REDIS_ADDR", "localhost:6379")})
	defer rdb.Close()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(101)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	stop := make(chan struct{})
	var flushWG sync.WaitGroup
	flushWG.Add(1)
	go func() {
		defer flushWG.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = rdb.FlushAll(context.Background()).Err()
			}
		}
	}()

	const n = 200
	var wins, losses int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			_, err := svc.AcquireHold(context.Background(), eventID, []int64{seatID}, fmt.Sprintf("user-%d", g))
			if err == nil {
				atomic.AddInt64(&wins, 1)
			} else if errors.Is(err, ErrSeatTaken) {
				atomic.AddInt64(&losses, 1)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	close(stop)
	flushWG.Wait()

	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1 — Postgres must remain the sole arbiter even with Redis flushed continuously", wins)
	}
	if losses != n-1 {
		t.Fatalf("losses = %d, want %d", losses, n-1)
	}
	assertExactlyOneHeld(t, pool, eventID, seatID)
}

func assertExactlyOneHeld(t *testing.T, pool *pgxpool.Pool, eventID, seatID int64) {
	t.Helper()
	ctx := context.Background()

	var heldCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_seats WHERE event_id=$1 AND seat_id=$2 AND status=1`,
		eventID, seatID).Scan(&heldCount); err != nil {
		t.Fatalf("query held count: %v", err)
	}
	if heldCount != 1 {
		t.Fatalf("event_seats status=1 count = %d, want 1", heldCount)
	}

	var distinctHolds int
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT hold_id) FROM event_seats WHERE event_id=$1 AND seat_id=$2 AND status=1`,
		eventID, seatID).Scan(&distinctHolds); err != nil {
		t.Fatalf("query distinct hold_id: %v", err)
	}
	if distinctHolds != 1 {
		t.Fatalf("distinct hold_id count = %d, want 1", distinctHolds)
	}

	var acquiredAudits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM holds_audit WHERE event_id=$1 AND seat_id=$2 AND outcome='ACQUIRED'`,
		eventID, seatID).Scan(&acquiredAudits); err != nil {
		t.Fatalf("query holds_audit ACQUIRED count: %v", err)
	}
	if acquiredAudits != 1 {
		t.Fatalf("holds_audit ACQUIRED rows = %d, want 1", acquiredAudits)
	}
}

// TestConfirm_ExactlyOneWinner: 50 goroutines confirming the SAME hold
// concurrently. ConfirmSeats' CAS (status IN (1,3) AND hold_id=@holdID)
// is self-idempotent — once one confirm flips the seat to BOOKED, every
// other confirm attempt matches zero rows.
func TestConfirm_ExactlyOneWinner(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(102)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	hold, err := svc.AcquireHold(ctx, eventID, []int64{seatID}, "user-confirm-test")
	if err != nil {
		t.Fatalf("setup AcquireHold: %v", err)
	}
	fence := hold.Seats[0].FenceToken

	const n = 50
	var successes, failures int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			err := svc.ConfirmSeats(context.Background(), eventID, []int64{seatID}, []int64{fence}, hold.HoldID, uuid.New())
			if err == nil {
				atomic.AddInt64(&successes, 1)
			} else if errors.Is(err, ErrHoldExpired) {
				atomic.AddInt64(&failures, 1)
			} else {
				t.Logf("goroutine %d: unexpected error: %v", g, err)
			}
		}(g)
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	if failures != n-1 {
		t.Fatalf("failures = %d, want %d", failures, n-1)
	}

	var status int16
	if err := pool.QueryRow(ctx, `SELECT status FROM event_seats WHERE event_id=$1 AND seat_id=$2`, eventID, seatID).Scan(&status); err != nil {
		t.Fatalf("query final status: %v", err)
	}
	if status != 2 {
		t.Fatalf("final status = %d, want 2 (BOOKED)", status)
	}
}

// TestExpiredHoldIsReclaimed proves passive expiry ALONE is sufficient —
// no reaper, no Redis TTL involvement. A HELD-but-expired row is seeded
// directly via raw SQL (Redis was never touched for this fake hold), and a
// fresh AcquireHold must still succeed purely off the
// `hold_expires_at < now()` predicate in the CAS's WHERE clause.
func TestExpiredHoldIsReclaimed(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(103)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	fakeHoldID := uuid.New()
	mustExec(t, pool,
		`UPDATE event_seats SET status=1, hold_id=$3, held_by='ghost', hold_expires_at=now() - interval '1 minute'
		 WHERE event_id=$1 AND seat_id=$2`, eventID, seatID, fakeHoldID)

	var statusBefore int16
	if err := pool.QueryRow(ctx, `SELECT status FROM event_seats WHERE event_id=$1 AND seat_id=$2`, eventID, seatID).Scan(&statusBefore); err != nil {
		t.Fatalf("query pre-reclaim status: %v", err)
	}
	if statusBefore != 1 {
		t.Fatalf("pre-reclaim status = %d, want 1 (HELD, seeded as already-expired)", statusBefore)
	}

	hold, err := svc.AcquireHold(ctx, eventID, []int64{seatID}, "new-claimant")
	if err != nil {
		t.Fatalf("AcquireHold on an expired hold: unexpected err = %v (passive expiry should have reclaimed it)", err)
	}
	if hold.Seats[0].SeatID != seatID {
		t.Fatalf("acquired the wrong seat: %d", hold.Seats[0].SeatID)
	}
}

// TestStaleFenceRejected: a hold acquired with a short TTL naturally
// expires (both at Redis and the DB), a second contender reclaims it and
// bumps the fence, and the FIRST hold's now-stale fence must be rejected
// by ConfirmSeats — the RedLock-question answer (docs/plan.md, fixed gap #3).
func TestStaleFenceRejected(t *testing.T) {
	pool := newTestPool(t)
	shortTTLSvc := newRealService(t, pool, 150*time.Millisecond)
	svc := newRealService(t, pool, 10*time.Minute)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(104)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	firstHold, err := shortTTLSvc.AcquireHold(ctx, eventID, []int64{seatID}, "zombie-holder")
	if err != nil {
		t.Fatalf("first AcquireHold: %v", err)
	}
	staleFence := firstHold.Seats[0].FenceToken

	time.Sleep(300 * time.Millisecond) // past the 150ms TTL, at both Redis and the DB

	secondHold, err := svc.AcquireHold(ctx, eventID, []int64{seatID}, "real-claimant")
	if err != nil {
		t.Fatalf("reclaim AcquireHold: %v", err)
	}
	if secondHold.Seats[0].FenceToken == staleFence {
		t.Fatalf("fence token did not bump on reclaim: still %d", staleFence)
	}

	// The zombie's confirm, with its now-stale fence, must be rejected.
	err = svc.ConfirmSeats(ctx, eventID, []int64{seatID}, []int64{staleFence}, firstHold.HoldID, uuid.New())
	if !errors.Is(err, ErrHoldExpired) {
		t.Fatalf("zombie confirm with stale fence: err = %v, want ErrHoldExpired", err)
	}

	// The real claimant's confirm, with the current fence, must succeed.
	if err := svc.ConfirmSeats(ctx, eventID, []int64{seatID}, []int64{secondHold.Seats[0].FenceToken}, secondHold.HoldID, uuid.New()); err != nil {
		t.Fatalf("real claimant confirm: unexpected err = %v", err)
	}
}

// TestMultiSeatOverlap_LoserSeatsStayAvailableNotOrphaned: two 4-seat
// requests share exactly one seat. The second must fail entirely (not
// partially), and — the property that actually matters — its other THREE
// seats must be AVAILABLE afterward, not stuck in some half-held state.
func TestMultiSeatOverlap_LoserSeatsStayAvailableNotOrphaned(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	// A,B,C,D (holder 1) + E,F,G (holder 2's genuinely-free seats) + D
	// shared between both requests, seeded once so seat_ordinal stays unique.
	seedTestEventSeats(t, pool, eventID, []int64{200, 201, 202, 203, 204, 205, 206})

	// holder 1 takes seats 200,201,202,203 (A,B,C,D)
	_, err := svc.AcquireHold(ctx, eventID, []int64{200, 201, 202, 203}, "holder-1")
	if err != nil {
		t.Fatalf("holder-1 AcquireHold: %v", err)
	}

	// holder 2 wants 203 (D, already taken) plus three genuinely free seats.
	_, err = svc.AcquireHold(ctx, eventID, []int64{203, 204, 205, 206}, "holder-2")
	if err == nil {
		t.Fatal("holder-2 AcquireHold unexpectedly succeeded despite seat 203 being taken")
	}
	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("err = %v, want *ConflictError", err)
	}
	if len(conflictErr.Conflicts) != 1 || conflictErr.Conflicts[0] != 203 {
		t.Fatalf("Conflicts = %v, want [203]", conflictErr.Conflicts)
	}

	// The whole point of this test: 204, 205, 206 must be AVAILABLE, not
	// orphaned in some intermediate HELD-by-nobody state.
	rows, err := pool.Query(ctx,
		`SELECT seat_id, status, hold_id FROM event_seats WHERE event_id=$1 AND seat_id IN (204,205,206) ORDER BY seat_id`,
		eventID)
	if err != nil {
		t.Fatalf("query loser seats: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var seatID int64
		var status int16
		var holdID *string
		if err := rows.Scan(&seatID, &status, &holdID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		if status != 0 {
			t.Errorf("seat %d: status = %d, want 0 (AVAILABLE, not orphaned)", seatID, status)
		}
		if holdID != nil {
			t.Errorf("seat %d: hold_id = %v, want NULL", seatID, *holdID)
		}
	}
	if count != 3 {
		t.Fatalf("expected 3 rows checked, got %d", count)
	}
}

// TestBestAvailable_AcquiresARealContiguousRun exercises the whole path
// against real seats from a real row (venue 1's seat_rows), not synthetic
// row_ids — proving findContiguousRuns' row-boundary logic holds against
// the actual seed-venue layout, not just a hand-built fixture.
func TestBestAvailable_AcquiresARealContiguousRun(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)
	ctx := context.Background()

	// Pick a real row from venue 1 with at least 4 seats, ordered by label —
	// scripts/seed-venue lays out 100 seats/row, so any row from that seed
	// works; row_id 1 is section A's first row.
	rows, err := pool.Query(ctx,
		`SELECT s.seat_id FROM seats s WHERE s.row_id = 1 ORDER BY s.seat_label::int LIMIT 4`)
	if err != nil {
		t.Fatalf("query real row seats: %v", err)
	}
	var realSeatIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		realSeatIDs = append(realSeatIDs, id)
	}
	rows.Close()
	if len(realSeatIDs) != 4 {
		t.Fatalf("expected 4 real seats from row_id=1, got %d — is venue 1 seeded?", len(realSeatIDs))
	}

	eventID := seedTestEvent(t, pool, testVenueID)
	seedTestEventSeats(t, pool, eventID, realSeatIDs)

	hold, err := svc.BestAvailable(ctx, eventID, 3, 5000, "best-available-user")
	if err != nil {
		t.Fatalf("BestAvailable: %v", err)
	}
	if len(hold.Seats) != 3 {
		t.Fatalf("got %d seats, want 3", len(hold.Seats))
	}
	// The 3 acquired seats must be a contiguous ordinal sub-run of the 4 seeded.
	got := map[int64]bool{}
	for _, s := range hold.Seats {
		got[s.SeatID] = true
	}
	matched := 0
	for _, id := range realSeatIDs {
		if got[id] {
			matched++
		}
	}
	if matched != 3 {
		t.Fatalf("acquired seats %v don't align with the seeded real row seats %v", hold.Seats, realSeatIDs)
	}
}

func TestBestAvailable_NoRunLargeEnoughReturnsErrNoContiguousSeats(t *testing.T) {
	pool := newTestPool(t)
	svc := newRealService(t, pool, 10*time.Minute)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seedTestEventSeats(t, pool, eventID, []int64{300}) // only 1 seat available

	_, err := svc.BestAvailable(ctx, eventID, 4, 5000, "user")
	if !errors.Is(err, ErrNoContiguousSeats) {
		t.Fatalf("err = %v, want ErrNoContiguousSeats", err)
	}
}
