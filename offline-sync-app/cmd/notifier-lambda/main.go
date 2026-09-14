// cmd/notifier-lambda is the real-AWS entrypoint: triggered by an SQS queue,
// pushes via the WebSocket API's Management API — cmd/server's in-process
// notifier loop pushes through an in-memory hub instead, which only works
// because it runs in the same process as the WebSocket connections
// themselves (see internal/wsstub's package comment).
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/db"
	"fieldsync/internal/notifier"
	"fieldsync/internal/sqsconsume"
)

var (
	q      db.Querier
	pusher *notifier.ManagementAPIPusher
)

func init() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	q = db.New(pool)

	pusher, err = notifier.NewManagementAPIPusher(ctx, cfg.WSManagementAPIEndpoint, cfg.AWSRegion)
	if err != nil {
		log.Fatalf("init Management API client: %v", err)
	}
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	return sqsconsume.Handle(ctx, q, notifier.ConsumerName, sqsEvent,
		func(ctx context.Context, evt db.DomainEvent) error {
			return notifier.Handle(ctx, q, pusher, evt)
		})
}

func main() {
	lambda.Start(handler)
}
