// cmd/dashboard-aggregator-lambda is the real-AWS entrypoint: triggered by
// an SQS queue instead of cmd/dashboard-aggregator's local polling loop.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/dashboardaggregator"
	"fieldsync/internal/db"
	"fieldsync/internal/sqsconsume"
)

var q db.Querier

func init() {
	cfg := config.Load()
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	q = db.New(pool)
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	return sqsconsume.Handle(ctx, q, dashboardaggregator.ConsumerName, sqsEvent,
		func(ctx context.Context, evt db.DomainEvent) error {
			return dashboardaggregator.Handle(ctx, q, evt)
		})
}

func main() {
	lambda.Start(handler)
}
