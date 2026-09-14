// cmd/outbox-relay is the scheduled safety net for the transactional outbox
// (internal/events.Publish writes domain_events rows; this re-scans
// published_at IS NULL and PutEvents them to EventBridge). In prod this runs
// on an EventBridge Scheduler cron, not as a long-lived process — the ticker
// loop below is a local-dev convenience so `make dev-server`-style iteration
// doesn't need a separate scheduled trigger.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/db"
	"fieldsync/internal/outbox"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	eb, err := outbox.NewEventBridgeClient(ctx, cfg.EventBusEndpoint, cfg.AWSRegion)
	if err != nil {
		log.Fatalf("init EventBridge client: %v", err)
	}

	relay := outbox.NewRelay(db.New(pool), eb, cfg.EventBusName)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	log.Printf("outbox-relay: polling every 5s (bus=%s)", cfg.EventBusName)

	for range ticker.C {
		published, err := relay.RunOnce(ctx)
		if err != nil {
			log.Printf("outbox-relay: RunOnce error: %v", err)
			continue
		}
		if published > 0 {
			log.Printf("outbox-relay: published %d event(s)", published)
		}
	}
}
