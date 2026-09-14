// cmd/outbox-relay is the local-dev ticker loop calling internal/outbox's
// RunOnce every tick against moto-server's EventBridge. The prod twin
// (cmd/outbox-relay-lambda, Phase 11) is invoked on a schedule instead of
// looping.
package main

import (
	"context"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/outbox"
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

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}
	eb := eventbridge.NewFromConfig(awsCfg, func(o *eventbridge.Options) {
		if cfg.EventBusEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.EventBusEndpoint)
		}
	})

	relay := outbox.NewRelay(q, eb, cfg.EventBusName)

	log.Printf("outbox-relay: polling every 2s against bus %q", cfg.EventBusName)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		n, err := relay.RunOnce(ctx)
		if err != nil {
			log.Printf("outbox-relay: tick error: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("outbox-relay: published %d event(s)", n)
		}
	}
}
