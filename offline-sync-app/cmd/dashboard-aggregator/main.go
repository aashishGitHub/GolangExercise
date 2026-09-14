// cmd/dashboard-aggregator polls domain_events for "photo.upserted" and
// recomputes location_stats. In prod this is an SQS-triggered Lambda
// subscribed to an EventBridge rule; the polling loop here is the local-dev
// substitute documented in internal/consume's package comment.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/consume"
	"fieldsync/internal/dashboardaggregator"
	"fieldsync/internal/db"
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
	log.Printf("dashboard-aggregator: polling every 5s")

	for range ticker.C {
		n, err := consume.RunOnce(ctx, q, dashboardaggregator.ConsumerName, dashboardaggregator.EventTypes,
			func(ctx context.Context, evt db.DomainEvent) error {
				return dashboardaggregator.Handle(ctx, q, evt)
			})
		if err != nil {
			log.Printf("dashboard-aggregator: RunOnce error: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("dashboard-aggregator: processed %d event(s)", n)
		}
	}
}
