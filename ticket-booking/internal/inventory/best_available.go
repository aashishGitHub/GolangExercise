package inventory

import (
	"context"
	"errors"
	"fmt"

	"ticketing/internal/db"
)

// ErrNoContiguousSeats: no run of `quantity` adjacent seats exists at or
// under maxPriceCents — none of the candidate runs found survived 3
// attempts against contention either.
var ErrNoContiguousSeats = errors.New("no_contiguous_seats")

const bestAvailableMaxAttempts = 3

// BestAvailable implements docs/plan.md's contiguous-seat algorithm: scan
// available seats ordered by ordinal, find runs of `quantity` CONSECUTIVE
// ordinals sharing the same row_id (ordinals are contiguous across a whole
// SECTION too, not just one row — decision #2 — so a run must break at a
// row boundary, not just an ordinal gap), then try to Acquire the first
// run found. If a concurrent Acquire wins the race for a seat in that run,
// retry against the NEXT run rather than the client's own round trip
// (bounded to 3 attempts — a real CAS conflict is normal under contention,
// not a bug to surface upward).
func (s *Service) BestAvailable(ctx context.Context, eventID int64, quantity int, maxPriceCents int32, userSub string) (*Hold, error) {
	if quantity <= 0 {
		return nil, fmt.Errorf("best available: quantity must be positive")
	}
	if quantity > s.maxSeatsPerHold {
		return nil, ErrTooManySeats
	}

	candidates, err := s.q.ListAvailableForBestAvailable(ctx, db.ListAvailableForBestAvailableParams{
		EventID: eventID, MaxPriceCents: maxPriceCents,
	})
	if err != nil {
		return nil, fmt.Errorf("best available: list candidates: %w", err)
	}

	runs := findContiguousRuns(candidates, quantity)
	if len(runs) == 0 {
		return nil, ErrNoContiguousSeats
	}

	var lastErr error
	attempts := bestAvailableMaxAttempts
	if len(runs) < attempts {
		attempts = len(runs)
	}
	for i := 0; i < attempts; i++ {
		hold, err := s.AcquireHold(ctx, eventID, runs[i], userSub)
		if err == nil {
			return hold, nil
		}
		lastErr = err
		if !errors.Is(err, ErrSeatTaken) {
			return nil, err // a real error (DB down, etc.) — don't mask it by retrying
		}
	}
	return nil, fmt.Errorf("best available: %w after %d attempts (last: %v)", ErrNoContiguousSeats, attempts, lastErr)
}

// findContiguousRuns scans candidates (already ordered by seat_ordinal) for
// every run of `quantity` consecutive ordinals within the same row, and
// returns each run's seat_ids in ordinal order.
func findContiguousRuns(candidates []db.ListAvailableForBestAvailableRow, quantity int) [][]int64 {
	var runs [][]int64
	for start := 0; start+quantity <= len(candidates); start++ {
		ok := true
		for i := 1; i < quantity; i++ {
			prev, cur := candidates[start+i-1], candidates[start+i]
			if cur.RowID != prev.RowID || cur.SeatOrdinal != prev.SeatOrdinal+1 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		run := make([]int64, quantity)
		for i := 0; i < quantity; i++ {
			run[i] = candidates[start+i].SeatID
		}
		runs = append(runs, run)
	}
	return runs
}
