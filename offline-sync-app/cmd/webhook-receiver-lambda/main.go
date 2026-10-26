// cmd/webhook-receiver-lambda serves webhook.NewRouter behind API Gateway HTTP API (payload v2.0).
// Setup lives in main, not init, so the test can build the adapter without WEBHOOK_SECRET or a database.
package main

import (
	"context"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/config"
	"fieldsync/internal/db"
	"fieldsync/internal/twoway"
	"fieldsync/internal/webhook"
)

func newAdapter(cfg twoway.Config, inbox webhook.Inbox) *httpadapter.HandlerAdapterV2 {
	verifier := webhook.NewVerifier(cfg.WebhookSecret, cfg.WebhookTolerance, time.Now)
	return httpadapter.NewV2(webhook.NewRouter(cfg.System, verifier, inbox, cfg.MaxBodyBytes))
}

func main() {
	cfg, err := twoway.LoadConfig()
	if err != nil {
		log.Fatalf("webhook-receiver-lambda: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), config.Load().DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	lambda.Start(newAdapter(cfg, webhook.NewDBInbox(db.New(pool))).ProxyWithContext)
}
