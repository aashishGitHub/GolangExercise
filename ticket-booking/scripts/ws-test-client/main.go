// scripts/ws-test-client is a real WebSocket client that connects to the
// running cmd/server, receives a decoded frame, and prints it — the "paste
// the actual received bytes, not a description" verification docs/plan.md
// Phase 7 requires. Not a test binary; a manual verification tool, same
// role as curl for the REST endpoints.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"

	"ticketing/internal/wsproto"
)

func main() {
	event := flag.Int64("event", 1, "event id")
	token := flag.String("token", "", "cognito-local ID token")
	sinceSeq := flag.Uint64("sinceSeq", 0, "resume from this seq (0 = full snapshot)")
	addr := flag.String("addr", "localhost:8080", "server address")
	count := flag.Int("count", 1, "how many frames to print before exiting")
	flag.Parse()

	if *token == "" {
		log.Fatal("-token required")
	}

	q := url.Values{}
	q.Set("event", fmt.Sprintf("%d", *event))
	q.Set("token", *token)
	if *sinceSeq > 0 {
		q.Set("sinceSeq", fmt.Sprintf("%d", *sinceSeq))
	}
	u := url.URL{Scheme: "ws", Host: *addr, Path: "/ws", RawQuery: q.Encode()}

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("dial: %v (resp=%v)", err, resp)
	}
	defer conn.Close()

	for i := 0; i < *count; i++ {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Fatalf("read frame %d: %v", i, err)
		}
		frame, err := wsproto.Decode(raw)
		if err != nil {
			log.Fatalf("decode frame %d: %v", i, err)
		}
		fmt.Printf("--- frame %d: %d bytes raw ---\n", i, len(raw))
		fmt.Printf("raw hex (first 64 bytes): %s\n", hex.EncodeToString(raw[:min(64, len(raw))]))
		switch frame.Op {
		case wsproto.OpSnapshot:
			fmt.Printf("SNAPSHOT layoutVersion=%d eventIDHash=%d seq=%d seatCount=%d packedLen=%d\n",
				frame.LayoutVersion, frame.EventIDHash, frame.Seq, frame.SeatCount, len(frame.Packed))
		case wsproto.OpSparse:
			fmt.Printf("SPARSE seq=%d changes=%v\n", frame.Seq, frame.Changes)
		}
	}
	os.Exit(0)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
