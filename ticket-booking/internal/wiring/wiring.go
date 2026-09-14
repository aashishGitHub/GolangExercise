// Package wiring builds the full dependency graph cmd/server and
// cmd/server-lambda BOTH need — extracted here (Phase 11) specifically so
// the two entrypoints share one wiring path instead of two copies quietly
// drifting apart. Everything process-lifetime-shaped (the AIMD ticker
// goroutine) starts here too: a Lambda execution environment is reused
// across warm invocations, so a background goroutine started at init
// behaves the same way it does in cmd/server's long-lived process — it
// just doesn't generalize across concurrent execution environments any
// more than cmd/server's in-process AIMD loop generalizes across
// replicas (the same real gap, documented in MILESTONES.md, not
// duplicated here).
package wiring

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/auth"
	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/holdlock"
	"ticketing/internal/httpapi"
	"ticketing/internal/inventory"
	"ticketing/internal/order"
	"ticketing/internal/payment"
	"ticketing/internal/projector"
	"ticketing/internal/storage"
	"ticketing/internal/ticketing"
	"ticketing/internal/waitingroom"
	"ticketing/internal/wshub"

	"github.com/go-chi/chi/v5"
)

// App holds everything a caller might need after New — currently just the
// router, but kept as a struct rather than returning *chi.Mux bare so a
// future caller (a test harness, say) can reach into the dependencies
// without another wiring rewrite.
type App struct {
	Router *chi.Mux
	Pool   *pgxpool.Pool
	Redis  *redis.Client
}

// New builds the full router + starts the AIMD loop. Callers own Close()
// (via App.Pool.Close() / App.Redis.Close()) — cmd/server defers it for
// the life of the process; cmd/server-lambda intentionally does NOT close
// on every invocation, since a warm Lambda container reuses the same pool
// across requests (closing it would defeat the whole point of a
// connection pool in a serverless context).
func New(ctx context.Context, cfg config.Config) (*App, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	q := db.New(pool)

	s3, err := storage.New(ctx, cfg.S3Endpoint, cfg.AWSRegion, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		return nil, err
	}

	lock := holdlock.New(cfg.RedisAddr)
	inv := inventory.New(pool, q, lock, time.Duration(cfg.HoldTTLSeconds)*time.Second, cfg.MaxSeatsPerHold)

	pay := payment.NewFakeProvider()
	pay.FailRate, pay.TimeoutRate, pay.AmbiguousRate = cfg.PaymentFailRate, cfg.PaymentTimeoutRate, cfg.PaymentAmbiguousRate
	orders := order.New(pool, q, inv, pay)

	tix := ticketing.New(q, s3, ticketing.NewHMACSigner(cfg.QRSigningSecret), cfg.S3TicketsBucket)
	orders.WithTicketing(tix)

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	proj := projector.New(q, rdb)

	metrics := waitingroom.NewHoldMetrics(200)
	wq := waitingroom.New(rdb, cfg.WaitingRoomSecret)
	controller := waitingroom.NewController(q, rdb, pool, metrics)
	go runAIMDLoop(ctx, q, controller)

	verifier := auth.NewVerifier(cfg.CognitoIssuerURL(), cfg.CognitoAudience)
	hub := wshub.New(verifier, q, proj, rdb)
	router := httpapi.NewRouter(verifier, q, inv, orders, hub, proj, wq, metrics, tix, cfg.S3LayoutsBucket, s3.PublicURL)

	return &App{Router: router, Pool: pool, Redis: rdb}, nil
}

func runAIMDLoop(ctx context.Context, q db.Querier, controller *waitingroom.Controller) {
	ticker := time.NewTicker(waitingroom.TickInterval)
	defer ticker.Stop()
	for range ticker.C {
		eventIDs, err := q.ListAllEventIDs(ctx)
		if err != nil {
			log.Printf("aimd: list events: %v", err)
			continue
		}
		for _, eventID := range eventIDs {
			if _, err := controller.Tick(ctx, eventID); err != nil {
				log.Printf("aimd: tick event %d: %v", eventID, err)
			}
		}
	}
}
