// cmd/saga-worker is the catch-up sweep for internal/order sagas — the
// HTTP handler already fires RunSaga inline in a goroutine on every
// POST /orders, so this ticker only matters for orders a crashed process
// left mid-flight (docs/plan.md's "inline fast path + scheduled catch-up"
// shape, same as cmd/outbox-relay and cmd/hold-reaper).
package main

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/order"
	"ticketing/internal/payment"
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

	lock := holdlock.New(cfg.RedisAddr)
	defer lock.Close()
	inv := inventory.New(pool, q, lock, time.Duration(cfg.HoldTTLSeconds)*time.Second, cfg.MaxSeatsPerHold)

	pay := payment.NewFakeProvider()
	pay.FailRate, pay.TimeoutRate, pay.AmbiguousRate = cfg.PaymentFailRate, cfg.PaymentTimeoutRate, cfg.PaymentAmbiguousRate
	orders := order.New(pool, q, inv, pay)

	log.Println("saga-worker: sweeping stuck orders every 5s")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		stuck, err := q.ListStuckOrders(ctx, db.ListStuckOrdersParams{StuckAfterSeconds: 30, RowLimit: 50})
		if err != nil {
			log.Printf("saga-worker: list stuck orders: %v", err)
			continue
		}
		for _, orderID := range stuck {
			resumeOrder(ctx, orders, orderID)
		}
	}
}

func resumeOrder(ctx context.Context, orders *order.Service, orderID uuid.UUID) {
	if err := orders.RunSaga(ctx, orderID); err != nil {
		log.Printf("saga-worker: resume order %s: %v", orderID, err)
		return
	}
	log.Printf("saga-worker: resumed order %s", orderID)
}
