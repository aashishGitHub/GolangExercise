package wsproto

import (
	"bytes"
	"testing"

	"ticketing/internal/seatmap"
)

func TestSnapshotRoundTrip(t *testing.T) {
	packed := seatmap.New(10)
	seatmap.Set(packed, 3, seatmap.StateHeld)
	seatmap.Set(packed, 7, seatmap.StateSold)

	raw := EncodeSnapshot(5, 42, 100, 10, packed)
	f, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if f.Op != OpSnapshot || f.LayoutVersion != 5 || f.EventIDHash != 42 || f.Seq != 100 || f.SeatCount != 10 {
		t.Fatalf("decoded header mismatch: %+v", f)
	}
	if !bytes.Equal(f.Packed, packed) {
		t.Fatalf("packed bitset mismatch: got %v want %v", f.Packed, packed)
	}
}

func TestSparseRoundTrip(t *testing.T) {
	changes := []Change{
		{Ordinal: 12345, State: uint8(seatmap.StateHeld)},
		{Ordinal: 0, State: uint8(seatmap.StateFree)},
		{Ordinal: 1073741823, State: uint8(seatmap.StateUnavailable)}, // max 30-bit ordinal
	}
	raw := EncodeSparse(7, changes)
	f, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if f.Op != OpSparse || f.Seq != 7 {
		t.Fatalf("header mismatch: %+v", f)
	}
	if len(f.Changes) != len(changes) {
		t.Fatalf("changes len = %d, want %d", len(f.Changes), len(changes))
	}
	for i, c := range changes {
		if f.Changes[i] != c {
			t.Errorf("change[%d] = %+v, want %+v", i, f.Changes[i], c)
		}
	}
}

func TestDecode_ShortBufferRejected(t *testing.T) {
	if _, err := Decode([]byte{OpSnapshot, ProtoVersion, 0, 0}); err == nil {
		t.Fatal("expected error for truncated snapshot frame")
	}
	if _, err := Decode(nil); err == nil {
		t.Fatal("expected error for empty buffer")
	}
}

func TestDecode_UnknownOpRejected(t *testing.T) {
	if _, err := Decode([]byte{0xFF, ProtoVersion, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected error for unknown opcode")
	}
}

func TestDecode_WrongVersionRejected(t *testing.T) {
	if _, err := Decode([]byte{OpSparse, 99, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected error for unsupported protocol version")
	}
}
