// cmd/reminder-scheduler-lambda is cmd/reminder-scheduler's prod twin:
// invoked by an EventBridge Scheduler rule instead of looping — one
// T-24h + T-2h pass per invocation.
package main

import (
	"context"
	"log"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/reminder"
)

func handler(ctx context.Context) error {
	cfg := config.Load()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)

	if n, err := reminder.RunOnce(ctx, q, "T-24h", reminder.T24h); err != nil {
		return err
	} else if n > 0 {
		log.Printf("reminder-scheduler-lambda: sent %d T-24h reminder(s)", n)
	}
	if n, err := reminder.RunOnce(ctx, q, "T-2h", reminder.T2h); err != nil {
		return err
	} else if n > 0 {
		log.Printf("reminder-scheduler-lambda: sent %d T-2h reminder(s)", n)
	}
	return nil
}

func main() {
	awslambda.Start(handler)
}
