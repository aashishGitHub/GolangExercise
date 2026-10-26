// cmd/webhook-receiver runs the whole two-way sync locally: the signed webhook
// endpoint plus the inbox worker, the forward extractor and the reverse
// loader as ticker loops. In prod each of these is its own deployable (HTTP
// endpoint, queue-triggered worker, scheduled extractor, event consumer); one
// process is a local-dev convenience, like cmd/server's in-process notifier.
package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/consume"
	"fieldsync/internal/db"
	"fieldsync/internal/twoway"
	"fieldsync/internal/webhook"
)

func main() {
	cfg, err := twoway.LoadConfig()
	if err != nil {
		log.Fatalf("webhook-receiver: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, config.Load().DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	q := db.New(pool)
	ownership := twoway.DefaultOwnership()
	applier := twoway.NewApplier(cfg.System, ownership)
	client := twoway.NewHTTPClient(cfg.ExternalBaseURL, twoway.DefaultRetryPolicy)

	worker := webhook.NewWorker(cfg.System, pool, applier.ProcessWebhook, cfg.MaxAttempts)
	extractor := twoway.NewExtractor(cfg.System, client, q, twoway.NewPgxTransactor(pool), applier, time.Now, twoway.ExtractorConfig{
		Budget: cfg.ExtractBudget, Overlap: cfg.ExtractOverlap, PageSize: cfg.ExtractPageSize,
	})
	loader := twoway.NewLoader(cfg.System, q, client, ownership)

	go loop(ctx, "inbox-worker", 2*time.Second, worker.RunOnce)
	go loop(ctx, "extractor", time.Minute, func(ctx context.Context) (int, error) {
		res, err := extractor.Run(ctx)
		return res.Issues, err
	})
	go loop(ctx, "loader", 5*time.Second, func(ctx context.Context) (int, error) {
		return consume.RunOnce(ctx, q, loader.ConsumerName(), loader.EventTypes(), loader.Handle)
	})

	verifier := webhook.NewVerifier(cfg.WebhookSecret, cfg.WebhookTolerance, time.Now)
	router := webhook.NewRouter(cfg.System, verifier, webhook.NewDBInbox(q), cfg.MaxBodyBytes)

	srv := &http.Server{Addr: cfg.WebhookAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	log.Printf("webhook-receiver: POST /webhooks/%s on %s, external system at %s", cfg.System, cfg.WebhookAddr, cfg.ExternalBaseURL)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("webhook-receiver: %v", err)
	}
}

func loop(ctx context.Context, name string, every time.Duration, run func(context.Context) (int, error)) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := run(ctx)
			if err != nil {
				log.Printf("%s: %v", name, err)
				continue
			}
			if n > 0 {
				log.Printf("%s: handled %d", name, n)
			}
		}
	}
}
