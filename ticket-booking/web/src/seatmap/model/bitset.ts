// The 2-bit-per-seat availability bitset — the wire format's client side.
// Mirrors internal/seatmap/seatmap.go byte-for-byte: byte = ordinal >> 2,
// shift = (ordinal & 3) * 2, little-end-within-byte. Values are LOCKED by
// the backend contract — never renumber them.

export const STATE_FREE = 0
export const STATE_HELD = 1
export const STATE_SOLD = 2
export const STATE_UNAVAILABLE = 3

export type SeatState = 0 | 1 | 2 | 3

export function byteLen(count: number): number {
  return Math.ceil(count / 4)
}

export function newBitset(count: number): Uint8Array {
  return new Uint8Array(byteLen(count))
}

export function getState(packed: Uint8Array, ordinal: number): SeatState {
  const b = ordinal >> 2
  const shift = (ordinal & 3) * 2
  return ((packed[b] >> shift) & 0b11) as SeatState
}

export function setState(packed: Uint8Array, ordinal: number, state: SeatState): void {
  const b = ordinal >> 2
  const shift = (ordinal & 3) * 2
  packed[b] = (packed[b] & ~(0b11 << shift)) | (state << shift)
}

// decodeSnapshot parses a 0x01 SNAPSHOT frame's payload (after the header)
// into a fresh bitset — used both for the initial HTTP availability fetch
// (which is just the raw packed bytes, no frame header) and, from Phase 7,
// the WS snapshot frame's body.
export function decodeSnapshot(bytes: Uint8Array): Uint8Array {
  return bytes.slice()
}
