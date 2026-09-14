// Package catalog builds the two static layout artifacts every event
// publish produces — docs/plan.md "Static layout format": layout.json
// (section/row metadata, ~20 KB) and seats.bin (a columnar binary blob of
// per-seat coordinates, ~250 KB raw). Both are served from S3/MinIO at an
// immutable versioned path; this package only builds the bytes, cmd/event-
// publisher owns writing them and the corresponding event_seats rows from
// the SAME walk (ListVenueSeatsOrdered) so the two can never disagree.
package catalog

import (
	"encoding/binary"
	"fmt"

	"ticketing/internal/db"
)

// LayoutMeta is layout.json.
type LayoutMeta struct {
	LayoutVersion int           `json:"layoutVersion"`
	VenueID       int64         `json:"venueId"`
	SeatCount     int           `json:"seatCount"`
	BBox          [4]int32      `json:"bbox"` // [minX, minY, maxX, maxY]
	Sections      []SectionMeta `json:"sections"`
	Rows          []RowMeta     `json:"rows"`
	// Tiers[i] is the tier name for seats.bin's tierIdx==i, in the SAME
	// first-seen-during-the-walk order EncodeSeatsBin's tierIdx uses. A
	// consumer must use THIS array to resolve tierIdx -> price (via the
	// pricing endpoint's tiers, keyed by name) — GET .../pricing's own
	// tier order (alphabetical) does NOT match tierIdx order, and nothing
	// else in this payload carries that mapping.
	Tiers []string `json:"tiers"`
}

type SectionMeta struct {
	Idx int `json:"idx"`
	// SectionID is the DB id — the join key GET /events/{id}/pricing's
	// closedSections uses, so a consumer can map a closed section back to
	// this array without a second lookup.
	SectionID int64  `json:"sectionId"`
	Name      string `json:"name"`
	Tier      string `json:"tier"`
	// Flat x,y pairs. A hand-authored venue would carry a real polygon;
	// this is the bounding rectangle of the section's own seats — an
	// honest simplification for a synthetically generated venue (see
	// scripts/seed-venue), documented rather than faked as hand-drawn.
	Polygon   []int32 `json:"polygon"`
	FirstRow  int     `json:"firstRow"`
	RowCount  int     `json:"rowCount"`
	FirstSeat int     `json:"firstSeat"`
	SeatCount int     `json:"seatCount"`
}

type RowMeta struct {
	Idx        int    `json:"idx"`
	SectionIdx int    `json:"sectionIdx"`
	Label      string `json:"label"`
	FirstSeat  int    `json:"firstSeat"`
	SeatCount  int    `json:"seatCount"`
}

// Built is everything one BuildLayout call produces: the JSON metadata, the
// seats.bin bytes, and the per-seat ordinal/tier assignment cmd/event-
// publisher needs to populate event_seats — all from one walk, so ordinal
// assignment and the rendered files are guaranteed consistent.
type Built struct {
	Meta     LayoutMeta
	SeatsBin []byte
	// SeatOrdinal[i] is seats.bin/bitset ordinal i's underlying seat_id —
	// the ordinal IS the slice index, by construction (docs/plan.md
	// decision #2: ordinals run section -> row -> seat, frozen per version).
	SeatID []int64
	// Tier[i] is the tier name for ordinal i, for the caller to resolve to
	// a price via event_price_tiers.
	Tier []string
	// SectionClosed[i] is the venue-template default sellability for
	// ordinal i's section, for a fresh event's initial event_seats.sellable.
	SectionClosed []bool
}

// BuildLayout walks ListVenueSeatsOrdered's rows (already ordered section ->
// row -> seat_label, per docs/plan.md decision #2) and builds both static
// artifacts in one pass.
func BuildLayout(venueID int64, layoutVersion int, rows []db.ListVenueSeatsOrderedRow) (Built, error) {
	if len(rows) == 0 {
		return Built{}, fmt.Errorf("build layout: venue %d has no seats", venueID)
	}

	var (
		meta          = LayoutMeta{LayoutVersion: layoutVersion, VenueID: venueID}
		x             = make([]int16, 0, len(rows))
		y             = make([]int16, 0, len(rows))
		seatNumber    = make([]uint16, 0, len(rows))
		tierIdx       = make([]uint8, 0, len(rows))
		seatID        = make([]int64, 0, len(rows))
		tierOf        = make([]string, 0, len(rows))
		sectionClosed = make([]bool, 0, len(rows))

		tierOrder  = map[string]uint8{} // first-seen order, stable given fixed section ordering
		minX, minY = int32(rows[0].XCoord), int32(rows[0].YCoord)
		maxX, maxY = minX, minY

		curSection int64 = -1
		curRow     int64 = -1
		sectionIdx       = -1
		rowIdx           = -1
	)

	for ord, r := range rows {
		if r.SectionID != curSection {
			curSection = r.SectionID
			sectionIdx++
			meta.Sections = append(meta.Sections, SectionMeta{
				Idx: sectionIdx, SectionID: r.SectionID, Name: r.SectionName, Tier: r.SectionTier,
				FirstRow: rowIdx + 1, FirstSeat: ord,
			})
		}
		if r.RowID != curRow {
			curRow = r.RowID
			rowIdx++
			meta.Rows = append(meta.Rows, RowMeta{
				Idx: rowIdx, SectionIdx: sectionIdx, Label: r.RowLabel, FirstSeat: ord,
			})
		}
		meta.Sections[sectionIdx].RowCount = rowIdx + 1 - meta.Sections[sectionIdx].FirstRow
		meta.Sections[sectionIdx].SeatCount++
		meta.Rows[rowIdx].SeatCount++

		if _, ok := tierOrder[r.SectionTier]; !ok {
			tierOrder[r.SectionTier] = uint8(len(tierOrder))
		}

		xi, yi := int32(r.XCoord), int32(r.YCoord)
		if xi < minX {
			minX = xi
		}
		if xi > maxX {
			maxX = xi
		}
		if yi < minY {
			minY = yi
		}
		if yi > maxY {
			maxY = yi
		}

		x = append(x, int16(r.XCoord))
		y = append(y, int16(r.YCoord))
		seatNumber = append(seatNumber, seatLabelToNumber(r.SeatLabel, ord))
		tierIdx = append(tierIdx, tierOrder[r.SectionTier])
		seatID = append(seatID, r.SeatID)
		tierOf = append(tierOf, r.SectionTier)
		sectionClosed = append(sectionClosed, r.SectionClosed)
	}

	// Bounding-rect polygon per section, from the seats actually in it —
	// see SectionMeta.Polygon's doc comment for why this isn't hand-drawn.
	secMinMax := make([][4]int32, len(meta.Sections))
	for i := range secMinMax {
		secMinMax[i] = [4]int32{1 << 30, 1 << 30, -(1 << 30), -(1 << 30)}
	}
	si := -1
	curSection = -1
	for _, r := range rows {
		if r.SectionID != curSection {
			curSection = r.SectionID
			si++
		}
		xi, yi := int32(r.XCoord), int32(r.YCoord)
		mm := &secMinMax[si]
		if xi < mm[0] {
			mm[0] = xi
		}
		if yi < mm[1] {
			mm[1] = yi
		}
		if xi > mm[2] {
			mm[2] = xi
		}
		if yi > mm[3] {
			mm[3] = yi
		}
	}
	for i, mm := range secMinMax {
		meta.Sections[i].Polygon = []int32{mm[0], mm[1], mm[2], mm[1], mm[2], mm[3], mm[0], mm[3]}
	}

	meta.SeatCount = len(rows)
	meta.BBox = [4]int32{minX, minY, maxX, maxY}
	meta.Tiers = make([]string, len(tierOrder))
	for tier, idx := range tierOrder {
		meta.Tiers[idx] = tier
	}

	return Built{
		Meta:          meta,
		SeatsBin:      EncodeSeatsBin(x, y, seatNumber, tierIdx, uint16(layoutVersion)),
		SeatID:        seatID,
		Tier:          tierOf,
		SectionClosed: sectionClosed,
	}, nil
}

// seatLabelToNumber best-effort-parses a numeric seat label for the binary
// format's Uint16 seatNumber field, falling back to a 1-based running
// position when the label isn't purely numeric (e.g. hand-authored venues
// with labels like "4A"). layout.json's RowMeta/seat_label text is always
// the source of truth for display; seatNumber in seats.bin is a fast-path
// hint the renderer can skip re-deriving.
func seatLabelToNumber(label string, fallback int) uint16 {
	var n uint16
	for _, c := range label {
		if c < '0' || c > '9' {
			return uint16(fallback + 1)
		}
		n = n*10 + uint16(c-'0')
	}
	if len(label) == 0 {
		return uint16(fallback + 1)
	}
	return n
}

// EncodeSeatsBin packs the columnar seat arrays per docs/plan.md's format:
//
//	offset 0  magic "SEAT" (4 bytes)
//	offset 4  version u8 | reserved u8 | layoutVersion u16 (LE)
//	offset 8  seatCount u32
//	offset 12 x           int16[seatCount]
//	offset .. y           int16[seatCount]
//	offset .. seatNumber  uint16[seatCount]
//	offset .. tierIdx     uint8[seatCount]
func EncodeSeatsBin(x, y []int16, seatNumber []uint16, tierIdx []uint8, layoutVersion uint16) []byte {
	n := len(x)
	buf := make([]byte, 12+n*2+n*2+n*2+n)

	copy(buf[0:4], "SEAT")
	buf[4] = 1 // format version
	buf[5] = 0 // reserved
	binary.LittleEndian.PutUint16(buf[6:8], layoutVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(n))

	off := 12
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(buf[off+i*2:], uint16(x[i]))
	}
	off += n * 2
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(buf[off+i*2:], uint16(y[i]))
	}
	off += n * 2
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(buf[off+i*2:], seatNumber[i])
	}
	off += n * 2
	copy(buf[off:], tierIdx)

	return buf
}
