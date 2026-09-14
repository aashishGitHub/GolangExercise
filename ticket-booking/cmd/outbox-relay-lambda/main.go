// cmd/outbox-relay-lambda is cmd/outbox-relay's prod twin: invoked by an
// EventBridge Scheduler rule (modules/eventing) instead of looping — a
// single RunOnce per invocation.
package main

import (
	"context"
	"log"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/outbox"
)

func handler(ctx context.Context) error {
	cfg := config.Load()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return err
	}
	eb := eventbridge.NewFromConfig(awsCfg, func(o *eventbridge.Options) {
		if cfg.EventBusEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.EventBusEndpoint)
		}
	})

	relay := outbox.NewRelay(q, eb, cfg.EventBusName)
	n, err := relay.RunOnce(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("outbox-relay-lambda: published %d event(s)", n)
	}
	return nil
}

func main() {
	awslambda.Start(handler)
}
