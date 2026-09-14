// cmd/projector is the local-dev ticker loop calling internal/projector's
// RunOnce every tick — the standalone consumer that turns domain_events
// into the Redis-backed availability read model internal/wshub serves.
// Deliberately its own process (not folded into cmd/server): the prod twin
// (cmd/projector-lambda, Phase 11) is invoked by an EventBridge rule
// subscribed to seat.* events, decoupled from whatever process happens to
// be terminating WebSocket connections.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/projector"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	proj := projector.New(q, rdb)

	log.Printf("projector: polling every 500ms")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		processed, applied, err := proj.RunOnce(ctx, 200)
		if err != nil {
			log.Printf("projector: tick error: %v", err)
			continue
		}
		if processed > 0 {
			log.Printf("projector: processed %d event(s), %d changed the bitset", processed, applied)
		}
	}
}
