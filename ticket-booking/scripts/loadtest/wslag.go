package main

import (
	"log"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"ticketing/internal/wsproto"
)

// wsLagObserver connects ONE real /ws client for the duration of the run
// and measures, for every SPARSE delta it receives, how long after the
// journey that caused it issued its POST /holds the delta actually
// arrived — docs/plan.md Phase 10's "WS delta lag p95".
type wsLagObserver struct {
	mu        sync.Mutex
	requested map[int]time.Time // ordinal -> hold-request time
	lagsMs    []float64
}

func newWSLagObserver() *wsLagObserver {
	return &wsLagObserver{requested: make(map[int]time.Time)}
}

func (o *wsLagObserver) noteHoldRequested(ordinal int, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requested[ordinal] = at
}

func (o *wsLagObserver) lagPercentiles() (p50, p95, p99 float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.lagsMs) == 0 {
		return 0, 0, 0
	}
	sorted := append([]float64(nil), o.lagsMs...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	pick := func(p float64) float64 {
		idx := int(p * float64(len(sorted)))
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}
	return pick(0.50), pick(0.95), pick(0.99)
}

// run connects and blocks decoding frames until the connection closes or
// ctx is cancelled — run it in its own goroutine.
func (o *wsLagObserver) run(addr string, eventID int64, token string, stop <-chan struct{}) {
	q := url.Values{}
	q.Set("event", strconv.FormatInt(eventID, 10))
	q.Set("token", token)
	u := url.URL{Scheme: "ws", Host: addr, Path: "/ws", RawQuery: q.Encode()}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("loadtest: ws lag observer failed to connect (lag numbers will be empty): %v", err)
		return
	}
	defer conn.Close()

	go func() {
		<-stop
		_ = conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		frame, err := wsproto.Decode(raw)
		if err != nil || frame.Op != wsproto.OpSparse {
			continue
		}
		now := time.Now()
		o.mu.Lock()
		for _, ch := range frame.Changes {
			if reqAt, ok := o.requested[int(ch.Ordinal)]; ok {
				o.lagsMs = append(o.lagsMs, float64(now.Sub(reqAt).Microseconds())/1000.0)
				delete(o.requested, int(ch.Ordinal))
			}
		}
		o.mu.Unlock()
	}
}
