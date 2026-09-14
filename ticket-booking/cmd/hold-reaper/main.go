// cmd/hold-reaper is the ACTIVE release loop — pure UX freshness, never a
// correctness requirement (docs/plan.md decision #2: the passive
// hold_expires_at < now() predicate in every CAS is the actual guarantee;
// this reaper only makes an expired seat's status flip sooner than "the
// next contender happens to try it"). Real deployment: an EventBridge
// Scheduler one-shot per hold (Phase 11); local dev substitutes this ticker
// scanning event_seats_expiry_idx, matching moto-server's inability to
// fire schedules (docs/plan.md fidelity gap #1).
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
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
		if err := tick(ctx, q, inv); err != nil {
			log.Printf("hold-reaper: tick error: %v", err)
		}
	}
}

func tick(ctx context.Context, q db.Querier, inv *inventory.Service) error {
	expired, err := q.ListExpiredHolds(ctx)
	if err != nil {
		return err
	}
	for _, h := range expired {
		seats, err := q.ListSeatIDsForHold(ctx, db.ListSeatIDsForHoldParams{EventID: h.EventID, HoldID: h.HoldID})
		if err != nil {
			log.Printf("hold-reaper: list seats for hold %s: %v", h.HoldID, err)
			continue
		}
		if len(seats) == 0 {
			continue // already reclaimed by a contender since the scan — not an error
		}
		holdUUID, err := uuidFromPg(h.HoldID)
		if err != nil {
			log.Printf("hold-reaper: bad hold_id %v: %v", h.HoldID, err)
			continue
		}
		// ReleaseHold is idempotent by construction — if a contender's own
		// AcquireHold already reclaimed this hold between the scan and
		// here, this is a harmless no-op (rowcount 0), not a race bug.
		if err := inv.ReleaseHold(ctx, h.EventID, seats, holdUUID, "system:hold-reaper"); err != nil {
			log.Printf("hold-reaper: release hold %s: %v", h.HoldID, err)
			continue
		}
		log.Printf("hold-reaper: released expired hold %s (event %d, %d seat(s))", h.HoldID, h.EventID, len(seats))
	}
	return nil
}

func uuidFromPg(u pgtype.UUID) (uuid.UUID, error) {
	if !u.Valid {
		return uuid.UUID{}, fmt.Errorf("null uuid")
	}
	return uuid.UUID(u.Bytes), nil
}
