import { describe, expect, it } from 'vitest'
import { buildSeatIndex, rowOfOrdinal, sectionOfOrdinal, type LayoutMeta } from './seatIndex'

// encodeSeatsBin mirrors internal/catalog.EncodeSeatsBin exactly — the same
// byte layout the Go backend produces, so this test proves the TS parser
// reads what the Go encoder writes, not just what the TS encoder itself
// happens to produce (an encode/decode pair in the same language can both
// be wrong the same way and still "pass").
function encodeSeatsBin(x: number[], y: number[], seatNumber: number[], tierIdx: number[], layoutVersion: number): ArrayBuffer {
  const n = x.length
  const buf = new ArrayBuffer(12 + n * 2 + n * 2 + n * 2 + n)
  const view = new DataView(buf)
  view.setUint8(0, 'S'.charCodeAt(0))
  view.setUint8(1, 'E'.charCodeAt(0))
  view.setUint8(2, 'A'.charCodeAt(0))
  view.setUint8(3, 'T'.charCodeAt(0))
  view.setUint8(4, 1) // format version
  view.setUint8(5, 0) // reserved
  view.setUint16(6, layoutVersion, true)
  view.setUint32(8, n, true)

  let off = 12
  for (let i = 0; i < n; i++) view.setInt16(off + i * 2, x[i], true)
  off += n * 2
  for (let i = 0; i < n; i++) view.setInt16(off + i * 2, y[i], true)
  off += n * 2
  for (let i = 0; i < n; i++) view.setUint16(off + i * 2, seatNumber[i], true)
  off += n * 2
  for (let i = 0; i < n; i++) view.setUint8(off + i, tierIdx[i])

  return buf
}

function fixtureMeta(seatCount: number, layoutVersion = 3): LayoutMeta {
  return {
    layoutVersion,
    venueId: 1,
    seatCount,
    bbox: [0, 0, 100, 100],
    sections: [{ idx: 0, sectionId: 1, name: 'A', tier: 'floor', polygon: [], firstRow: 0, rowCount: 1, firstSeat: 0, seatCount }],
    rows: [{ idx: 0, sectionIdx: 0, label: 'Row 1', firstSeat: 0, seatCount }],
    tiers: ['floor'],
  }
}

describe('buildSeatIndex', () => {
  it('parses a Go-encoded seats.bin into typed-array views matching the source values', () => {
    const x = [10, -20, 30]
    const y = [1, 2, 3]
    const seatNumber = [1, 2, 3]
    const tierIdx = [0, 1, 0]
    const buf = encodeSeatsBin(x, y, seatNumber, tierIdx, 3)

    const index = buildSeatIndex(fixtureMeta(3), buf)

    expect(index.count).toBe(3)
    expect(Array.from(index.x)).toEqual(x)
    expect(Array.from(index.y)).toEqual(y) // proves negative int16 (-20) round-trips
    expect(Array.from(index.seatNumber)).toEqual(seatNumber)
    expect(Array.from(index.tierIdx)).toEqual(tierIdx)
  })

  it('rejects a bad magic', () => {
    const buf = new ArrayBuffer(16)
    new DataView(buf).setUint32(0, 0, true) // not "SEAT"
    expect(() => buildSeatIndex(fixtureMeta(1), buf)).toThrow(/magic/)
  })

  it('refuses to build when layoutVersion disagrees between layout.json and seats.bin', () => {
    const buf = encodeSeatsBin([1], [1], [1], [0], 7) // seats.bin says version 7
    expect(() => buildSeatIndex(fixtureMeta(1, 3), buf)).toThrow(/layoutVersion/) // layout.json says 3
  })

  it('refuses to build when seatCount disagrees', () => {
    const buf = encodeSeatsBin([1, 2], [1, 2], [1, 2], [0, 0], 3)
    expect(() => buildSeatIndex(fixtureMeta(5, 3), buf)).toThrow(/seatCount/)
  })
})

describe('rowOfOrdinal / sectionOfOrdinal', () => {
  it('finds the right row and section for an ordinal within a contiguous range', () => {
    const meta: LayoutMeta = {
      layoutVersion: 1,
      venueId: 1,
      seatCount: 6,
      bbox: [0, 0, 10, 10],
      sections: [
        { idx: 0, sectionId: 1, name: 'A', tier: 'floor', polygon: [], firstRow: 0, rowCount: 1, firstSeat: 0, seatCount: 3 },
        { idx: 1, sectionId: 2, name: 'B', tier: 'upper', polygon: [], firstRow: 1, rowCount: 1, firstSeat: 3, seatCount: 3 },
      ],
      rows: [
        { idx: 0, sectionIdx: 0, label: 'Row 1', firstSeat: 0, seatCount: 3 },
        { idx: 1, sectionIdx: 1, label: 'Row 1', firstSeat: 3, seatCount: 3 },
      ],
      tiers: ['floor', 'upper'],
    }
    const buf = encodeSeatsBin([0, 1, 2, 3, 4, 5], [0, 0, 0, 0, 0, 0], [1, 2, 3, 1, 2, 3], [0, 0, 0, 1, 1, 1], 1)
    const index = buildSeatIndex(meta, buf)

    expect(sectionOfOrdinal(index, 0).name).toBe('A')
    expect(sectionOfOrdinal(index, 3).name).toBe('B') // first ordinal of the second section
    expect(rowOfOrdinal(index, 5).label).toBe('Row 1')
    expect(rowOfOrdinal(index, 5).idx).toBe(1)
  })
})
