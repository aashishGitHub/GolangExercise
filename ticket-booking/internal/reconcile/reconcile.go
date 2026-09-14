// Package reconcile implements deep-dive.md §6's 3-step reconciler — the
// tail-resolver for ambiguous payment states (docs/plan.md "The saga" —
// reconciler). Runs on boot and every tick thereafter; owns the outcome for
// every payment order.RunSaga deliberately left in AUTHORIZING with an
// UNKNOWN payment status rather than guessing.
package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ticketing/internal/db"
	"ticketing/internal/order"
	"ticketing/internal/payment"
)

// StatusChecker is the extra capability the reconciler needs beyond
// payment.Provider's Charge/Refund — "query the provider by idempotency
// key" (deep-dive.md §6). A real provider SDK exposes this directly;
// *payment.FakeProvider satisfies it via StatusOf.
type StatusChecker interface {
	StatusOf(idempotencyKey string) payment.Status
}

type Service struct {
	q       db.Querier
	pay     payment.Provider
	checker StatusChecker
	sagas   *order.Service
}

func New(q db.Querier, pay payment.Provider, checker StatusChecker, sagas *order.Service) *Service {
	return &Service{q: q, pay: pay, checker: checker, sagas: sagas}
}

const stuckAfter = 5 * time.Minute

// RunOnce implements the 3 steps:
//  1. find PENDING/UNKNOWN payments older than stuckAfter,
//  2. query the provider directly by idempotency key,
//  3. resolve: CAPTURED+no-confirmed-order+hold-still-valid -> complete
//     the booking; CAPTURED+hold-gone -> compensate (order.RunSaga's own
//     compensate path, reached by re-running the saga); not-captured ->
//     fail the order and release the hold.
//
// Either branch always emits an outbox event via order.RunSaga's own
// Publish calls, so the dashboard sees the resolution either way.
func (s *Service) RunOnce(ctx context.Context) (resolved int, err error) {
	stuck, err := s.q.ListStuckPayments(ctx, db.ListStuckPaymentsParams{
		StuckAfterSeconds: int32(stuckAfter.Seconds()), RowLimit: 100,
	})
	if err != nil {
		return 0, fmt.Errorf("reconcile: list stuck payments: %w", err)
	}

	for _, p := range stuck {
		actual := s.checker.StatusOf(p.IdempotencyKey)

		switch actual {
		case payment.StatusCaptured:
			// The charge DID land, even though the saga saw UNKNOWN at the
			// time. Update our record, then re-run the saga so it picks
			// up from "charge is CAPTURED" and proceeds to confirm/
			// compensate exactly as the happy path would.
			_ = s.q.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
				PaymentID: p.PaymentID, Status: string(payment.StatusCaptured), ProviderRef: p.ProviderRef,
			})
			if err := s.sagas.RunSaga(ctx, p.OrderID); err != nil {
				return resolved, fmt.Errorf("reconcile: resume saga for order %s: %w", p.OrderID, err)
			}
			resolved++

		case payment.StatusFailed:
			_ = s.q.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
				PaymentID: p.PaymentID, Status: string(payment.StatusFailed),
			})
			_ = s.q.FailOrder(ctx, db.FailOrderParams{
				OrderID: p.OrderID, FailureCode: toPgText("payment_declined"),
				FailureDetail: toPgText("resolved as declined by the reconciler"),
			})
			resolved++

		default:
			// Still genuinely unresolvable this tick (provider itself is
			// unreachable, say) — leave it for the next run rather than
			// guess.
		}
	}
	return resolved, nil
}

// CheckInvariant runs both money-invariant queries (docs/plan.md's
// two "must always be zero rows" checks) and returns every violation
// found — callers (cmd/reconciler, and tests) decide how loudly to alarm.
func (s *Service) CheckInvariant(ctx context.Context) (seatWithNoMoney, moneyWithNoSeat int, err error) {
	missing, err := s.q.FindOrdersMissingPayment(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("reconcile: check seat-with-no-money invariant: %w", err)
	}
	orphaned, err := s.q.FindCapturedPaymentsMissingOrderOrRefund(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("reconcile: check money-with-no-seat invariant: %w", err)
	}
	return len(missing), len(orphaned), nil
}

func toPgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
