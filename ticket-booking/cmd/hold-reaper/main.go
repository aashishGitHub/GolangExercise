// cmd/hold-reaper is the local-dev ticker loop calling internal/reaper's
// RunOnce every tick — the ACTIVE release loop, pure UX freshness, never a
// correctness requirement (docs/plan.md decision #2: the passive
// hold_expires_at < now() predicate in every CAS is the actual guarantee;
// this reaper only makes an expired seat's status flip sooner than "the
// next contender happens to try it"). The prod twin
// (cmd/hold-reaper-lambda, Phase 11) is invoked by an EventBridge
// Scheduler one-shot per hold instead of a periodic full-table scan —
// this ticker is the local substitute, matching moto-server's inability
// to fire schedules (docs/plan.md fidelity gap #1).
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/reaper"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	lock := holdlock.New(cfg.RedisAddr)
	defer lock.Close()
	inv := inventory.New(pool, q, lock, time.Duration(cfg.HoldTTLSeconds)*time.Second, cfg.MaxSeatsPerHold)

	log.Println("hold-reaper: scanning for expired holds every 3s")
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := reaper.RunOnce(ctx, q, inv); err != nil {
			log.Printf("hold-reaper: tick error: %v", err)
		}
	}
}
