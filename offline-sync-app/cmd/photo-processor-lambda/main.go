// cmd/photo-processor-lambda is the real-AWS entrypoint: triggered by an SQS
// queue (an EventBridge rule for "photo.upserted" routes to it, with a DLQ)
// instead of cmd/photo-processor's local domain_events-polling loop.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/db"
	"fieldsync/internal/photoprocessor"
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
	return sqsconsume.Handle(ctx, q, photoprocessor.ConsumerName, sqsEvent, photoprocessor.Handle)
}

func main() {
	lambda.Start(handler)
}
