// Package order is the saga orchestrator (docs/plan.md "The saga").
// orders.status IS the state machine:
//
//	PENDING -> AUTHORIZING -> CAPTURED -> CONFIRMING -> CONFIRMED -> TICKETED
//	                             |             |
//	                             |             +-> COMPENSATING -> REALLOCATED (->CONFIRMED)
//	                             |                               -> REFUNDING -> COMPENSATED
//	                             +-> FAILED (no money moved)
//
// issue_ticket is a STUB in this phase — Phase 9 builds the real QR/S3
// path; here it only flips status to TICKETED, documented rather than
// silently faked as complete.
package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/db"
	"ticketing/internal/events"
	"ticketing/internal/inventory"
	"ticketing/internal/payment"
)

var (
	ErrHoldNotFound = errors.New("hold not found")
	// ErrNotHoldOwner covers ownership; a second CreateOrder for the same
	// hold is rejected by orders.hold_id's UNIQUE constraint directly
	// (surfaced as a plain DB error, not a sentinel here).
	ErrNotHoldOwner = errors.New("caller does not own this hold")
)

const (
	extendBuffer   = 180 * time.Second // payment_timeout + buffer, docs/plan.md "Extend at payment initiation"
	reallocMaxMult = 1                 // reallocation searches at the SAME max price paid, never higher
)

type Service struct {
	pool *pgxpool.Pool
	q    db.Querier
	inv  *inventory.Service
	pay  payment.Provider
}

func New(pool *pgxpool.Pool, q db.Querier, inv *inventory.Service, pay payment.Provider) *Service {
	return &Service{pool: pool, q: q, inv: inv, pay: pay}
}

// CreateOrder validates the caller owns the hold, computes the total from
// the seats' price-locked-at-hold-time price_cents, and writes the orders +
// order_saga_steps rows in one transaction. It does NOT run the saga —
// callers (the HTTP handler, immediately, and cmd/saga-worker, as a
// catch-up sweep) call RunSaga separately, matching the outbox-relay's own
// "inline fast path + scheduled catch-up" shape from Phase 5.
func (s *Service) CreateOrder(ctx context.Context, holdID uuid.UUID, userSub string) (uuid.UUID, error) {
	seats, err := s.q.GetSeatsByHoldID(ctx, toPgUUID(holdID))
	if err != nil {
		return uuid.Nil, fmt.Errorf("create order: lookup hold: %w", err)
	}
	if len(seats) == 0 {
		return uuid.Nil, ErrHoldNotFound
	}
	if seats[0].HeldBy.String != userSub {
		return uuid.Nil, ErrNotHoldOwner
	}

	eventID := seats[0].EventID
	seatIDs := make([]int64, len(seats))
	var amount int32
	for i, seat := range seats {
		seatIDs[i] = seat.SeatID
		amount += seat.HoldPriceCents.Int32
	}

	orderID := uuid.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create order: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	if _, err := qtx.CreateOrder(ctx, db.CreateOrderParams{
		OrderID: orderID, UserSub: userSub, EventID: eventID,
		HoldID: holdID, SeatIds: seatIDs, AmountCents: amount,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("create order: insert: %w", err)
	}
	for _, step := range []string{"extend_hold", "charge", "confirm", "issue_ticket"} {
		if err := qtx.CreateSagaStep(ctx, db.CreateSagaStepParams{OrderID: orderID, Step: step}); err != nil {
			return uuid.Nil, fmt.Errorf("create order: saga step %s: %w", step, err)
		}
	}
	if err := events.Publish(ctx, qtx, orderID.String(), "order.created", map[string]any{
		"orderId": orderID, "eventId": eventID, "amountCents": amount,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("create order: publish outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("create order: commit: %w", err)
	}
	return orderID, nil
}

// RunSaga drives one order through every step, synchronously, to
// completion or to a stable non-terminal state (AUTHORIZING/CONFIRMING
// with an UNKNOWN payment — the reconciler's job, not this function's, to
// resolve). Safe to call repeatedly on the same order: each step checks
// the order's current status before acting.
func (s *Service) RunSaga(ctx context.Context, orderID uuid.UUID) error {
	ord, err := s.q.GetOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("run saga: get order: %w", err)
	}
	if ord.Status != "PENDING" && ord.Status != "AUTHORIZING" && ord.Status != "CONFIRMING" {
		return nil // terminal or already resolved — nothing to do
	}

	holdID := ord.HoldID

	// Step 1: extend_hold — must succeed BEFORE any money moves.
	if ord.Status == "PENDING" {
		_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: orderID, Status: "AUTHORIZING"})
		_, err := s.inv.ExtendHold(ctx, ord.EventID, ord.SeatIds, holdID, extendBuffer)
		if err != nil {
			s.markStep(ctx, orderID, "extend_hold", "FAILED", err.Error())
			s.fail(ctx, orderID, "hold_expired", "hold expired before payment could be authorized")
			return nil
		}
		s.markStep(ctx, orderID, "extend_hold", "DONE", "")
	}

	// Step 2: charge — idempotency key stable per (user, order, hold), so
	// a retry of THIS function re-sends the same key rather than issuing a
	// new charge (deep-dive.md §6).
	idempotencyKey := chargeIdempotencyKey(ord.UserSub, orderID, holdID)
	payRow, err := s.q.InsertPayment(ctx, db.InsertPaymentParams{
		PaymentID: uuid.New(), OrderID: orderID, IdempotencyKey: idempotencyKey, AmountCents: ord.AmountCents,
	})
	if err != nil {
		// ON CONFLICT DO NOTHING with :one means a re-run's INSERT found an
		// existing row and returned nothing usable — fetch it instead.
		payRow, err = s.q.GetPaymentByIdempotencyKey(ctx, idempotencyKey)
		if err != nil {
			return fmt.Errorf("run saga: get existing payment: %w", err)
		}
	}

	if payRow.Status == "PENDING" {
		result, chargeErr := s.pay.Charge(ctx, idempotencyKey, ord.AmountCents)
		if chargeErr != nil {
			return fmt.Errorf("run saga: charge: %w", chargeErr)
		}
		_ = s.q.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
			PaymentID: payRow.PaymentID, Status: string(result.Status),
			ProviderRef: toPgText(result.ProviderRef), LastError: pgtype.Text{},
		})
		payRow.Status = string(result.Status)
	}

	switch payRow.Status {
	case "FAILED":
		s.markStep(ctx, orderID, "charge", "FAILED", "payment declined")
		s.fail(ctx, orderID, "payment_declined", "payment provider declined the charge")
		return nil
	case "UNKNOWN":
		// Ambiguous — never blindly retry. Leave the order in AUTHORIZING;
		// the reconciler resolves this by querying the provider directly.
		return nil
	case "CAPTURED":
		s.markStep(ctx, orderID, "charge", "DONE", "")
	default:
		return nil // still PENDING somehow — leave for the next run
	}

	// Step 3: confirm.
	_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: orderID, Status: "CONFIRMING"})
	fences, err := s.currentFences(ctx, ord.EventID, ord.SeatIds, holdID)
	if err != nil {
		return fmt.Errorf("run saga: read fences: %w", err)
	}
	confirmErr := s.inv.ConfirmSeats(ctx, ord.EventID, ord.SeatIds, fences, holdID, orderID)
	if confirmErr == nil {
		s.markStep(ctx, orderID, "confirm", "DONE", "")
		_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: orderID, Status: "CONFIRMED"})
		// Step 4: issue_ticket — STUB. Real QR/S3 issuance is Phase 9.
		s.markStep(ctx, orderID, "issue_ticket", "DONE", "")
		_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: orderID, Status: "TICKETED"})
		return nil
	}

	// Confirm failed — money is ALREADY captured. Compensate: re-allocate
	// an equivalent seat FIRST, refund only if that fails (docs/plan.md's
	// compensation priority, script.md: "most users prefer a seat two rows
	// back over a refund").
	return s.compensate(ctx, ord, holdID, payRow, confirmErr)
}

func (s *Service) compensate(ctx context.Context, ord db.Order, oldHoldID uuid.UUID, payRow db.Payment, cause error) error {
	orderID := ord.OrderID
	quantity := len(ord.SeatIds)
	maxPrice := ord.AmountCents / int32(quantity)

	newHold, reallocErr := s.inv.BestAvailable(ctx, ord.EventID, quantity, maxPrice*reallocMaxMult, ord.UserSub)
	if reallocErr == nil {
		newFences := make([]int64, len(newHold.Seats))
		newSeatIDs := make([]int64, len(newHold.Seats))
		for i, seat := range newHold.Seats {
			newSeatIDs[i] = seat.SeatID
			newFences[i] = seat.FenceToken
		}
		if confirmErr := s.inv.ConfirmSeats(ctx, ord.EventID, newSeatIDs, newFences, newHold.HoldID, orderID); confirmErr == nil {
			_ = s.q.ReallocateOrder(ctx, db.ReallocateOrderParams{OrderID: ord.OrderID, HoldID: newHold.HoldID, SeatIds: newSeatIDs})
			_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: ord.OrderID, Status: "CONFIRMED"})
			s.markStep(ctx, orderID, "confirm", "COMPENSATED", fmt.Sprintf("reallocated after: %v", cause))
			s.markStep(ctx, orderID, "issue_ticket", "DONE", "")
			_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: ord.OrderID, Status: "TICKETED"})
			return nil
		}
		_ = s.inv.ReleaseHold(ctx, ord.EventID, newSeatIDs, newHold.HoldID, ord.UserSub)
	}

	// Reallocation didn't work — refund.
	refundRef, refundErr := s.pay.Refund(ctx, payRow.ProviderRef.String, payRow.AmountCents)
	if refundErr != nil {
		return fmt.Errorf("run saga: refund after failed compensation: %w", refundErr)
	}
	_, _ = s.q.InsertRefund(ctx, db.InsertRefundParams{
		RefundID: uuid.New(), PaymentID: payRow.PaymentID, ProviderRef: toPgText(refundRef),
		AmountCents: payRow.AmountCents, Status: "COMPLETED",
	})
	s.markStep(ctx, orderID, "confirm", "COMPENSATED", fmt.Sprintf("refunded after: %v", cause))
	_ = s.q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{OrderID: ord.OrderID, Status: "COMPENSATED"})
	return nil
}

// currentFences reads the LIVE fence_token per seat from event_seats — the
// fences AcquireHold originally returned may be stale if anything reclaimed
// and re-held in between (which ConfirmSeats' own equality check would
// reject anyway; reading fresh here just avoids a guaranteed-wrong attempt).
func (s *Service) currentFences(ctx context.Context, eventID int64, seatIDs []int64, holdID uuid.UUID) ([]int64, error) {
	rows, err := s.q.GetSeatsByHoldID(ctx, toPgUUID(holdID))
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]int64, len(rows))
	for _, r := range rows {
		byID[r.SeatID] = r.FenceToken
	}
	fences := make([]int64, len(seatIDs))
	for i, id := range seatIDs {
		fences[i] = byID[id] // 0 if the hold is already gone — ConfirmSeats will correctly reject
	}
	return fences, nil
}

func (s *Service) markStep(ctx context.Context, orderID uuid.UUID, step, state, lastErr string) {
	_ = s.q.UpdateSagaStep(ctx, db.UpdateSagaStepParams{
		OrderID: orderID, Step: step, State: state, LastError: toPgText(lastErr),
	})
}

func (s *Service) fail(ctx context.Context, orderID uuid.UUID, code, detail string) {
	_ = s.q.FailOrder(ctx, db.FailOrderParams{OrderID: orderID, FailureCode: toPgText(code), FailureDetail: toPgText(detail)})
}

// chargeIdempotencyKey is deterministic per (user, order, hold) — stable
// across retries of the SAME logical checkout attempt, different for a
// genuinely new one (a new orderID) — docs/plan.md "The saga".
func chargeIdempotencyKey(userSub string, orderID, holdID uuid.UUID) string {
	sum := sha256.Sum256([]byte(userSub + ":" + orderID.String() + ":" + holdID.String()))
	return hex.EncodeToString(sum[:])
}

func toPgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
func toPgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
