package inventory

import (
	"reflect"
	"testing"

	"ticketing/internal/db"
)

func row(seatID int64, ordinal int32, rowID int64) db.ListAvailableForBestAvailableRow {
	return db.ListAvailableForBestAvailableRow{SeatID: seatID, SeatOrdinal: ordinal, RowID: rowID}
}

func TestFindContiguousRuns_WithinOneRow(t *testing.T) {
	candidates := []db.ListAvailableForBestAvailableRow{
		row(101, 0, 1), row(102, 1, 1), row(103, 2, 1), row(104, 3, 1),
	}
	runs := findContiguousRuns(candidates, 3)
	want := [][]int64{{101, 102, 103}, {102, 103, 104}}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs = %v, want %v", runs, want)
	}
}

func TestFindContiguousRuns_BreaksAtRowBoundary(t *testing.T) {
	// Ordinals 0,1,2,3 are consecutive INTEGERS, but ordinal 2 starts a new
	// row — decision #2: ordinals are contiguous across a whole section,
	// not just within a row, so a naive ordinal-adjacency check alone would
	// wrongly treat seat 2 (row 2) as physically next to seat 1 (row 1).
	candidates := []db.ListAvailableForBestAvailableRow{
		row(101, 0, 1), row(102, 1, 1), // row 1 ends here
		row(103, 2, 2), row(104, 3, 2), // row 2 starts here
	}
	runs := findContiguousRuns(candidates, 3)
	if len(runs) != 0 {
		t.Fatalf("runs = %v, want none (no 3-run stays within one row)", runs)
	}

	runsOf2 := findContiguousRuns(candidates, 2)
	want := [][]int64{{101, 102}, {103, 104}}
	if !reflect.DeepEqual(runsOf2, want) {
		t.Fatalf("runs of 2 = %v, want %v (each row's own pair, no cross-row run)", runsOf2, want)
	}
}

func TestFindContiguousRuns_NoneWhenTooFewCandidates(t *testing.T) {
	candidates := []db.ListAvailableForBestAvailableRow{row(101, 0, 1), row(102, 1, 1)}
	if runs := findContiguousRuns(candidates, 5); runs != nil {
		t.Fatalf("runs = %v, want nil", runs)
	}
}

func TestFindContiguousRuns_SkipsAGapInOrdinals(t *testing.T) {
	// A seat in the middle is already taken (not in the candidate list) —
	// ordinal jumps from 1 to 3 within the same row.
	candidates := []db.ListAvailableForBestAvailableRow{
		row(101, 0, 1), row(102, 1, 1), row(104, 3, 1), row(105, 4, 1),
	}
	runs := findContiguousRuns(candidates, 2)
	want := [][]int64{{101, 102}, {104, 105}}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs = %v, want %v", runs, want)
	}
}
