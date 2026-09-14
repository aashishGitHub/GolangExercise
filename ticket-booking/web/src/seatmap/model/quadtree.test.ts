import { describe, expect, it } from 'vitest'
import { buildQuadtree, nearestSeat, queryRect } from './quadtree'
import type { LayoutMeta, SeatIndex } from './seatIndex'

// A synthetic grid: 20x20 seats on a 1-unit pitch, so results are
// hand-verifiable rather than approximate.
function gridIndex(size: number): SeatIndex {
  const count = size * size
  const x = new Int16Array(count)
  const y = new Int16Array(count)
  let i = 0
  for (let row = 0; row < size; row++) {
    for (let col = 0; col < size; col++) {
      x[i] = col
      y[i] = row
      i++
    }
  }
  const meta: LayoutMeta = {
    layoutVersion: 1,
    venueId: 1,
    seatCount: count,
    bbox: [0, 0, size - 1, size - 1],
    sections: [],
    rows: [],
    tiers: [],
  }
  return { count, layoutVersion: 1, venueId: 1, meta, x, y, seatNumber: new Uint16Array(count), tierIdx: new Uint8Array(count) }
}

describe('queryRect', () => {
  it('returns exactly the ordinals within the query rectangle on a known grid', () => {
    const index = gridIndex(20) // 400 seats, forces the tree past one leaf
    const tree = buildQuadtree(index)

    const got = queryRect(tree, index, 5, 5, 7, 7).sort((a, b) => a - b)
    // Expect the 3x3 block of seats at columns/rows 5..7.
    const want: number[] = []
    for (let row = 5; row <= 7; row++) {
      for (let col = 5; col <= 7; col++) want.push(row * 20 + col)
    }
    want.sort((a, b) => a - b)

    expect(got).toEqual(want)
  })

  it('returns nothing for a rect entirely outside the bbox', () => {
    const index = gridIndex(20)
    const tree = buildQuadtree(index)
    expect(queryRect(tree, index, 1000, 1000, 2000, 2000)).toEqual([])
  })

  it('returns every seat for a rect covering the whole bbox', () => {
    const index = gridIndex(10)
    const tree = buildQuadtree(index)
    const got = queryRect(tree, index, 0, 0, 9, 9)
    expect(got.length).toBe(100)
  })
})

describe('nearestSeat', () => {
  it('finds the exact seat when queried at its own coordinates', () => {
    const index = gridIndex(20)
    const tree = buildQuadtree(index)
    // ordinal for (col=12,row=7) is row*20+col = 152
    expect(nearestSeat(tree, index, 12, 7)).toBe(152)
  })

  it('finds the closest seat to an off-grid point', () => {
    const index = gridIndex(20)
    const tree = buildQuadtree(index)
    // (12.3, 7.4) is closest to (12,7) -> ordinal 152
    expect(nearestSeat(tree, index, 12.3, 7.4)).toBe(152)
  })

  it('returns null for an empty tree', () => {
    const index = gridIndex(0)
    const tree = buildQuadtree(index)
    expect(nearestSeat(tree, index, 0, 0)).toBeNull()
  })
})
