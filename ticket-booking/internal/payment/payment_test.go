package payment

import (
	"context"
	"testing"
)

func TestFakeProvider_HappyPath(t *testing.T) {
	p := NewFakeProvider()
	result, err := p.Charge(context.Background(), "key-1", 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if result.Status != StatusCaptured {
		t.Fatalf("status = %v, want CAPTURED (all rates default to 0)", result.Status)
	}
	if result.ProviderRef == "" {
		t.Error("expected a non-empty provider ref")
	}
}

func TestFakeProvider_ChargeIsIdempotentOnSameKey(t *testing.T) {
	p := NewFakeProvider()
	ctx := context.Background()
	first, _ := p.Charge(ctx, "key-2", 1000)
	second, _ := p.Charge(ctx, "key-2", 1000)
	if first.ProviderRef != second.ProviderRef {
		t.Errorf("provider refs differ across replay: %q vs %q", first.ProviderRef, second.ProviderRef)
	}
	if second.Status != StatusCaptured {
		t.Errorf("replay status = %v, want CAPTURED", second.Status)
	}
}

func TestFakeProvider_AlwaysFails(t *testing.T) {
	p := NewFakeProvider()
	p.FailRate = 1.0
	result, err := p.Charge(context.Background(), "key-3", 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %v, want FAILED", result.Status)
	}
}

func TestFakeProvider_AmbiguousChargeActuallyCapturesButReportsUnknown(t *testing.T) {
	p := NewFakeProvider()
	p.AmbiguousRate = 1.0
	result, err := p.Charge(context.Background(), "key-4", 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if result.Status != StatusUnknown {
		t.Fatalf("reported status = %v, want UNKNOWN", result.Status)
	}
	// The whole point of AmbiguousRate: the reconciler's "query the
	// provider directly" must find it actually captured.
	if got := p.StatusOf("key-4"); got != StatusCaptured {
		t.Fatalf("StatusOf = %v, want CAPTURED (the charge landed even though Charge reported UNKNOWN)", got)
	}
}

func TestFakeProvider_TimeoutNeverCaptures(t *testing.T) {
	p := NewFakeProvider()
	p.TimeoutRate = 1.0
	result, err := p.Charge(context.Background(), "key-5", 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if result.Status != StatusUnknown {
		t.Fatalf("status = %v, want UNKNOWN", result.Status)
	}
	if got := p.StatusOf("key-5"); got != StatusFailed {
		t.Fatalf("StatusOf = %v, want FAILED (a genuine timeout never captured)", got)
	}
}

func TestFakeProvider_Refund(t *testing.T) {
	p := NewFakeProvider()
	ref, err := p.Refund(context.Background(), "fake_key-1", 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ref == "" {
		t.Error("expected a non-empty refund ref")
	}
}
