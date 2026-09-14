// The crux (docs/plan.md "The crux: one source of truth for canvas and
// hidden DOM"): every seat has exactly one dense integer identity — its
// ordinal — which is simultaneously its index in these columnar arrays,
// its index in the 2-bit bitset, its payload in the quadtree, and the key
// of its hidden-DOM gridcell. Ordinals run section -> row -> seat, so every
// row and section is a CONTIGUOUS range. This module only builds the
// (immutable) index; the (mutable) availability bitset lives separately
// (seatmap/model/bitset.ts) — see SeatIndex's own doc comment for why.

export interface LayoutMeta {
  layoutVersion: number
  venueId: number
  seatCount: number
  bbox: [number, number, number, number]
  sections: SectionMeta[]
  rows: RowMeta[]
  // tiers[i] is the tier name for seats.bin's tierIdx==i — the ONLY
  // correct way to resolve a seat's tierIdx to a price via the pricing
  // endpoint's tiers (keyed by name, in a DIFFERENT — alphabetical — order).
  tiers: string[]
}

export interface SectionMeta {
  idx: number
  sectionId: number
  name: string
  tier: string
  polygon: number[]
  firstRow: number
  rowCount: number
  firstSeat: number
  seatCount: number
}

export interface RowMeta {
  idx: number
  sectionIdx: number
  label: string
  firstSeat: number
  seatCount: number
}

// SeatIndex is built ONCE from layout.json + seats.bin and never mutated
// after that. The only mutable thing anywhere in the seat-map model is the
// availability bitset (bitset.ts) — kept as a wholly separate array so
// "rebuild the index" and "apply a delta" are never the same code path.
export interface SeatIndex {
  count: number
  layoutVersion: number
  venueId: number
  meta: LayoutMeta

  // Columnar seat attributes, index = ordinal — parsed straight out of
  // seats.bin's typed-array layout (a memcpy, not a parse).
  x: Int16Array
  y: Int16Array
  seatNumber: Uint16Array
  tierIdx: Uint8Array
}

const SEATS_BIN_MAGIC = 'SEAT'
const SEATS_BIN_HEADER_LEN = 12

// parseSeatsBin mirrors internal/catalog.EncodeSeatsBin byte-for-byte:
//   offset 0  magic "SEAT" (4 bytes)
//   offset 4  version u8 | reserved u8 | layoutVersion u16 (LE)
//   offset 8  seatCount u32
//   offset 12 x           int16[seatCount]
//   offset .. y           int16[seatCount]
//   offset .. seatNumber  uint16[seatCount]
//   offset .. tierIdx     uint8[seatCount]
function parseSeatsBin(buf: ArrayBuffer): {
  layoutVersion: number
  seatCount: number
  x: Int16Array
  y: Int16Array
  seatNumber: Uint16Array
  tierIdx: Uint8Array
} {
  const view = new DataView(buf)
  const magic = String.fromCharCode(view.getUint8(0), view.getUint8(1), view.getUint8(2), view.getUint8(3))
  if (magic !== SEATS_BIN_MAGIC) {
    throw new Error(`seats.bin: bad magic ${JSON.stringify(magic)}, want "SEAT"`)
  }
  const layoutVersion = view.getUint16(6, true)
  const seatCount = view.getUint32(8, true)

  let off = SEATS_BIN_HEADER_LEN
  // Int16Array/Uint16Array construction requires 2-byte alignment; the
  // header is 12 bytes (already a multiple of 4), so this is always safe
  // without a copy.
  const x = new Int16Array(buf, off, seatCount)
  off += seatCount * 2
  const y = new Int16Array(buf, off, seatCount)
  off += seatCount * 2
  const seatNumber = new Uint16Array(buf, off, seatCount)
  off += seatCount * 2
  const tierIdx = new Uint8Array(buf, off, seatCount)

  return { layoutVersion, seatCount, x, y, seatNumber, tierIdx }
}

export function buildSeatIndex(meta: LayoutMeta, seatsBinBuffer: ArrayBuffer): SeatIndex {
  const bin = parseSeatsBin(seatsBinBuffer)
  if (bin.seatCount !== meta.seatCount) {
    throw new Error(`seats.bin seatCount (${bin.seatCount}) != layout.json seatCount (${meta.seatCount})`)
  }
  if (bin.layoutVersion !== meta.layoutVersion) {
    // Layout version skew is a correctness hazard, not cosmetic
    // (docs/plan.md "Static layout format") — refuse rather than
    // silently render every seat as the wrong seat.
    throw new Error(`seats.bin layoutVersion (${bin.layoutVersion}) != layout.json layoutVersion (${meta.layoutVersion})`)
  }

  return {
    count: bin.seatCount,
    layoutVersion: meta.layoutVersion,
    venueId: meta.venueId,
    meta,
    x: bin.x,
    y: bin.y,
    seatNumber: bin.seatNumber,
    tierIdx: bin.tierIdx,
  }
}

/** The row a given ordinal belongs to — a linear scan is fine at the row
 * count this app deals with (tens, not thousands); switch to a binary
 * search over firstSeat if that ever stops being true. */
export function rowOfOrdinal(index: SeatIndex, ordinal: number): RowMeta {
  for (const row of index.meta.rows) {
    if (ordinal >= row.firstSeat && ordinal < row.firstSeat + row.seatCount) return row
  }
  throw new Error(`ordinal ${ordinal} out of range`)
}

export function sectionOfOrdinal(index: SeatIndex, ordinal: number): SectionMeta {
  for (const section of index.meta.sections) {
    if (ordinal >= section.firstSeat && ordinal < section.firstSeat + section.seatCount) return section
  }
  throw new Error(`ordinal ${ordinal} out of range`)
}
