// cmd/reconciler-lambda is cmd/reconciler's prod twin: invoked by an
// EventBridge Scheduler rule instead of looping — a single resolution
// pass plus the money-invariant check per invocation.
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
	"ticketing/internal/reconcile"
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
	rec := reconcile.New(q, pay, pay, orders)

	n, err := rec.RunOnce(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("reconciler-lambda: resolved %d stuck payment(s)", n)
	}

	seatWithNoMoney, moneyWithNoSeat, err := rec.CheckInvariant(ctx)
	if err != nil {
		return err
	}
	if seatWithNoMoney > 0 || moneyWithNoSeat > 0 {
		log.Printf("reconciler-lambda: !!! MONEY INVARIANT VIOLATED !!! seat-with-no-money=%d money-with-no-seat=%d",
			seatWithNoMoney, moneyWithNoSeat)
	}
	return nil
}

func main() {
	awslambda.Start(handler)
}
