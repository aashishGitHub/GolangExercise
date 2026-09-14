// Package projector maintains the Redis-backed read model docs/plan.md
// Phase 7 describes: a packed 2-bit availability bitset, a monotonic seq
// counter, and a capped delta ring — all keyed per event, all derived from
// domain_events (never event_seats directly, so the read model has exactly
// one input: the same outbox every other consumer reads).
//
// It is the sole writer of the Redis bitset — mirroring
// internal/inventory's "sole writer of event_seats" invariant one layer
// downstream. internal/wshub only ever reads what this package writes.
package projector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/db"
	"ticketing/internal/seatmap"
	"ticketing/internal/wsproto"
)

const (
	ConsumerName = "projector"
	ringCap      = 200 // capped delta ring per event — Phase 7's "overflow -> exactly one resync" test depends on this being small enough to overflow deliberately
)

func bitsetKey(eventID int64) string  { return fmt.Sprintf("{event:%d}:bitset", eventID) }
func seqKey(eventID int64) string     { return fmt.Sprintf("{event:%d}:seq", eventID) }
func ringKey(eventID int64) string    { return fmt.Sprintf("{event:%d}:deltas", eventID) }
func notifyChan(eventID int64) string { return fmt.Sprintf("{event:%d}:notify", eventID) }

type Projector struct {
	q   db.Querier
	rdb *redis.Client

	// seat_id -> seat_ordinal, cached per event_id. Domain events key by
	// seat_id (event_seats' internal identity); every wire structure is
	// ordinal-indexed (docs/plan.md decision #5). Rebuilt from Postgres on
	// first sight of an event_id — bounded by that event's seat count, not
	// reloaded per event.
	ordinals map[int64]map[int64]int32
}

func New(q db.Querier, rdb *redis.Client) *Projector {
	return &Projector{q: q, rdb: rdb, ordinals: make(map[int64]map[int64]int32)}
}

func (p *Projector) ordinalOf(ctx context.Context, eventID, seatID int64) (int32, error) {
	m, ok := p.ordinals[eventID]
	if !ok {
		rows, err := p.q.ListSeatOrdinalsByEvent(ctx, eventID)
		if err != nil {
			return 0, fmt.Errorf("load seat ordinals for event %d: %w", eventID, err)
		}
		m = make(map[int64]int32, len(rows))
		for _, r := range rows {
			m[r.SeatID] = r.SeatOrdinal
		}
		p.ordinals[eventID] = m
	}
	ordinal, ok := m[seatID]
	if !ok {
		return 0, fmt.Errorf("seat %d not found in event %d's ordinal map", seatID, eventID)
	}
	return ordinal, nil
}

// SeatCount returns an event's total seat count, via the same seat_id ->
// ordinal cache ordinalOf uses (so a connect-time snapshot build and an
// event-application ordinal lookup never disagree on which seats exist).
func (p *Projector) SeatCount(ctx context.Context, eventID int64) (int, error) {
	if m, ok := p.ordinals[eventID]; ok {
		return len(m), nil
	}
	rows, err := p.q.ListSeatOrdinalsByEvent(ctx, eventID)
	if err != nil {
		return 0, fmt.Errorf("load seat ordinals for event %d: %w", eventID, err)
	}
	m := make(map[int64]int32, len(rows))
	for _, r := range rows {
		m[r.SeatID] = r.SeatOrdinal
	}
	p.ordinals[eventID] = m
	return len(m), nil
}

// EnsureBitset returns the current packed bitset and seq for an event,
// building it from Postgres (Index Only Scan path, same query the Phase 2
// DB-scan fallback uses) on first access and seeding Redis with it. Safe
// for concurrent callers across processes: SETNX means only one writer's
// initial snapshot wins, everyone else just reads it back.
func (p *Projector) EnsureBitset(ctx context.Context, eventID int64) (packed []byte, seq uint64, err error) {
	packed, err = p.rdb.Get(ctx, bitsetKey(eventID)).Bytes()
	if err == nil {
		seq, err = p.rdb.Get(ctx, seqKey(eventID)).Uint64()
		if err != nil && err != redis.Nil {
			return nil, 0, fmt.Errorf("read seq for event %d: %w", eventID, err)
		}
		return packed, seq, nil
	}
	if err != redis.Nil {
		return nil, 0, fmt.Errorf("read bitset for event %d: %w", eventID, err)
	}

	rows, err := p.q.ListEventSeatsForAvailability(ctx, eventID)
	if err != nil {
		return nil, 0, fmt.Errorf("db-scan seed for event %d: %w", eventID, err)
	}
	packed = seatmap.New(len(rows))
	for _, r := range rows {
		seatmap.Set(packed, int(r.SeatOrdinal), seatmap.StateFromDB(r.Status, r.Sellable))
	}
	// SETNX: if another process seeded first, defer to its snapshot rather
	// than clobbering a bitset that may already have deltas applied on top
	// of it.
	ok, setErr := p.rdb.SetNX(ctx, bitsetKey(eventID), packed, 0).Result()
	if setErr != nil {
		return nil, 0, fmt.Errorf("seed bitset for event %d: %w", eventID, setErr)
	}
	if !ok {
		packed, err = p.rdb.Get(ctx, bitsetKey(eventID)).Bytes()
		if err != nil {
			return nil, 0, fmt.Errorf("re-read bitset for event %d after lost SETNX race: %w", eventID, err)
		}
	}
	seq, err = p.rdb.Get(ctx, seqKey(eventID)).Uint64()
	if err != nil && err != redis.Nil {
		return nil, 0, fmt.Errorf("read seq for event %d: %w", eventID, err)
	}
	return packed, seq, nil
}

type domainEventPayload struct {
	EventID int64 `json:"eventId"`
	SeatID  int64 `json:"seatId"`
}

func stateForEventType(eventType string) (seatmap.State, bool) {
	switch eventType {
	case "seat.held":
		return seatmap.StateHeld, true
	case "seat.released":
		return seatmap.StateFree, true
	case "seat.booked":
		return seatmap.StateSold, true
	default:
		return 0, false
	}
}

// RunOnce applies up to one batch of not-yet-projected domain events to the
// Redis read model and returns how many produced an actual bitset change
// (an event whose effect the bitset already reflects — e.g. a db-scan seed
// that ran after the event was written but before this consumer processed
// it — is idempotently skipped: same design as internal/inventory's CAS
// being self-idempotent, one layer up).
// Returns (processed, applied, err): processed is how many rows this call
// consumed (the correct signal for "is the backlog drained yet?" — a
// drain loop must not stop on applied==0, since a no-op event is still
// consumed but never bumps applied); applied is how many actually changed
// the bitset (the signal worth logging/alerting on).
func (p *Projector) RunOnce(ctx context.Context, batchSize int32) (processed, applied int, err error) {
	events, err := p.q.ListUnprocessedDomainEvents(ctx, db.ListUnprocessedDomainEventsParams{
		ConsumerName: ConsumerName, RowLimit: batchSize,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list unprocessed domain events: %w", err)
	}
	processed = len(events)

	for _, evt := range events {
		newState, known := stateForEventType(evt.EventType)
		if !known {
			log.Printf("projector: ignoring unrecognized event type %q (event %s)", evt.EventType, evt.EventID)
			p.markProcessed(ctx, evt.EventID)
			continue
		}

		var payload domainEventPayload
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			log.Printf("projector: skipping event %s: malformed payload: %v", evt.EventID, err)
			p.markProcessed(ctx, evt.EventID)
			continue
		}

		ordinal, err := p.ordinalOf(ctx, payload.EventID, payload.SeatID)
		if err != nil {
			log.Printf("projector: skipping event %s: %v", evt.EventID, err)
			p.markProcessed(ctx, evt.EventID)
			continue
		}

		changed, err := p.apply(ctx, payload.EventID, ordinal, newState)
		if err != nil {
			// Leave unprocessed — retried next tick. Do NOT mark processed
			// on a Redis-side failure, or the bitset would permanently
			// miss this seat's transition.
			return processed, applied, fmt.Errorf("apply event %s: %w", evt.EventID, err)
		}
		if changed {
			applied++
		}
		p.markProcessed(ctx, evt.EventID)
	}
	return processed, applied, nil
}

// apply performs one ordinal's state transition against the Redis read
// model: read-modify-write the packed byte, INCR the seq, push the encoded
// delta onto the capped ring, and PUBLISH it for any subscribed wshub to
// forward live. Returns changed=false (a no-op) if the bitset already
// reflects newState — the idempotency that makes at-least-once event
// delivery safe here, mirroring internal/inventory's self-idempotent CAS.
//
// Not atomic across steps (no MULTI/EXEC) — a crash between the byte write
// and the seq INCR is a real local-stack gap, documented in MILESTONES.md
// rather than fixed with a Lua script, since a single in-process projector
// with no concurrent writer to the same event makes the race window
// theoretical here. A multi-replica projector would need this script.
func (p *Projector) apply(ctx context.Context, eventID int64, ordinal int32, newState seatmap.State) (changed bool, err error) {
	// MUST run before the first GETRANGE/SETRANGE below, not just
	// opportunistically: Redis auto-vivifies a missing key on SETRANGE,
	// but only out to the highest byte offset actually written — a
	// projector processing events for an event nobody has ever fetched a
	// snapshot for yet would otherwise leave a permanently truncated
	// bitset (EnsureBitset's own SETNX seed never fires again once ANY
	// key exists, even a wrongly-sized one). This was a real bug, caught
	// by the Phase 7 live-stack WS verification below, not by a unit
	// test: seq=8, but a 30,000-seat event's bitset was only 226 bytes
	// (Index Only Scan on Postgres was never consulted, because 8 stray
	// domain events from earlier phases' curl verification against
	// event_id=1 got processed by cmd/projector before any client had
	// ever hit /ws or /availability for that event).
	if _, _, err := p.EnsureBitset(ctx, eventID); err != nil {
		return false, fmt.Errorf("ensure bitset before apply: %w", err)
	}

	byteOff := int64(ordinal) >> 2
	shift := uint((ordinal & 3) * 2)

	old, err := p.rdb.GetRange(ctx, bitsetKey(eventID), byteOff, byteOff).Result()
	if err != nil {
		return false, fmt.Errorf("read bitset byte: %w", err)
	}
	var oldByte byte
	if len(old) == 1 {
		oldByte = old[0]
	}
	oldState := seatmap.State((oldByte >> shift) & 0b11)
	if oldState == newState {
		return false, nil
	}
	newByte := (oldByte &^ (0b11 << shift)) | (byte(newState) << shift)

	if _, err := p.rdb.SetRange(ctx, bitsetKey(eventID), byteOff, string([]byte{newByte})).Result(); err != nil {
		return false, fmt.Errorf("write bitset byte: %w", err)
	}
	seq, err := p.rdb.Incr(ctx, seqKey(eventID)).Result()
	if err != nil {
		return false, fmt.Errorf("incr seq: %w", err)
	}

	delta := wsproto.EncodeSparse(uint64(seq), []wsproto.Change{{Ordinal: uint32(ordinal), State: uint8(newState)}})

	pipe := p.rdb.Pipeline()
	pipe.LPush(ctx, ringKey(eventID), delta)
	pipe.LTrim(ctx, ringKey(eventID), 0, ringCap-1)
	pipe.Publish(ctx, notifyChan(eventID), delta)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("push delta ring / publish: %w", err)
	}
	return true, nil
}

// GapFill returns ONE combined delta frame bringing a client from sinceSeq
// up to the current seq (docs/plan.md Phase 7: "reconnect with sinceSeq ->
// one delta covering all 3", not N separate frames). ok=false means the
// ring doesn't reach back far enough (the client has been gone longer than
// ringCap deltas' worth of time) — the caller must fall back to a full
// 0x01 SNAPSHOT instead. This is the mechanism behind "overflow the ring ->
// exactly one resync, not a storm": a client that missed too much gets ONE
// snapshot, not a doomed attempt to replay a gap that no longer exists.
//
// Coalescing is last-write-wins per ordinal: if a seat toggled state more
// than once within the gap, only its final state at currentSeq survives in
// the combined frame — correct, since the client only needs to reach the
// same end state the live bitset is already in, not replay history.
func (p *Projector) GapFill(ctx context.Context, eventID int64, sinceSeq uint64) (frame []byte, ok bool, err error) {
	currentSeq, err := p.rdb.Get(ctx, seqKey(eventID)).Uint64()
	if err != nil && err != redis.Nil {
		return nil, false, fmt.Errorf("read current seq: %w", err)
	}
	if sinceSeq >= currentSeq {
		return nil, true, nil // already caught up
	}

	raw, err := p.rdb.LRange(ctx, ringKey(eventID), 0, -1).Result()
	if err != nil {
		return nil, false, fmt.Errorf("read delta ring: %w", err)
	}
	// raw is newest-first (LPUSH order). Collect frames with seq > sinceSeq,
	// then verify no gap: the oldest collected frame's seq must be exactly
	// sinceSeq+1, otherwise something between sinceSeq and the ring's
	// coverage was evicted and a snapshot is required instead.
	var wanted []wsproto.Frame
	for _, r := range raw {
		f, decErr := wsproto.Decode([]byte(r))
		if decErr != nil {
			return nil, false, fmt.Errorf("decode ring entry: %w", decErr)
		}
		if f.Seq > sinceSeq {
			wanted = append(wanted, f)
		}
	}
	if len(wanted) == 0 {
		return nil, false, nil // ring didn't retain anything past sinceSeq
	}
	oldestWantedSeq := wanted[len(wanted)-1].Seq // last appended = oldest kept, since raw is newest-first
	if oldestWantedSeq != sinceSeq+1 {
		return nil, false, nil // gap: something between sinceSeq and here was evicted
	}

	// wanted is newest-first; fold oldest-to-newest so a later entry's
	// state for a given ordinal overwrites an earlier one.
	byOrdinal := make(map[uint32]uint8)
	var order []uint32
	for i := len(wanted) - 1; i >= 0; i-- {
		for _, ch := range wanted[i].Changes {
			if _, seen := byOrdinal[ch.Ordinal]; !seen {
				order = append(order, ch.Ordinal)
			}
			byOrdinal[ch.Ordinal] = ch.State
		}
	}
	changes := make([]wsproto.Change, len(order))
	for i, ord := range order {
		changes[i] = wsproto.Change{Ordinal: ord, State: byOrdinal[ord]}
	}
	return wsproto.EncodeSparse(currentSeq, changes), true, nil
}

func (p *Projector) markProcessed(ctx context.Context, eventID uuid.UUID) {
	if err := p.q.MarkEventProcessed(ctx, db.MarkEventProcessedParams{
		EventID: eventID, ConsumerName: ConsumerName,
	}); err != nil {
		log.Printf("projector: mark processed failed for %s (will retry, may reprocess a no-op): %v", eventID, err)
	}
}
