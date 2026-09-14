// cmd/hold-reaper-lambda is cmd/hold-reaper's prod twin. Simplified here
// to the same full-table scan as the local ticker rather than the
// theoretically-purer per-hold one-shot Scheduler invocation docs/plan.md
// describes ("the individual one-shot hold-expiry schedules are created
// at runtime by the Order Service") — wiring 50,000 individual per-hold
// schedules is real future work with no correctness payoff (passive
// expiry in the CAS predicate is what actually prevents an overbook,
// docs/plan.md decision #2); this twin proves the build/deploy path
// without inventing that machinery.
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
	"ticketing/internal/reaper"
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

	n, err := reaper.RunOnce(ctx, q, inv)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("hold-reaper-lambda: released %d expired hold(s)", n)
	}
	return nil
}

func main() {
	awslambda.Start(handler)
}
