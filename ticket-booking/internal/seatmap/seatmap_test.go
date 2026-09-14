package seatmap

import (
	"testing"
)

func TestByteLen(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 3: 1, 4: 1, 5: 2, 30000: 7500, 50000: 12500}
	for count, want := range cases {
		if got := ByteLen(count); got != want {
			t.Errorf("ByteLen(%d) = %d, want %d", count, got, want)
		}
	}
}

func TestSetGet_AllOrdinalsInByte(t *testing.T) {
	// Ordinals 0-3 share one byte; verify each sub-position round-trips
	// without disturbing its neighbors — this is where an off-by-one in the
	// shift math would silently corrupt an adjacent seat.
	packed := New(4)
	states := []State{StateHeld, StateSold, StateUnavailable, StateFree}
	for ord, s := range states {
		Set(packed, ord, s)
	}
	for ord, want := range states {
		if got := Get(packed, ord); got != want {
			t.Errorf("ordinal %d: Get = %d, want %d (packed=%08b)", ord, got, want, packed[0])
		}
	}
}

func TestSetGet_RoundTripAllOrdinals(t *testing.T) {
	const count = 337 // deliberately not a multiple of 4
	packed := New(count)
	for ord := 0; ord < count; ord++ {
		Set(packed, ord, State(ord%4))
	}
	for ord := 0; ord < count; ord++ {
		want := State(ord % 4)
		if got := Get(packed, ord); got != want {
			t.Fatalf("ordinal %d: Get = %d, want %d", ord, got, want)
		}
	}
}

func TestSet_DoesNotDisturbNeighbors(t *testing.T) {
	packed := New(8)
	for i := range packed {
		packed[i] = 0xFF // every ordinal starts at state 3 (UNAVAILABLE)
	}
	Set(packed, 5, StateFree)
	for ord := 0; ord < 8; ord++ {
		want := StateUnavailable
		if ord == 5 {
			want = StateFree
		}
		if got := Get(packed, ord); got != want {
			t.Errorf("ordinal %d: Get = %d, want %d", ord, got, want)
		}
	}
}

func TestNew_AllFree(t *testing.T) {
	packed := New(100)
	for ord := 0; ord < 100; ord++ {
		if got := Get(packed, ord); got != StateFree {
			t.Fatalf("ordinal %d: Get = %d, want StateFree on a fresh bitset", ord, got)
		}
	}
}

func TestStateFromDB(t *testing.T) {
	cases := []struct {
		status   int16
		sellable bool
		want     State
	}{
		{0, true, StateFree},
		{1, true, StateHeld},
		{2, true, StateSold},
		{3, true, StateHeld}, // PENDING_PAYMENT projects to wire HELD
		{0, false, StateUnavailable},
		{1, false, StateUnavailable}, // sellable=false wins regardless of status
		{2, false, StateUnavailable},
		{99, true, StateUnavailable}, // unknown status fails toward unsellable
	}
	for _, c := range cases {
		if got := StateFromDB(c.status, c.sellable); got != c.want {
			t.Errorf("StateFromDB(%d, %v) = %d, want %d", c.status, c.sellable, got, c.want)
		}
	}
}
