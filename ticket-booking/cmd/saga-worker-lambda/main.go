// cmd/saga-worker-lambda is cmd/saga-worker's prod twin: invoked by an
// EventBridge Scheduler rule instead of looping — POST /orders already
// fires RunSaga inline, so this only matters for orders a crashed process
// left mid-flight (docs/plan.md's "inline fast path + scheduled catch-up"
// shape).
package main

import (
	"context"
	"log"
	"time"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/order"
	"ticketing/internal/payment"
)

func handler(ctx context.Context) error {
	cfg := config.Load()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)

	lock := holdlock.New(cfg.RedisAddr)
	defer lock.Close()
	inv := inventory.New(pool, q, lock, time.Duration(cfg.HoldTTLSeconds)*time.Second, cfg.MaxSeatsPerHold)

	pay := payment.NewFakeProvider()
	pay.FailRate, pay.TimeoutRate, pay.AmbiguousRate = cfg.PaymentFailRate, cfg.PaymentTimeoutRate, cfg.PaymentAmbiguousRate
	orders := order.New(pool, q, inv, pay)

	stuck, err := q.ListStuckOrders(ctx, db.ListStuckOrdersParams{StuckAfterSeconds: 30, RowLimit: 50})
	if err != nil {
		return err
	}
	for _, orderID := range stuck {
		if err := orders.RunSaga(ctx, orderID); err != nil {
			log.Printf("saga-worker-lambda: resume order %s: %v", orderID, err)
			continue
		}
		log.Printf("saga-worker-lambda: resumed order %s", orderID)
	}
	return nil
}

func main() {
	awslambda.Start(handler)
}
