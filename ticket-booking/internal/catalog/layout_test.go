package catalog

import (
	"encoding/binary"
	"testing"

	"ticketing/internal/db"
)

func TestEncodeSeatsBin_HeaderAndRoundTrip(t *testing.T) {
	x := []int16{10, -20, 30}
	y := []int16{1, 2, 3}
	seatNumber := []uint16{1, 2, 3}
	tierIdx := []uint8{0, 1, 0}

	buf := EncodeSeatsBin(x, y, seatNumber, tierIdx, 7)

	if string(buf[0:4]) != "SEAT" {
		t.Fatalf("magic = %q, want SEAT", buf[0:4])
	}
	if buf[4] != 1 {
		t.Errorf("format version = %d, want 1", buf[4])
	}
	if got := binary.LittleEndian.Uint16(buf[6:8]); got != 7 {
		t.Errorf("layoutVersion = %d, want 7", got)
	}
	if got := binary.LittleEndian.Uint32(buf[8:12]); got != 3 {
		t.Errorf("seatCount = %d, want 3", got)
	}

	wantLen := 12 + 3*2 + 3*2 + 3*2 + 3
	if len(buf) != wantLen {
		t.Fatalf("len(buf) = %d, want %d", len(buf), wantLen)
	}

	// Decode x[1] = -20 back out, proving the int16 round-trips through
	// LittleEndian.PutUint16's uint16 cast correctly (two's complement).
	off := 12 + 2 // x[1]
	gotX1 := int16(binary.LittleEndian.Uint16(buf[off : off+2]))
	if gotX1 != -20 {
		t.Errorf("x[1] round-tripped as %d, want -20", gotX1)
	}
}

func TestSeatLabelToNumber(t *testing.T) {
	cases := []struct {
		label    string
		fallback int
		want     uint16
	}{
		{"42", 0, 42},
		{"1", 99, 1},
		{"4A", 5, 6}, // non-numeric -> fallback+1
		{"", 5, 6},   // empty -> fallback+1
	}
	for _, c := range cases {
		if got := seatLabelToNumber(c.label, c.fallback); got != c.want {
			t.Errorf("seatLabelToNumber(%q, %d) = %d, want %d", c.label, c.fallback, got, c.want)
		}
	}
}

// twoSectionVenue builds a tiny synthetic ListVenueSeatsOrdered result:
// 2 sections x 2 rows x 2 seats = 8 seats, already in the walk order the
// real query would return.
func twoSectionVenue() []db.ListVenueSeatsOrderedRow {
	rows := []db.ListVenueSeatsOrderedRow{}
	seatID := int64(1)
	for sec := int64(1); sec <= 2; sec++ {
		tier := "floor"
		if sec == 2 {
			tier = "balcony"
		}
		for row := int64(1); row <= 2; row++ {
			for seatN := 1; seatN <= 2; seatN++ {
				rows = append(rows, db.ListVenueSeatsOrderedRow{
					SectionID: sec, SectionName: "Section", SectionTier: tier,
					SectionClosed: sec == 2, // section 2 closed, for the sellable test
					RowID:         (sec-1)*2 + row, RowLabel: "Row",
					SeatID: seatID, SeatLabel: "seat",
					XCoord: int32(seatID * 10), YCoord: int32(row),
				})
				seatID++
			}
		}
	}
	return rows
}

func TestBuildLayout_OrdinalsAndCounts(t *testing.T) {
	rows := twoSectionVenue()
	built, err := BuildLayout(1, 3, rows)
	if err != nil {
		t.Fatalf("BuildLayout: %v", err)
	}

	if built.Meta.SeatCount != 8 {
		t.Errorf("SeatCount = %d, want 8", built.Meta.SeatCount)
	}
	if len(built.Meta.Sections) != 2 {
		t.Fatalf("len(Sections) = %d, want 2", len(built.Meta.Sections))
	}
	if got := built.Meta.Sections[0].SeatCount; got != 4 {
		t.Errorf("section 0 SeatCount = %d, want 4", got)
	}
	if got := built.Meta.Sections[1].FirstSeat; got != 4 {
		t.Errorf("section 1 FirstSeat = %d, want 4 (contiguous ranges, decision #2)", got)
	}
	if len(built.Meta.Rows) != 4 {
		t.Fatalf("len(Rows) = %d, want 4", len(built.Meta.Rows))
	}

	// Ordinal IS the slice index: SeatID[ordinal] must match the walk order.
	for ord, id := range built.SeatID {
		if int64(ord+1) != id {
			t.Errorf("SeatID[%d] = %d, want %d (ordinals must equal walk position)", ord, id, ord+1)
		}
	}

	// SectionClosed denormalizes into sellable per ordinal.
	for ord, closed := range built.SectionClosed {
		wantClosed := ord >= 4 // section 2 (ordinals 4-7) is closed
		if closed != wantClosed {
			t.Errorf("SectionClosed[%d] = %v, want %v", ord, closed, wantClosed)
		}
	}

	if len(built.SeatsBin) == 0 {
		t.Error("SeatsBin is empty")
	}
}
