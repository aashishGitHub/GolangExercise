import { describe, expect, it } from 'vitest'
import { STATE_FREE, STATE_HELD, STATE_SOLD, STATE_UNAVAILABLE, byteLen, getState, newBitset, setState } from './bitset'

describe('byteLen', () => {
  it.each([
    [0, 0],
    [1, 1],
    [3, 1],
    [4, 1],
    [5, 2],
    [30000, 7500],
    [50000, 12500],
  ])('byteLen(%i) = %i', (count, want) => {
    expect(byteLen(count)).toBe(want)
  })
})

it('setState/getState round-trips every ordinal in one byte without disturbing neighbors', () => {
  // The exact scenario that would catch an off-by-one in the shift math —
  // mirrors internal/seatmap/seatmap_test.go's Go-side equivalent test, so
  // both implementations of the wire format are independently proven.
  const packed = newBitset(4)
  const states = [STATE_HELD, STATE_SOLD, STATE_UNAVAILABLE, STATE_FREE] as const
  states.forEach((s, ord) => setState(packed, ord, s))
  states.forEach((want, ord) => {
    expect(getState(packed, ord)).toBe(want)
  })
})

it('setState/getState round-trips across a non-multiple-of-4 seat count', () => {
  const count = 337
  const packed = newBitset(count)
  for (let ord = 0; ord < count; ord++) {
    setState(packed, ord, (ord % 4) as 0 | 1 | 2 | 3)
  }
  for (let ord = 0; ord < count; ord++) {
    expect(getState(packed, ord)).toBe(ord % 4)
  }
})

it('a fresh bitset is all FREE', () => {
  const packed = newBitset(100)
  for (let ord = 0; ord < 100; ord++) {
    expect(getState(packed, ord)).toBe(STATE_FREE)
  }
})

it('setState only touches its own ordinal', () => {
  const packed = newBitset(8)
  packed.fill(0xff) // every ordinal starts at state 3 (UNAVAILABLE)
  setState(packed, 5, STATE_FREE)
  for (let ord = 0; ord < 8; ord++) {
    expect(getState(packed, ord)).toBe(ord === 5 ? STATE_FREE : STATE_UNAVAILABLE)
  }
})
