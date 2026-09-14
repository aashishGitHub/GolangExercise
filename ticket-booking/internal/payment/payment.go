// Package payment is a fake payment provider with injectable pathology —
// real dependencies with real failure modes matter more than a happy-path
// stub (docs/plan.md "internal/payment fake provider"). Defaults to
// all-zero rates so ordinary dev stays clean; the load harness (Phase 10)
// and the reconciler's own tests turn these on.
package payment

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
)

type Status string

const (
	StatusCaptured Status = "CAPTURED"
	StatusFailed   Status = "FAILED"
	// StatusUnknown: the charge may or may not have actually captured —
	// the only honest response to a provider timeout (deep-dive.md §6).
	// "Never blindly retry a raw charge": the caller must re-send with the
	// SAME idempotency key or query status, never issue a fresh charge.
	StatusUnknown Status = "UNKNOWN"
)

type ChargeResult struct {
	ProviderRef string
	Status      Status
}

// Provider is the interface internal/order depends on — small enough to
// fake by hand in tests without a real payment gateway SDK.
type Provider interface {
	// Charge is idempotent on idempotencyKey: calling it twice with the
	// same key against THIS fake returns the same result both times
	// (mirroring a real provider's own idempotency-key support — deep-dive
	// §6's "two independent layers", the DB unique constraint being the
	// first).
	Charge(ctx context.Context, idempotencyKey string, amountCents int32) (ChargeResult, error)
	Refund(ctx context.Context, providerRef string, amountCents int32) (refundRef string, err error)
}

// FakeProvider injects failure/timeout/ambiguity at configurable rates,
// all in [0,1]. AmbiguousRate is the only way to genuinely exercise the
// reconciler: it CAPTURES the charge for real (in its own memory) but
// still reports StatusUnknown, exactly like a real provider timeout that
// actually succeeded server-side.
type FakeProvider struct {
	FailRate      float64
	TimeoutRate   float64 // reported as StatusUnknown, NOT actually captured
	AmbiguousRate float64 // reported as StatusUnknown, but IS actually captured

	captured map[string]bool // idempotencyKey -> true, once actually captured
}

func NewFakeProvider() *FakeProvider {
	return &FakeProvider{captured: make(map[string]bool)}
}

func (p *FakeProvider) Charge(ctx context.Context, idempotencyKey string, amountCents int32) (ChargeResult, error) {
	// Idempotent replay: if this key already captured, return the same
	// result again rather than charging twice.
	if p.captured[idempotencyKey] {
		return ChargeResult{ProviderRef: "fake_" + idempotencyKey, Status: StatusCaptured}, nil
	}

	if roll() < p.FailRate {
		return ChargeResult{Status: StatusFailed}, nil
	}
	if roll() < p.AmbiguousRate {
		p.captured[idempotencyKey] = true                                                      // captures for real...
		return ChargeResult{ProviderRef: "fake_" + idempotencyKey, Status: StatusUnknown}, nil // ...but reports unknown
	}
	if roll() < p.TimeoutRate {
		return ChargeResult{Status: StatusUnknown}, nil // genuinely never captured
	}

	p.captured[idempotencyKey] = true
	return ChargeResult{ProviderRef: "fake_" + idempotencyKey, Status: StatusCaptured}, nil
}

func (p *FakeProvider) Refund(ctx context.Context, providerRef string, amountCents int32) (string, error) {
	return "refund_" + providerRef, nil
}

// StatusOf lets the reconciler query an ambiguous charge's ACTUAL fate —
// what a real provider's "get charge by idempotency key" API stands in for.
func (p *FakeProvider) StatusOf(idempotencyKey string) Status {
	if p.captured[idempotencyKey] {
		return StatusCaptured
	}
	return StatusFailed
}

// roll returns a uniform float in [0,1) using crypto/rand — this is test
// infrastructure, not a security boundary, but crypto/rand avoids pulling
// in math/rand's global-seed footgun for no benefit.
func roll() float64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		panic(fmt.Sprintf("payment.roll: %v", err))
	}
	return float64(n.Int64()) / float64(int64(1)<<53)
}
