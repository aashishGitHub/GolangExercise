// Package inventory is THE correctness core (docs/plan.md "The correctness
// core"). No package other than this one may write event_seats — that is a
// hard, greppable invariant: every write to that table lives in this file.
//
// Postgres is the sole arbiter (docs/plan.md decision #1). Redis
// (internal/holdlock) is a throughput-only contention filter that can be
// wrong, flushed, or entirely unreachable without ever producing a
// double-book — only ever a double-hold, which the DB CAS resolves down to
// exactly one winner. Every multi-seat statement here receives its seat_ids
// pre-sorted ascending (see sortDedup) — that ordering is what makes
// deadlock between two concurrent multi-seat CAS statements structurally
// impossible, not just unlikely (fixed gap #8).
package inventory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/db"
	"ticketing/internal/events"
)

var (
	// ErrSeatTaken: a legitimate race lost. Always carries a *ConflictError
	// via errors.As for the specific seat ids — retryable with DIFFERENT
	// seats, not the same request (docs/plan.md "Status-code semantics").
	ErrSeatTaken = errors.New("seat_taken")
	// ErrTooManySeats: over MAX_SEATS_PER_HOLD (fixed gap #5).
	ErrTooManySeats = errors.New("too_many_seats")
	// ErrDuplicateSeat: the same seat id appeared twice in one request —
	// rejected before it can reach ConfirmSeats' unnest join, which would
	// otherwise error ("command cannot affect row a second time") on a
	// duplicate (fixed gap #9).
	ErrDuplicateSeat = errors.New("duplicate_seat")
	// ErrHoldExpired: the hold existed but its clock beat the caller —
	// retryable by re-holding (distinct from ErrSeatTaken).
	ErrHoldExpired = errors.New("hold_expired")
)

// ConflictError carries the seat ids a caller lost the race for, so the
// HTTP layer can report exactly which seats to repaint/retry.
type ConflictError struct {
	Conflicts []int64
}

func (e *ConflictError) Error() string        { return "seat_taken" }
func (e *ConflictError) Is(target error) bool { return target == ErrSeatTaken }

// Hold is the result of a successful AcquireHold.
type Hold struct {
	HoldID    uuid.UUID
	EventID   int64
	ExpiresAt time.Time
	Seats     []HeldSeat
}

type HeldSeat struct {
	SeatID      int64
	SeatOrdinal int32
	FenceToken  int64
	PriceCents  int32
}

// Locker is the Redis contention-filter surface Service depends on —
// *holdlock.Lock satisfies it structurally. Kept as an interface so unit
// tests can inject a fake instead of dialing real Redis (or a nil pointer,
// which would panic on first use).
type Locker interface {
	Acquire(ctx context.Context, eventID, seatID int64, holdID string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, eventID, seatID int64, holdID string) error
}

type Service struct {
	pool            *pgxpool.Pool
	q               db.Querier
	lock            Locker
	holdTTL         time.Duration
	maxSeatsPerHold int
}

func New(pool *pgxpool.Pool, q db.Querier, lock Locker, holdTTL time.Duration, maxSeatsPerHold int) *Service {
	return &Service{pool: pool, q: q, lock: lock, holdTTL: holdTTL, maxSeatsPerHold: maxSeatsPerHold}
}

// sortDedup sorts ascending and reports whether any duplicate was removed.
func sortDedup(ids []int64) (sorted []int64, hadDupes bool) {
	cp := append([]int64(nil), ids...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	out := cp[:0]
	var prev int64 = -1
	first := true
	for _, id := range cp {
		if !first && id == prev {
			hadDupes = true
			continue
		}
		out = append(out, id)
		prev = id
		first = false
	}
	return out, hadDupes
}

// AcquireHold implements docs/plan.md "Acquire a hold" steps 0-3 (step 4,
// scheduling the active release, is Phase 5's cmd/hold-reaper).
func (s *Service) AcquireHold(ctx context.Context, eventID int64, seatIDs []int64, userSub string) (*Hold, error) {
	seatIDs, hadDupes := sortDedup(seatIDs)
	if hadDupes {
		return nil, ErrDuplicateSeat
	}
	if len(seatIDs) == 0 {
		return nil, fmt.Errorf("acquire hold: no seats requested")
	}
	if len(seatIDs) > s.maxSeatsPerHold {
		return nil, ErrTooManySeats
	}

	holdID := uuid.New()

	// Step 2: Redis contention filter, seats ascending. On ErrUnavailable
	// we stop trying Redis for the REST of this batch and let the DB CAS
	// be the sole gate for them — already-acquired keys stay acquired,
	// nothing is released just because Redis blipped mid-loop (fixed gap #7).
	var acquiredRedis []int64
	for _, seatID := range seatIDs {
		ok, err := s.lock.Acquire(ctx, eventID, seatID, holdID.String(), s.holdTTL)
		if err != nil {
			log.Printf("inventory: redis unavailable mid-acquire, degrading to DB-only for the rest of this batch: %v", err)
			break
		}
		if !ok {
			s.releaseRedisBatch(ctx, eventID, acquiredRedis, holdID.String())
			s.audit(ctx, holdID, eventID, seatID, userSub, "LOST_REDIS", 0, time.Time{})
			return nil, &ConflictError{Conflicts: []int64{seatID}}
		}
		acquiredRedis = append(acquiredRedis, seatID)
	}

	// Step 3: Postgres CAS — the arbiter, in an explicit transaction so a
	// partial match rolls back EVERYTHING (docs/plan.md: "Partial ->
	// ROLLBACK the whole batch" — a multi-seat hold is all-or-nothing).
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.releaseRedisBatch(ctx, eventID, acquiredRedis, holdID.String())
		return nil, fmt.Errorf("acquire hold: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	qtx := db.New(tx)
	rows, err := qtx.AcquireHold(ctx, db.AcquireHoldParams{
		EventID: eventID, SeatIds: seatIDs,
		HoldID: toPgUUID(holdID), UserSub: toPgText(userSub),
		TtlSeconds: int32(s.holdTTL.Seconds()),
	})
	if err != nil {
		s.releaseRedisBatch(ctx, eventID, acquiredRedis, holdID.String())
		return nil, fmt.Errorf("acquire hold: cas: %w", err)
	}

	if len(rows) != len(seatIDs) {
		_ = tx.Rollback(ctx) // explicit: about to reuse ctx for compensating writes
		s.releaseRedisBatch(ctx, eventID, acquiredRedis, holdID.String())

		won := make(map[int64]bool, len(rows))
		for _, r := range rows {
			won[r.SeatID] = true
		}
		var conflicts []int64
		for _, id := range seatIDs {
			if !won[id] {
				conflicts = append(conflicts, id)
			}
		}
		for _, c := range conflicts {
			s.audit(ctx, holdID, eventID, c, userSub, "LOST_DB_CAS", 0, time.Time{})
		}
		return nil, &ConflictError{Conflicts: conflicts}
	}

	// Outbox write in the SAME transaction as the CAS (docs/plan.md
	// decision #2's mechanism) — one event per seat, matching holds_audit's
	// own per-seat granularity.
	for _, r := range rows {
		aggregateID := fmt.Sprintf("%d:%d", eventID, r.SeatID)
		if err := events.Publish(ctx, qtx, aggregateID, "seat.held", map[string]any{
			"eventId": eventID, "seatId": r.SeatID, "seatOrdinal": r.SeatOrdinal,
			"holdId": holdID, "fenceToken": r.FenceToken, "expiresAt": r.HoldExpiresAt.Time,
		}); err != nil {
			s.releaseRedisBatch(ctx, eventID, acquiredRedis, holdID.String())
			return nil, fmt.Errorf("acquire hold: publish outbox event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		s.releaseRedisBatch(ctx, eventID, acquiredRedis, holdID.String())
		return nil, fmt.Errorf("acquire hold: commit: %w", err)
	}

	hold := &Hold{HoldID: holdID, EventID: eventID}
	for _, r := range rows {
		hold.ExpiresAt = r.HoldExpiresAt.Time
		hold.Seats = append(hold.Seats, HeldSeat{
			SeatID: r.SeatID, SeatOrdinal: r.SeatOrdinal,
			FenceToken: r.FenceToken, PriceCents: r.HoldPriceCents.Int32,
		})
	}
	for _, seat := range hold.Seats {
		s.audit(ctx, holdID, eventID, seat.SeatID, userSub, "ACQUIRED", seat.FenceToken, hold.ExpiresAt)
	}
	return hold, nil
}

// ReleaseHold is idempotent by construction (docs/plan.md "Release") —
// rowcount 0 is success, not an error: the hold was already gone (expired-
// and-reclaimed, or already confirmed). Runs in an explicit transaction
// purely so the outbox write shares the CAS's fate (docs/plan.md decision
// #2) — there is no partial-match/rollback concern here the way there is
// for Acquire/Confirm, since a release's WHERE clause has no "must match
// every seat or none" requirement.
func (s *Service) ReleaseHold(ctx context.Context, eventID int64, seatIDs []int64, holdID uuid.UUID, userSub string) error {
	seatIDs, _ = sortDedup(seatIDs)
	for _, seatID := range seatIDs {
		if err := s.lock.Release(ctx, eventID, seatID, holdID.String()); err != nil {
			log.Printf("inventory: redis release failed (best-effort, not fatal): %v", err)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("release hold: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	n, err := qtx.ReleaseHold(ctx, db.ReleaseHoldParams{
		EventID: eventID, SeatIds: seatIDs, HoldID: toPgUUID(holdID),
	})
	if err != nil {
		return fmt.Errorf("release hold: %w", err)
	}
	if n > 0 {
		for _, seatID := range seatIDs {
			aggregateID := fmt.Sprintf("%d:%d", eventID, seatID)
			if err := events.Publish(ctx, qtx, aggregateID, "seat.released", map[string]any{
				"eventId": eventID, "seatId": seatID, "holdId": holdID,
			}); err != nil {
				return fmt.Errorf("release hold: publish outbox event: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("release hold: commit: %w", err)
	}

	for _, seatID := range seatIDs {
		s.audit(ctx, holdID, eventID, seatID, userSub, "RELEASED", 0, time.Time{})
	}
	return nil
}

// ExtendHold is called at payment initiation (docs/plan.md "Extend at
// payment initiation"). It does not roll back partial extends — the
// caller (the order saga, Phase 6) checks rowcount < len(seatIDs) and
// aborts BEFORE charging if the hold has already lapsed.
func (s *Service) ExtendHold(ctx context.Context, eventID int64, seatIDs []int64, holdID uuid.UUID, extend time.Duration) (int64, error) {
	seatIDs, _ = sortDedup(seatIDs)
	n, err := s.q.ExtendHold(ctx, db.ExtendHoldParams{
		EventID: eventID, SeatIds: seatIDs, HoldID: toPgUUID(holdID),
		ExtendSeconds: int32(extend.Seconds()),
	})
	if err != nil {
		return 0, fmt.Errorf("extend hold: %w", err)
	}
	if n < int64(len(seatIDs)) {
		return n, ErrHoldExpired
	}
	return n, nil
}

// ConfirmSeats is the single most important statement in the whole system
// (docs/plan.md "Confirm"). seatIDs and fences MUST be the same length and
// index-aligned — fences[i] is the fence token AcquireHold returned for
// seatIDs[i]. Runs in an explicit transaction: a partial match rolls back
// EVERYTHING, exactly like Acquire.
func (s *Service) ConfirmSeats(ctx context.Context, eventID int64, seatIDs, fences []int64, holdID, orderID uuid.UUID) error {
	if len(seatIDs) != len(fences) {
		return fmt.Errorf("confirm seats: seatIDs and fences length mismatch (%d != %d)", len(seatIDs), len(fences))
	}
	sortSeatsAndFencesTogether(seatIDs, fences)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("confirm seats: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := db.New(tx)
	n, err := qtx.ConfirmSeats(ctx, db.ConfirmSeatsParams{
		EventID: eventID, SeatIds: seatIDs, Fences: fences,
		HoldID: toPgUUID(holdID), OrderID: toPgUUID(orderID),
	})
	if err != nil {
		return fmt.Errorf("confirm seats: cas: %w", err)
	}
	if n != int64(len(seatIDs)) {
		_ = tx.Rollback(ctx)
		for _, seatID := range seatIDs {
			s.audit(ctx, holdID, eventID, seatID, "", "STALE_FENCE_REJECTED", 0, time.Time{})
		}
		return fmt.Errorf("confirm seats: %w (matched %d of %d — hold expired or fence stale)", ErrHoldExpired, n, len(seatIDs))
	}

	for _, seatID := range seatIDs {
		aggregateID := fmt.Sprintf("%d:%d", eventID, seatID)
		if err := events.Publish(ctx, qtx, aggregateID, "seat.booked", map[string]any{
			"eventId": eventID, "seatId": seatID, "orderId": orderID, "holdId": holdID,
		}); err != nil {
			return fmt.Errorf("confirm seats: publish outbox event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirm seats: commit: %w", err)
	}
	for _, seatID := range seatIDs {
		s.audit(ctx, holdID, eventID, seatID, "", "CONFIRMED", 0, time.Time{})
	}
	return nil
}

func (s *Service) releaseRedisBatch(ctx context.Context, eventID int64, seatIDs []int64, holdID string) {
	for _, seatID := range seatIDs {
		if err := s.lock.Release(ctx, eventID, seatID, holdID); err != nil {
			log.Printf("inventory: redis compensating release failed (best-effort): %v", err)
		}
	}
}

// audit writes holds_audit OUTSIDE any hold/confirm transaction (docs/plan.md
// "holds_audit" — "a lost CAS has no transaction to ride on"), via the
// service's plain Querier so it never adds latency to — or shares fate
// with — the hot-path transaction. Best-effort: an audit failure is logged,
// never returned to the caller.
func (s *Service) audit(ctx context.Context, holdID uuid.UUID, eventID, seatID int64, userSub, outcome string, fenceToken int64, expiresAt time.Time) {
	arg := db.InsertHoldsAuditParams{
		HoldID: holdID, EventID: eventID, SeatID: seatID, UserSub: userSub, Outcome: outcome,
	}
	if fenceToken != 0 {
		arg.FenceToken = pgtype.Int8{Int64: fenceToken, Valid: true}
	}
	if !expiresAt.IsZero() {
		arg.ExpiresAt = pgtype.Timestamptz{Time: expiresAt, Valid: true}
	}
	if err := s.q.InsertHoldsAudit(ctx, arg); err != nil {
		log.Printf("inventory: holds_audit write failed (best-effort): %v", err)
	}
}

func sortSeatsAndFencesTogether(seatIDs, fences []int64) {
	idx := make([]int, len(seatIDs))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return seatIDs[idx[a]] < seatIDs[idx[b]] })
	sortedSeats := make([]int64, len(seatIDs))
	sortedFences := make([]int64, len(fences))
	for i, j := range idx {
		sortedSeats[i] = seatIDs[j]
		sortedFences[i] = fences[j]
	}
	copy(seatIDs, sortedSeats)
	copy(fences, sortedFences)
}

func toPgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
func toPgText(s string) pgtype.Text    { return pgtype.Text{String: s, Valid: true} }
