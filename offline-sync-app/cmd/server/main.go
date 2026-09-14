// cmd/server runs the chi-lith as a plain HTTP server for local dev.
// The same internal/httpapi.NewRouter() will later be wrapped by
// aws-lambda-go-api-proxy in cmd/lambda for dev/prod parity.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/auth"
	"fieldsync/internal/config"
	"fieldsync/internal/consume"
	"fieldsync/internal/db"
	"fieldsync/internal/httpapi"
	"fieldsync/internal/notifier"
	"fieldsync/internal/storage"
	"fieldsync/internal/wsstub"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	store, err := storage.New(ctx, cfg.S3Endpoint, cfg.AWSRegion, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("init S3 client: %v", err)
	}
	// Real AWS provisions the bucket via Terraform; local dev has no apply
	// step, so ensure it exists against MinIO on startup.
	if err := store.EnsureBucket(ctx); err != nil {
		log.Fatalf("ensure S3 bucket %q: %v", cfg.S3Bucket, err)
	}

	verifier := auth.NewVerifier(cfg.CognitoIssuer, cfg.CognitoAudience)
	q := db.New(pool)
	hub := wsstub.NewHub()

	// notifier is the one consumer that must run in-process (see
	// internal/wsstub's package comment) — every other consumer is its own
	// binary (cmd/outbox-relay, cmd/photo-processor, cmd/dashboard-aggregator).
	go runNotifierLoop(ctx, q, hub)

	router := httpapi.NewRouter(verifier, q, pool, store, hub)

	log.Printf("fieldsync server listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := http.ListenAndServe(cfg.Addr, router); err != nil {
		log.Fatal(err)
	}
}

func runNotifierLoop(ctx context.Context, q db.Querier, hub *wsstub.Hub) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		n, err := consume.RunOnce(ctx, q, notifier.ConsumerName, notifier.EventTypes,
			func(ctx context.Context, evt db.DomainEvent) error {
				return notifier.Handle(ctx, q, hub, evt)
			})
		if err != nil {
			log.Printf("notifier: RunOnce error: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("notifier: processed %d event(s)", n)
		}
	}
}
