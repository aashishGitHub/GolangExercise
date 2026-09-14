// cmd/outbox-relay-lambda is the real-AWS entrypoint: invoked on a schedule
// by EventBridge Scheduler (per the plan doc's locked decision — "a
// scheduled outbox-relay Lambda"), one RunOnce per invocation. Local dev
// uses cmd/outbox-relay's ticker loop instead, since there's no scheduler to
// invoke a Lambda locally.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/db"
	"fieldsync/internal/outbox"
)

var relay *outbox.Relay

func init() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	eb, err := outbox.NewEventBridgeClient(ctx, cfg.EventBusEndpoint, cfg.AWSRegion)
	if err != nil {
		log.Fatalf("init EventBridge client: %v", err)
	}

	relay = outbox.NewRelay(db.New(pool), eb, cfg.EventBusName)
}

func handler(ctx context.Context) error {
	published, err := relay.RunOnce(ctx)
	if err != nil {
		return err
	}
	if published > 0 {
		log.Printf("outbox-relay-lambda: published %d event(s)", published)
	}
	return nil
}

func main() {
	lambda.Start(handler)
}
