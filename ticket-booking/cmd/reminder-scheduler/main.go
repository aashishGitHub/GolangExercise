// cmd/reminder-scheduler is the local-dev ticker loop calling
// internal/reminder's RunOnce for both T-24h and T-2h every tick. The
// prod twin (cmd/reminder-scheduler-lambda, Phase 11) is invoked by an
// EventBridge Scheduler rule instead of looping — moto-server is
// control-plane only (Phase 7's documented gap: creates schedules but
// never fires them), so this ticker is the honest local stand-in.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/reminder"
)

const pollInterval = 30 * time.Second

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	log.Printf("reminder-scheduler: polling every %s", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := reminder.RunOnce(ctx, q, "T-24h", reminder.T24h); err != nil {
			log.Printf("reminder-scheduler: T-24h tick error: %v", err)
		}
		if _, err := reminder.RunOnce(ctx, q, "T-2h", reminder.T2h); err != nil {
			log.Printf("reminder-scheduler: T-2h tick error: %v", err)
		}
	}
}
