// Package reaper is the ACTIVE hold-release scan (docs/plan.md decision
// #2: pure UX freshness, never a correctness requirement — the passive
// hold_expires_at < now() predicate in every CAS is the actual guarantee).
// Extracted out of cmd/hold-reaper (Phase 11) so cmd/hold-reaper's ticker
// and cmd/hold-reaper-lambda's single-shot Scheduler invocation call the
// exact same code, not two copies.
package reaper

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"ticketing/internal/db"
	"ticketing/internal/inventory"
)

// RunOnce scans event_seats_expiry_idx and actively releases every
// expired hold it finds, returning how many it released.
func RunOnce(ctx context.Context, q db.Querier, inv *inventory.Service) (int, error) {
	expired, err := q.ListExpiredHolds(ctx)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, h := range expired {
		seats, err := q.ListSeatIDsForHold(ctx, db.ListSeatIDsForHoldParams{EventID: h.EventID, HoldID: h.HoldID})
		if err != nil {
			log.Printf("reaper: list seats for hold %s: %v", h.HoldID, err)
			continue
		}
		if len(seats) == 0 {
			continue // already reclaimed by a contender since the scan — not an error
		}
		holdUUID, err := uuidFromPg(h.HoldID)
		if err != nil {
			log.Printf("reaper: bad hold_id %v: %v", h.HoldID, err)
			continue
		}
		// ReleaseHold is idempotent by construction — if a contender's own
		// AcquireHold already reclaimed this hold between the scan and
		// here, this is a harmless no-op (rowcount 0), not a race bug.
		if err := inv.ReleaseHold(ctx, h.EventID, seats, holdUUID, "system:hold-reaper"); err != nil {
			log.Printf("reaper: release hold %s: %v", h.HoldID, err)
			continue
		}
		log.Printf("reaper: released expired hold %s (event %d, %d seat(s))", h.HoldID, h.EventID, len(seats))
		released++
	}
	return released, nil
}

func uuidFromPg(u pgtype.UUID) (uuid.UUID, error) {
	if !u.Valid {
		return uuid.UUID{}, fmt.Errorf("null uuid")
	}
	return uuid.UUID(u.Bytes), nil
}
