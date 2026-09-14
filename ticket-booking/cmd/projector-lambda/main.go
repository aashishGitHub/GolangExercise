// cmd/projector-lambda is cmd/projector's prod twin: invoked by an
// EventBridge rule subscribed to seat.* events (modules/eventing) instead
// of polling — a single RunOnce per invocation, decoupled from whatever
// process happens to be terminating WebSocket connections (cmd/server).
package main

import (
	"context"
	"log"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/projector"
)

func handler(ctx context.Context) error {
	cfg := config.Load()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	proj := projector.New(q, rdb)
	processed, applied, err := proj.RunOnce(ctx, 200)
	if err != nil {
		return err
	}
	if processed > 0 {
		log.Printf("projector-lambda: processed %d event(s), %d changed the bitset", processed, applied)
	}
	return nil
}

func main() {
	awslambda.Start(handler)
}
