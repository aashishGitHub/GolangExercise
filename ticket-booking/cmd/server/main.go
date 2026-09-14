// cmd/server is the local-dev chi lith: httpapi.NewRouter behind a plain
// net/http server. cmd/lambda (Phase 11) wraps the identical router with
// aws-lambda-go-api-proxy's httpadapter.NewV2 for API Gateway.
package main

import (
	"context"
	"log"
	"net/http"
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
	"ticketing/internal/waitingroom"
	"ticketing/internal/wshub"
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

	s3, err := storage.New(ctx, cfg.S3Endpoint, cfg.AWSRegion, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("s3 client: %v", err)
	}

	lock := holdlock.New(cfg.RedisAddr)
	defer lock.Close()
	inv := inventory.New(pool, q, lock, time.Duration(cfg.HoldTTLSeconds)*time.Second, cfg.MaxSeatsPerHold)

	pay := payment.NewFakeProvider()
	pay.FailRate, pay.TimeoutRate, pay.AmbiguousRate = cfg.PaymentFailRate, cfg.PaymentTimeoutRate, cfg.PaymentAmbiguousRate
	orders := order.New(pool, q, inv, pay)

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()
	proj := projector.New(q, rdb)

	metrics := waitingroom.NewHoldMetrics(200)
	wq := waitingroom.New(rdb, cfg.WaitingRoomSecret)
	controller := waitingroom.NewController(q, rdb, pool, metrics)
	go runAIMDLoop(ctx, q, controller)

	verifier := auth.NewVerifier(cfg.CognitoIssuerURL(), cfg.CognitoAudience)
	hub := wshub.New(verifier, q, proj, rdb)
	r := httpapi.NewRouter(verifier, q, inv, orders, hub, proj, wq, metrics, cfg.S3LayoutsBucket, s3.PublicURL)

	log.Printf("ticketing server listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := http.ListenAndServe(cfg.Addr, r); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

// runAIMDLoop ticks docs/plan.md's AIMD controller at 1Hz for every event.
// Runs IN this process, sharing the exact pool and HoldMetrics the HTTP
// handlers write to — deliberately not a separate cmd/* binary: the
// pool-utilization and hold-latency signals only mean something if they
// come from the process actually serving hold requests. A real
// multi-replica deployment would need a shared metrics backend for this to
// generalize; documented in MILESTONES.md as a real gap, not fixed here.
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
