// Package seatmap implements the 2-bit-per-seat availability bitset —
// docs/plan.md "The 2-bit availability bitset and wire deltas": 30,000 seats
// pack into 7,500 bytes, 50,000 into 12,500. Packing is little-end-within-
// byte: ordinal 0 occupies bits 0-1, ordinal 3 occupies bits 6-7.
//
//	byte  = ordinal >> 2
//	shift = (ordinal & 3) * 2
//	state = (bytes[byte] >> shift) & 0b11
package seatmap

// State is one seat's wire-visible availability. These four values are
// locked by docs/plan.md's API contract — never renumber them, the frontend
// depends on the exact bit pattern.
type State uint8

const (
	StateFree        State = 0
	StateHeld        State = 1
	StateSold        State = 2
	StateUnavailable State = 3
)

// ByteLen returns the packed size in bytes for a given seat count.
func ByteLen(count int) int {
	return (count + 3) / 4
}

// New allocates a zeroed (all-FREE) packed bitset for count seats.
func New(count int) []byte {
	return make([]byte, ByteLen(count))
}

// Get reads the state of one ordinal.
func Get(packed []byte, ordinal int) State {
	b := ordinal >> 2
	shift := uint((ordinal & 3) * 2)
	return State((packed[b] >> shift) & 0b11)
}

// Set writes the state of one ordinal in place.
func Set(packed []byte, ordinal int, s State) {
	b := ordinal >> 2
	shift := uint((ordinal & 3) * 2)
	packed[b] = (packed[b] &^ (0b11 << shift)) | (byte(s) << shift)
}

// StateFromDB maps event_seats' DB status + sellable to the wire State.
//
// DB status: 0 AVAILABLE, 1 HELD, 2 BOOKED, 3 PENDING_PAYMENT.
// PENDING_PAYMENT projects to wire HELD — a checkout-in-progress hold looks
// identical to an ordinary hold from the outside. `sellable = false`
// overrides everything to UNAVAILABLE regardless of status — DB status has
// no value that means "structurally unsellable" on its own (docs/plan.md,
// fixed gap #2), so this mapping is the only place that state is produced.
func StateFromDB(dbStatus int16, sellable bool) State {
	if !sellable {
		return StateUnavailable
	}
	switch dbStatus {
	case 0:
		return StateFree
	case 1, 3:
		return StateHeld
	case 2:
		return StateSold
	default:
		// Unknown status: fail toward "not sellable" rather than
		// advertising a seat nobody can actually book.
		return StateUnavailable
	}
}
