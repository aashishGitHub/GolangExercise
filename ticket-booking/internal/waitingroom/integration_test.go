//go:build integration

//	Run: docker compose up -d postgres redis && migrate -path migrations up \
//	       && go test -tags=integration ./internal/waitingroom/...
package waitingroom

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/db"
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

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: envOr("REDIS_ADDR", "localhost:6379")})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func seedTestEvent(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var eventID int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (venue_id, artist, title, starts_at, onsale_at)
		 VALUES (1, 'Waiting Room Test', 'Waiting Room Test', now() + interval '1 day', now())
		 RETURNING event_id`).Scan(&eventID)
	if err != nil {
		t.Fatalf("seed test event: %v", err)
	}
	return eventID
}

func TestQueue_JoinBeforeCursorStaysQueued(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedis(t)
	eventID := seedTestEvent(t, pool)
	q := New(rdb, "test-secret")
	ctx := context.Background()

	// Cursor defaults to 0 (nothing admitted yet) — the first joiner is at
	// rank 0, which is NOT < cursor(0), so they must be queued, not
	// admitted, even though they're first in line.
	result, err := q.Join(ctx, eventID, "user-1")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if result.Admitted {
		t.Fatal("first joiner should be queued (rank 0 is not < cursor 0)")
	}
	if result.Position != 0 {
		t.Fatalf("Position = %d, want 0", result.Position)
	}
}

func TestQueue_AdmittedOnceCursorPassesRank(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedis(t)
	eventID := seedTestEvent(t, pool)
	q := New(rdb, "test-secret")
	ctx := context.Background()

	if _, err := q.Join(ctx, eventID, "user-1"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	// Advance the cursor manually, as the AIMD controller would.
	if err := rdb.Set(ctx, cursorKey(eventID), 1, 0).Err(); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	result, err := q.Join(ctx, eventID, "user-1")
	if err != nil {
		t.Fatalf("Join (2nd): %v", err)
	}
	if !result.Admitted {
		t.Fatal("user-1 should be admitted now that cursor(1) > their rank(0)")
	}
	if result.Token == "" {
		t.Fatal("expected a non-empty admission token")
	}
	if err := q.VerifyToken(result.Token, "user-1", eventID); err != nil {
		t.Fatalf("issued token failed its own verification: %v", err)
	}
}

func TestQueue_RejoinKeepsOriginalPlaceInLine(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedis(t)
	eventID := seedTestEvent(t, pool)
	q := New(rdb, "test-secret")
	ctx := context.Background()

	if _, err := q.Join(ctx, eventID, "user-1"); err != nil {
		t.Fatalf("Join (1st): %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := q.Join(ctx, eventID, "user-2"); err != nil {
		t.Fatalf("Join user-2: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	// user-1 re-POSTs (a refresh/retry) — ZADD NX must not move their
	// arrival score forward, or a refresh would unfairly cut in front of
	// arrivals that came in between.
	result, err := q.Join(ctx, eventID, "user-1")
	if err != nil {
		t.Fatalf("Join (rejoin): %v", err)
	}
	if result.Position != 0 {
		t.Fatalf("user-1's position after rejoin = %d, want 0 (still first)", result.Position)
	}
}

func TestController_AllGreenIncreasesRate(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedis(t)
	eventID := seedTestEvent(t, pool)
	q := db.New(pool)
	metrics := NewHoldMetrics(200) // empty: P99=0, ErrorRate=0 -> all green
	c := NewController(q, rdb, pool, metrics)
	ctx := context.Background()

	result, err := c.Tick(ctx, eventID)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if result.RedLatency || result.RedPool || result.RedErrors {
		t.Fatalf("expected an all-green tick, got %+v", result)
	}
	wantRate := DefaultRate + AdditiveStep
	if result.Rate != wantRate {
		t.Fatalf("rate = %v, want %v (DefaultRate + AdditiveStep)", result.Rate, wantRate)
	}

	rows, err := q.ListWaitingRoomAudit(ctx, eventID)
	if err != nil {
		t.Fatalf("ListWaitingRoomAudit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
}

func TestController_ClampedPoolTriggersRateHalvingWithinThreeTicks(t *testing.T) {
	// A SEPARATE pool clamped to 1 max conn, deliberately starved by
	// holding its only connection for the duration of the test — this is
	// docs/plan.md's literal ask: "clamp the pgx pool to 5 [here, 1, for a
	// sharp and fast signal] and show the AIMD rate halving within 3
	// ticks."
	dsn := envOr("DATABASE_URL", "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.MaxConns = 1
	clampedPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new clamped pool: %v", err)
	}
	t.Cleanup(clampedPool.Close)

	ctx := context.Background()
	conn, err := clampedPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the pool's only connection: %v", err)
	}
	defer conn.Release()
	// The pool now reports AcquiredConns()=1, MaxConns()=1 -> utilization
	// 1.0 > PoolRedThreshold(0.80) for as long as this connection is held.

	rdb := newTestRedis(t)
	mainPool := newTestPool(t)
	eventID := seedTestEvent(t, mainPool)
	q := db.New(mainPool)
	metrics := NewHoldMetrics(200)
	c := NewController(q, rdb, clampedPool, metrics)

	var last TickResult
	for i := 0; i < 3; i++ {
		last, err = c.Tick(ctx, eventID)
		if err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		if !last.RedPool {
			t.Fatalf("tick %d: RedPool = false, want true (pool 1/1 acquired)", i)
		}
	}
	// Start rate is DefaultRate(1.0); three red ticks halve it three times:
	// max(1.0*0.5, MinRate) = max(0.5, 1.0) = 1.0 each time (MinRate floors
	// it). Assert the floor holds and never creeps back up while pool
	// stays saturated — the actually-interesting behavior is verified via
	// a higher starting rate below.
	if last.Rate != MinRate {
		t.Fatalf("rate after 3 red ticks from DefaultRate = %v, want floored at MinRate=%v", last.Rate, MinRate)
	}

	// Seed a high rate manually (as if the queue had been green for a
	// while) and confirm it actually HALVES three times under sustained
	// red, the number docs/plan.md's verification asks for explicitly.
	if err := rdb.Set(ctx, rateKey(eventID), 100.0, 0).Err(); err != nil {
		t.Fatalf("seed rate: %v", err)
	}
	rates := make([]float64, 0, 3)
	for i := 0; i < 3; i++ {
		last, err = c.Tick(ctx, eventID)
		if err != nil {
			t.Fatalf("Tick (post-seed) %d: %v", i, err)
		}
		rates = append(rates, last.Rate)
	}
	want := []float64{50.0, 25.0, 12.5}
	for i, w := range want {
		if rates[i] != w {
			t.Fatalf("rate sequence = %v, want %v (halving each red tick)", rates, want)
		}
	}
	t.Logf("AIMD rate halved under sustained pool saturation: 100.0 -> %v", rates)
}
