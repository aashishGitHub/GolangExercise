// cmd/reconciler runs internal/reconcile's 3-step resolution loop plus the
// two money-invariant checks (docs/plan.md "The saga" — reconciler),
// logging loudly (Sev-1-shaped, even without real alerting wired up yet)
// if either invariant is ever violated — it should never fire.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/inventory"
	"ticketing/internal/order"
	"ticketing/internal/payment"
	"ticketing/internal/reconcile"
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
	rec := reconcile.New(q, pay, pay, orders)

	log.Println("reconciler: running on boot, then every 60s")
	tick(ctx, rec)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		tick(ctx, rec)
	}
}

func tick(ctx context.Context, rec *reconcile.Service) {
	n, err := rec.RunOnce(ctx)
	if err != nil {
		log.Printf("reconciler: run error: %v", err)
	} else if n > 0 {
		log.Printf("reconciler: resolved %d stuck payment(s)", n)
	}

	seatWithNoMoney, moneyWithNoSeat, err := rec.CheckInvariant(ctx)
	if err != nil {
		log.Printf("reconciler: invariant check error: %v", err)
		return
	}
	if seatWithNoMoney > 0 || moneyWithNoSeat > 0 {
		log.Printf("reconciler: !!! MONEY INVARIANT VIOLATED !!! seat-with-no-money=%d money-with-no-seat=%d",
			seatWithNoMoney, moneyWithNoSeat)
	}
}
