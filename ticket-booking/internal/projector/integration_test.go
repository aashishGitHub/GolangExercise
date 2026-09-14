//go:build integration

//	Run: docker compose up -d postgres redis && migrate -path migrations up \
//	       && go test -tags=integration ./internal/projector/...
package projector

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/seatmap"
	"ticketing/internal/wsproto"
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

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: envOr("REDIS_ADDR", "localhost:6379")})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
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
		 VALUES ($1, 'Projector Test', 'Projector Test', now() + interval '1 day', now())
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

func newTestSetup(t *testing.T) (*pgxpool.Pool, db.Querier, *redis.Client, *inventory.Service, *Projector) {
	t.Helper()
	pool := newTestPool(t)
	q := db.New(pool)
	rdb := newTestRedis(t)
	lock := holdlock.New(envOr("REDIS_ADDR", "localhost:6379"))
	t.Cleanup(func() { _ = lock.Close() })
	inv := inventory.New(pool, q, lock, 10*time.Minute, 8)
	return pool, q, rdb, inv, New(q, rdb)
}

func TestProjector_SeatHeldThenReleased(t *testing.T) {
	pool, _, rdb, inv, proj := newTestSetup(t)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(9001)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	// Ensure the bitset exists (as a real connecting WS client would
	// trigger) before any events are applied, so we can observe the
	// transition from an explicit FREE baseline.
	packed, seq, err := proj.EnsureBitset(ctx, eventID)
	if err != nil {
		t.Fatalf("EnsureBitset: %v", err)
	}
	if seatmap.Get(packed, 0) != seatmap.StateFree || seq != 0 {
		t.Fatalf("initial state = %v seq=%d, want FREE seq=0", seatmap.Get(packed, 0), seq)
	}

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "projector-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}

	// drainAll, not a single RunOnce: on a shared domain_events table this
	// is the FIRST-ever run of a "projector" consumer against rows left
	// unprocessed by every earlier phase's integration test suite (a real,
	// legitimate catch-up — a new consumer replays the whole backlog, same
	// as the outbox relay would). The exact total applied is backlog-size-
	// dependent and not this test's concern; THIS event's own seq is,
	// since seq is scoped per event_id regardless of what else is in the
	// batch.
	drainAll(t, proj, ctx)

	packed, seq, err = proj.EnsureBitset(ctx, eventID)
	if err != nil {
		t.Fatalf("EnsureBitset after held: %v", err)
	}
	if got := seatmap.Get(packed, 0); got != seatmap.StateHeld {
		t.Fatalf("state after hold = %v, want HELD", got)
	}
	if seq != 1 {
		t.Fatalf("seq after hold = %d, want 1", seq)
	}

	if err := inv.ReleaseHold(ctx, eventID, []int64{seatID}, hold.HoldID, "projector-test-user"); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	drainAll(t, proj, ctx)

	packed, seq, err = proj.EnsureBitset(ctx, eventID)
	if err != nil {
		t.Fatalf("EnsureBitset after release: %v", err)
	}
	if got := seatmap.Get(packed, 0); got != seatmap.StateFree {
		t.Fatalf("state after release = %v, want FREE", got)
	}
	if seq != 2 {
		t.Fatalf("seq after release = %d, want 2", seq)
	}

	// Replaying RunOnce with nothing new pending must apply nothing —
	// processed_events dedup, not a bitset re-scan.
	processed, _, err := proj.RunOnce(ctx, 100)
	if err != nil {
		t.Fatalf("RunOnce (idle): %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed on idle tick = %d, want 0", processed)
	}

	_ = rdb // used indirectly via proj; kept for readability of the setup
}

// drainAll runs RunOnce until it consumes zero rows — the correct way to
// reach "fully caught up" against a domain_events table that may already
// hold a backlog from other test suites. Stopping on applied==0 instead
// would be wrong: a no-op event (bitset already reflects it) is still
// consumed but never increments applied, so a batch full of no-ops
// followed by more real backlog would look falsely "done".
func drainAll(t *testing.T, proj *Projector, ctx context.Context) {
	t.Helper()
	for i := 0; i < 100; i++ { // hard cap so a real bug fails fast instead of hanging
		processed, _, err := proj.RunOnce(ctx, 500)
		if err != nil {
			t.Fatalf("RunOnce (drain): %v", err)
		}
		if processed == 0 {
			return
		}
	}
	t.Fatal("drainAll: did not converge to 0 processed within 100 ticks")
}

func TestProjector_ConfirmProducesSoldState(t *testing.T) {
	pool, _, _, inv, proj := newTestSetup(t)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(9002)
	seedTestEventSeats(t, pool, eventID, []int64{seatID})

	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "projector-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}
	fences := make([]int64, len(hold.Seats))
	for i, s := range hold.Seats {
		fences[i] = s.FenceToken
	}
	orderID := uuid.New() // event_seats.booking_id has no FK to orders — a bare UUID is a valid, real confirm target
	if err := inv.ConfirmSeats(ctx, eventID, []int64{seatID}, fences, hold.HoldID, orderID); err != nil {
		t.Fatalf("ConfirmSeats: %v", err)
	}

	drainAll(t, proj, ctx)

	packed, _, err := proj.EnsureBitset(ctx, eventID)
	if err != nil {
		t.Fatalf("EnsureBitset: %v", err)
	}
	if got := seatmap.Get(packed, 0); got != seatmap.StateSold {
		t.Fatalf("state after confirm = %v, want SOLD", got)
	}
}

func TestGapFill_CoversRecentGapAndOverflowTriggersResync(t *testing.T) {
	pool, _, _, inv, proj := newTestSetup(t)
	ctx := context.Background()

	eventID := seedTestEvent(t, pool, testVenueID)
	seatID := int64(9003)
	seatID2 := int64(9004)
	// Both seats seeded in ONE call: seedTestEventSeats numbers ordinals
	// from 0 within each call, so a second call for the same event_id
	// would collide on ordinal 0 against event_seats_ordinal_uq — the
	// exact bug already documented in Phase 3's integration tests.
	seedTestEventSeats(t, pool, eventID, []int64{seatID, seatID2})

	if _, _, err := proj.EnsureBitset(ctx, eventID); err != nil {
		t.Fatalf("EnsureBitset: %v", err)
	}

	// Three cheap transitions (held/released x1 + held) so we can gap-fill
	// a small, definitely-still-in-the-ring range first.
	hold, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "gap-test-user")
	if err != nil {
		t.Fatalf("AcquireHold: %v", err)
	}
	if err := inv.ReleaseHold(ctx, eventID, []int64{seatID}, hold.HoldID, "gap-test-user"); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if _, err := inv.AcquireHold(ctx, eventID, []int64{seatID}, "gap-test-user"); err != nil {
		t.Fatalf("AcquireHold 2: %v", err)
	}
	drainAll(t, proj, ctx)

	frame, ok, err := proj.GapFill(ctx, eventID, 0)
	if err != nil {
		t.Fatalf("GapFill: %v", err)
	}
	if !ok {
		t.Fatal("GapFill from seq 0 should be covered by the ring (only 3 deltas so far)")
	}
	f, err := wsproto.Decode(frame)
	if err != nil {
		t.Fatalf("decode gap-fill frame: %v", err)
	}
	// ONE combined frame (docs/plan.md: "one delta covering all 3"), not
	// three separate frames — seatID toggled held/released/held, so only
	// its FINAL state (HELD, from the second AcquireHold) should survive
	// the coalesce.
	if len(f.Changes) != 1 {
		t.Fatalf("gap-fill frame has %d changes, want 1 (coalesced)", len(f.Changes))
	}
	if got := seatmap.State(f.Changes[0].State); got != seatmap.StateHeld {
		t.Fatalf("coalesced final state = %v, want HELD", got)
	}
	if f.Seq != 3 {
		t.Fatalf("gap-fill frame seq = %d, want 3 (current seq)", f.Seq)
	}

	// Now overflow the ring (ringCap=200) with > 200 more transitions on
	// seatID2 (already seeded above), alternating hold/release, and verify
	// a stale sinceSeq gets ok=false — exactly one resync, not a doomed
	// partial replay.
	const iterations = 110 // 220 events, > ringCap
	for i := 0; i < iterations; i++ {
		h, err := inv.AcquireHold(ctx, eventID, []int64{seatID2}, "overflow-test-user")
		if err != nil {
			t.Fatalf("AcquireHold iter %d: %v", i, err)
		}
		if err := inv.ReleaseHold(ctx, eventID, []int64{seatID2}, h.HoldID, "overflow-test-user"); err != nil {
			t.Fatalf("ReleaseHold iter %d: %v", i, err)
		}
	}
	totalApplied := 0
	for i := 0; i < 100; i++ {
		processed, applied, err := proj.RunOnce(ctx, 500)
		if err != nil {
			t.Fatalf("RunOnce overflow drain: %v", err)
		}
		totalApplied += applied
		if processed == 0 {
			break
		}
	}
	if totalApplied < iterations*2 {
		t.Fatalf("applied %d events, want at least %d", totalApplied, iterations*2)
	}

	_, ok, err = proj.GapFill(ctx, eventID, 1) // seq 1 is long gone from the ring by now
	if err != nil {
		t.Fatalf("GapFill after overflow: %v", err)
	}
	if ok {
		t.Fatal("GapFill from an evicted seq should return ok=false (caller must resync via snapshot)")
	}
}
