// cmd/photo-processor polls domain_events for "photo.upserted" and runs
// geotag validation. In prod this is an SQS-triggered Lambda subscribed to
// an EventBridge rule; the polling loop here is the local-dev substitute
// documented in internal/consume's package comment.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/consume"
	"fieldsync/internal/db"
	"fieldsync/internal/photoprocessor"
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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	log.Printf("photo-processor: polling every 5s")

	for range ticker.C {
		n, err := consume.RunOnce(ctx, q, photoprocessor.ConsumerName, photoprocessor.EventTypes, photoprocessor.Handle)
		if err != nil {
			log.Printf("photo-processor: RunOnce error: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("photo-processor: processed %d event(s)", n)
		}
	}
}
