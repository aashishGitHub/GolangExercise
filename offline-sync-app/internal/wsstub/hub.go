// Package wsstub is the documented local-dev fidelity gap called out from
// the very first architecture pass: real AWS pushes to WebSocket API Gateway
// connections via the Management API, addressing connections registered in
// DynamoDB from any Lambda invocation, in any process. That cross-process
// addressing has no local equivalent, so this hub only works because the
// notifier consumer loop runs as a goroutine inside cmd/server, sharing this
// exact in-memory map — it cannot be a separate local process the way
// outbox-relay/photo-processor/dashboard-aggregator are.
package wsstub

import (
	"context"
	"sync"

	"github.com/coder/websocket"
)

type Hub struct {
	mu    sync.RWMutex
	conns map[string]*websocket.Conn
}

func NewHub() *Hub {
	return &Hub{conns: make(map[string]*websocket.Conn)}
}

func (h *Hub) Register(connectionID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[connectionID] = conn
}

func (h *Hub) Unregister(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, connectionID)
}

// Push mirrors the shape of a real API Gateway Management API
// PostToConnection call — same "here's a connection id, here's a payload"
// contract — so the notifier's own logic doesn't need to change if this is
// ever swapped for a real Management API client.
func (h *Hub) Push(ctx context.Context, connectionID string, message []byte) error {
	h.mu.RLock()
	conn, ok := h.conns[connectionID]
	h.mu.RUnlock()
	if !ok {
		return nil // connection already gone — not an error, matches GoneException semantics
	}
	return conn.Write(ctx, websocket.MessageText, message)
}
