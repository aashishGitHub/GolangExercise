// Package wsproto implements docs/plan.md's locked WebSocket binary wire
// format for availability — identical bytes fan out to every subscriber of
// an event, which is the whole point: one buffer, N connections, no
// per-client serialization cost.
//
//	0x01 SNAPSHOT  op|protoVer|u16 layoutVersion|u32 eventIdHash|u64 seq|u32 seatCount|packed bitset
//	0x02 SPARSE    op|protoVer|u64 seq|u16 count|u32[count] = (state<<30)|ordinal
//
// All multi-byte fields are little-endian. protoVer is 1.
//
// 0x03 RUN (run-length encoded ranges) is in the locked contract but is not
// implemented here: every domain event this system produces (seat.held,
// seat.released, seat.booked) changes exactly one seat, so every delta this
// projector ever emits is naturally a 1-entry SPARSE frame. RUN only pays
// for itself for a bulk operation (e.g. an operator closing an entire
// section) that doesn't exist yet — an honest scope cut, not an oversight.
package wsproto

import "encoding/binary"

const (
	OpSnapshot byte = 0x01
	OpSparse   byte = 0x02
	OpRun      byte = 0x03 // reserved, not emitted (see package doc)

	ProtoVersion byte = 1
)

// Change is one (ordinal, new-state) pair, packed into the SPARSE format's
// u32 as (state<<30)|ordinal — 30 bits of ordinal (over a billion seats,
// nowhere near a real venue) leaves 2 bits for state, matching seatmap.State.
type Change struct {
	Ordinal uint32
	State   uint8 // 0-3, see internal/seatmap.State
}

func packChange(c Change) uint32 {
	return (uint32(c.State) << 30) | (c.Ordinal & 0x3FFFFFFF)
}

func unpackChange(v uint32) Change {
	return Change{Ordinal: v & 0x3FFFFFFF, State: uint8(v >> 30)}
}

// EncodeSnapshot builds a 0x01 frame. packed is the full 2-bit bitset
// (internal/seatmap's wire format) for seatCount seats.
func EncodeSnapshot(layoutVersion uint16, eventIDHash uint32, seq uint64, seatCount uint32, packed []byte) []byte {
	buf := make([]byte, 0, 2+2+4+8+4+len(packed))
	buf = append(buf, OpSnapshot, ProtoVersion)
	buf = binary.LittleEndian.AppendUint16(buf, layoutVersion)
	buf = binary.LittleEndian.AppendUint32(buf, eventIDHash)
	buf = binary.LittleEndian.AppendUint64(buf, seq)
	buf = binary.LittleEndian.AppendUint32(buf, seatCount)
	buf = append(buf, packed...)
	return buf
}

// EncodeSparse builds a 0x02 frame carrying one or more ordinal/state
// changes at a single seq.
func EncodeSparse(seq uint64, changes []Change) []byte {
	buf := make([]byte, 0, 2+8+2+4*len(changes))
	buf = append(buf, OpSparse, ProtoVersion)
	buf = binary.LittleEndian.AppendUint64(buf, seq)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(changes)))
	for _, c := range changes {
		buf = binary.LittleEndian.AppendUint32(buf, packChange(c))
	}
	return buf
}

// Frame is a decoded envelope — used by the client-facing WS test client
// and by wshub's gap-fill logic, never by the hot encode path above.
type Frame struct {
	Op      byte
	Seq     uint64 // 0 for SNAPSHOT frames decoded before the seq field is read separately if needed
	Changes []Change

	// SNAPSHOT-only fields.
	LayoutVersion uint16
	EventIDHash   uint32
	SeatCount     uint32
	Packed        []byte
}

// Decode parses either frame type. Returns an error for a short buffer or
// an unrecognized op — a real client must never guess at a malformed frame.
func Decode(b []byte) (Frame, error) {
	if len(b) < 2 {
		return Frame{}, errShort
	}
	op, ver := b[0], b[1]
	if ver != ProtoVersion {
		return Frame{}, errVersion
	}
	switch op {
	case OpSnapshot:
		if len(b) < 2+2+4+8+4 {
			return Frame{}, errShort
		}
		layoutVersion := binary.LittleEndian.Uint16(b[2:4])
		eventIDHash := binary.LittleEndian.Uint32(b[4:8])
		seq := binary.LittleEndian.Uint64(b[8:16])
		seatCount := binary.LittleEndian.Uint32(b[16:20])
		packed := b[20:]
		return Frame{Op: op, Seq: seq, LayoutVersion: layoutVersion, EventIDHash: eventIDHash, SeatCount: seatCount, Packed: packed}, nil
	case OpSparse:
		if len(b) < 2+8+2 {
			return Frame{}, errShort
		}
		seq := binary.LittleEndian.Uint64(b[2:10])
		count := binary.LittleEndian.Uint16(b[10:12])
		if len(b) < 12+4*int(count) {
			return Frame{}, errShort
		}
		changes := make([]Change, count)
		for i := range changes {
			off := 12 + 4*i
			changes[i] = unpackChange(binary.LittleEndian.Uint32(b[off : off+4]))
		}
		return Frame{Op: op, Seq: seq, Changes: changes}, nil
	default:
		return Frame{}, errUnknownOp
	}
}

type protoError string

func (e protoError) Error() string { return string(e) }

const (
	errShort     = protoError("wsproto: frame too short")
	errVersion   = protoError("wsproto: unsupported protocol version")
	errUnknownOp = protoError("wsproto: unknown opcode")
)
