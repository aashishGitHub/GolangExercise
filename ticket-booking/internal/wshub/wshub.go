// Package wshub is the local stand-in for API Gateway WebSocket + a
// notifier Lambda (docs/plan.md Phase 7): it terminates /ws connections,
// sends the initial 0x01 SNAPSHOT (or gap-fills from internal/projector's
// delta ring), and fans out live deltas published by the projector over
// Redis Pub/Sub — one Redis SUBSCRIBE per event with connections, not one
// per connection, so fan-out cost doesn't scale with subscriber count.
//
// It also implements the three local-emulator fidelity patches
// docs/plan.md calls "the highest-value fidelity patch in the stack":
// real API Gateway WebSocket enforces a 10-minute idle timeout and a
// 128 KB frame cap and can silently drop connections; moto has no
// WebSocket emulation at all, so without deliberately reproducing these
// here, reconnect/resync code would only ever be exercised for the first
// time in production. Locally, it is exercised on every run.
package wshub

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/auth"
	"ticketing/internal/db"
	"ticketing/internal/projector"
	"ticketing/internal/wsproto"
)

const (
	// IdleTimeout mirrors API Gateway WebSocket's real 10-minute idle
	// disconnect — enforced here via ping/pong + read deadline, not just
	// documented.
	IdleTimeout = 10 * time.Minute
	pingEvery   = IdleTimeout / 3

	// FrameSizeCap mirrors API Gateway WebSocket's real 128 KB per-frame
	// limit. Every frame this system currently emits (a snapshot for a
	// 30k-seat venue is ~7.5 KB; a delta is a handful of bytes) is far
	// under this, so the enforcement path is real but not exercised at
	// today's scale — noted honestly rather than claimed as tested.
	FrameSizeCap = 128 * 1024

	// ChaosDisconnectRate randomly severs an established connection on
	// roughly 1% of outbound writes — the fidelity patch that forces
	// every local run to exercise reconnect + resync instead of leaving
	// that path untested until a real Gateway hiccup in production.
	ChaosDisconnectRate = 0.01
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // CORS is the chi middleware's job for HTTP; WS has no preflight
}

type Hub struct {
	verifier *auth.Verifier
	q        db.Querier
	proj     *projector.Projector
	rdb      *redis.Client

	mu   sync.Mutex
	subs map[int64]*eventSub // eventID -> fan-out subscription, one per event with >=1 live connection
}

func New(verifier *auth.Verifier, q db.Querier, proj *projector.Projector, rdb *redis.Client) *Hub {
	return &Hub{verifier: verifier, q: q, proj: proj, rdb: rdb, subs: make(map[int64]*eventSub)}
}

// eventSub is the single Redis SUBSCRIBE for one event, fanning out to
// every locally-connected client for that event.
type eventSub struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	conns  map[*conn]struct{}
}

type conn struct {
	ws       *websocket.Conn
	connID   uuid.UUID
	eventID  int64
	send     chan []byte
	lastSeq  uint64
	closed   chan struct{}
}

// ServeWS handles GET /ws?event={id}&token={jwt}&sinceSeq={n}. Token is a
// query param, not an Authorization header — a browser WebSocket cannot
// set custom headers, mirroring how a real API Gateway WebSocket JWT-in-
// query-string authorizer is wired (internal/auth.VerifyToken's own doc
// comment already anticipated this).
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	eventID, err := parseEventID(r.URL.Query().Get("event"))
	if err != nil {
		http.Error(w, "invalid or missing event", http.StatusBadRequest)
		return
	}
	claims, err := h.verifier.VerifyToken(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var sinceSeq uint64
	if s := r.URL.Query().Get("sinceSeq"); s != "" {
		if _, err := fmt.Sscanf(s, "%d", &sinceSeq); err != nil {
			http.Error(w, "invalid sinceSeq", http.StatusBadRequest)
			return
		}
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("wshub: upgrade failed: %v", err)
		return
	}

	c := &conn{
		ws: ws, connID: uuid.New(), eventID: eventID,
		send: make(chan []byte, 64), closed: make(chan struct{}),
	}

	ctx := context.Background()
	if err := h.q.InsertWSConnection(ctx, db.InsertWSConnectionParams{
		ConnectionID: c.connID, EventID: eventID, UserSub: claims.Sub,
	}); err != nil {
		log.Printf("wshub: failed to record connection (non-fatal): %v", err)
	}

	// Subscribe BEFORE building the snapshot/gap-fill, not after: pumpRedis
	// starts queueing live deltas into c.send the instant this call
	// returns. If we built the snapshot first and subscribed second, any
	// delta published in that window would never reach this connection —
	// not even via reconnect, since nothing marks it "missed". c.send is
	// buffered and writeLoop hasn't started yet, so anything queued here
	// just waits; some of it may duplicate/precede what the snapshot
	// already reflects, which is fine — wsproto's seq contract makes a
	// client-side "ignore anything <= my last applied seq" the correct,
	// expected handling of at-least-once delivery, not a bug to route
	// around.
	h.subscribe(eventID, c)
	defer h.unsubscribe(eventID, c)

	if err := h.sendInitialFrames(ctx, c, sinceSeq); err != nil {
		log.Printf("wshub: initial frame send failed for conn %s: %v", c.connID, err)
		return
	}

	go h.writeLoop(c)
	h.readLoop(ctx, c)
}

func parseEventID(s string) (int64, error) {
	var id int64
	if s == "" {
		return 0, fmt.Errorf("missing event")
	}
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid event id %q", s)
	}
	return id, nil
}

// sendInitialFrames sends either a gap-fill (client reconnecting with a
// known sinceSeq the ring still covers) or a full snapshot — never both,
// and never a partial gap-fill silently downgraded to nothing.
func (h *Hub) sendInitialFrames(ctx context.Context, c *conn, sinceSeq uint64) error {
	if sinceSeq > 0 {
		frame, ok, err := h.proj.GapFill(ctx, c.eventID, sinceSeq)
		if err != nil {
			return fmt.Errorf("gap-fill: %w", err)
		}
		if ok {
			if frame == nil {
				c.lastSeq = sinceSeq // already caught up, nothing to send
				return nil
			}
			if err := c.ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return fmt.Errorf("write gap-fill frame: %w", err)
			}
			if fr, decErr := wsproto.Decode(frame); decErr == nil {
				c.lastSeq = fr.Seq
			}
			return nil
		}
		// Ring didn't cover the gap — fall through to a full snapshot,
		// exactly the "one resync, not a storm" contract.
	}

	packed, seq, err := h.proj.EnsureBitset(ctx, c.eventID)
	if err != nil {
		return fmt.Errorf("ensure bitset: %w", err)
	}
	seatCount, err := h.proj.SeatCount(ctx, c.eventID)
	if err != nil {
		return fmt.Errorf("seat count: %w", err)
	}
	evt, err := h.q.GetEvent(ctx, c.eventID)
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}
	frame := wsproto.EncodeSnapshot(uint16(evt.LayoutVersion), uint32(c.eventID), seq, uint32(seatCount), packed)
	if len(frame) > FrameSizeCap {
		return fmt.Errorf("snapshot frame %d bytes exceeds %d byte cap", len(frame), FrameSizeCap)
	}
	if err := c.ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	c.lastSeq = seq
	return nil
}

func (h *Hub) subscribe(eventID int64, c *conn) {
	h.mu.Lock()
	sub, ok := h.subs[eventID]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		sub = &eventSub{cancel: cancel, conns: make(map[*conn]struct{})}
		h.subs[eventID] = sub
		go h.pumpRedis(ctx, eventID, sub)
	}
	sub.mu.Lock()
	sub.conns[c] = struct{}{}
	sub.mu.Unlock()
	h.mu.Unlock()
}

func (h *Hub) unsubscribe(eventID int64, c *conn) {
	close(c.closed)
	_ = c.ws.Close()
	if err := h.q.CloseWSConnection(context.Background(), c.connID); err != nil {
		log.Printf("wshub: failed to mark connection closed (non-fatal): %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	sub, ok := h.subs[eventID]
	if !ok {
		return
	}
	sub.mu.Lock()
	delete(sub.conns, c)
	empty := len(sub.conns) == 0
	sub.mu.Unlock()
	if empty {
		sub.cancel()
		delete(h.subs, eventID)
	}
}

// pumpRedis is the ONE subscriber per event, regardless of how many local
// connections are watching it — fan-out happens in Go, not in Redis.
func (h *Hub) pumpRedis(ctx context.Context, eventID int64, sub *eventSub) {
	pubsub := h.rdb.Subscribe(ctx, fmt.Sprintf("{event:%d}:notify", eventID))
	defer func() { _ = pubsub.Close() }()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			payload := []byte(msg.Payload)
			sub.mu.Lock()
			for c := range sub.conns {
				select {
				case c.send <- payload:
				default:
					log.Printf("wshub: conn %s send buffer full, dropping (client will gap on next frame and resync)", c.connID)
				}
			}
			sub.mu.Unlock()
		}
	}
}

func (h *Hub) writeLoop(c *conn) {
	pingTicker := time.NewTicker(pingEvery)
	defer pingTicker.Stop()
	defer close(c.send)

	for {
		select {
		case <-c.closed:
			return
		case <-pingTicker.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case frame, ok := <-c.send:
			if !ok {
				return
			}
			if chaosDisconnect() {
				log.Printf("wshub: chaos-disconnecting conn %s (~1%% fidelity patch, forces client resync)", c.connID)
				return
			}
			if len(frame) > FrameSizeCap {
				log.Printf("wshub: dropping oversized frame (%d bytes) for conn %s", len(frame), c.connID)
				continue
			}
			if err := c.ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
			if f, err := wsproto.Decode(frame); err == nil {
				c.lastSeq = f.Seq
			}
		}
	}
}

func (h *Hub) readLoop(ctx context.Context, c *conn) {
	c.ws.SetReadLimit(FrameSizeCap)
	_ = c.ws.SetReadDeadline(time.Now().Add(IdleTimeout))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(IdleTimeout))
	})
	for {
		if _, _, err := c.ws.ReadMessage(); err != nil {
			return
		}
	}
}

// chaosDisconnect uses crypto/rand, not math/rand, purely so this package
// has no global PRNG seeding concerns — it is a fidelity fault injector,
// not a security control, but crypto/rand is just as cheap here and one
// less thing to explain in review.
func chaosDisconnect() bool {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return false
	}
	return n.Int64() < int64(ChaosDisconnectRate*10000)
}
