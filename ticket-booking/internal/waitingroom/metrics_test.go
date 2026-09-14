package waitingroom

import (
	"testing"
	"time"
)

func TestHoldMetrics_P99AndErrorRate(t *testing.T) {
	m := NewHoldMetrics(100)
	for i := 0; i < 99; i++ {
		m.Record(10*time.Millisecond, false)
	}
	m.Record(900*time.Millisecond, true) // the one slow, failed call

	if got := m.ErrorRate(); got < 0.009 || got > 0.011 {
		t.Fatalf("ErrorRate = %v, want ~0.01 (1/100)", got)
	}
	// P99 of 100 samples (99 fast + 1 slow) should land on the slow one.
	if got := m.P99(); got != 900*time.Millisecond {
		t.Fatalf("P99 = %v, want 900ms", got)
	}
}

func TestHoldMetrics_EmptyIsZero(t *testing.T) {
	m := NewHoldMetrics(10)
	if got := m.P99(); got != 0 {
		t.Fatalf("P99 on empty = %v, want 0", got)
	}
	if got := m.ErrorRate(); got != 0 {
		t.Fatalf("ErrorRate on empty = %v, want 0", got)
	}
}

func TestHoldMetrics_RingBufferWrapsAndForgetsOldSamples(t *testing.T) {
	m := NewHoldMetrics(10)
	// Fill with 10 failures, then overwrite all of them with successes —
	// the ring buffer must forget the old failures entirely, not average
	// them in forever.
	for i := 0; i < 10; i++ {
		m.Record(time.Millisecond, true)
	}
	if got := m.ErrorRate(); got != 1.0 {
		t.Fatalf("ErrorRate after 10 failures = %v, want 1.0", got)
	}
	for i := 0; i < 10; i++ {
		m.Record(time.Millisecond, false)
	}
	if got := m.ErrorRate(); got != 0.0 {
		t.Fatalf("ErrorRate after wraparound = %v, want 0.0 (old failures forgotten)", got)
	}
}
