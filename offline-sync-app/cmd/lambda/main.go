// cmd/lambda wraps the exact same chi router as cmd/server behind
// aws-lambda-go-api-proxy, targeting API Gateway HTTP API's v2 payload
// format — one handler, both runtimes, so local dev matches prod behavior.
// Built as a provided.al2023 custom runtime: the binary must be named
// "bootstrap" (see infra/terraform/modules/compute's build step).
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/auth"
	"fieldsync/internal/config"
	"fieldsync/internal/db"
	"fieldsync/internal/httpapi"
	"fieldsync/internal/storage"
	"fieldsync/internal/wsstub"
)

var adapter *httpadapter.HandlerAdapterV2

func init() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	store, err := storage.New(ctx, cfg.S3Endpoint, cfg.AWSRegion, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("init S3 client: %v", err)
	}
	// No store.EnsureBucket here, unlike cmd/server: Terraform provisions the
	// real bucket (infra/terraform/modules/storage) — a cold-start CreateBucket
	// call would be redundant IAM surface a Lambda has no business holding.

	verifier := auth.NewVerifier(cfg.CognitoIssuer, cfg.CognitoAudience)
	// The /ws route this hub backs is unreachable in real deployment — HTTP
	// API (this Lambda's actual integration) doesn't do WebSocket at all; a
	// genuinely separate API Gateway WebSocket API + notifier Lambda would
	// own that in a real distributed deployment. Constructed anyway purely
	// so this Lambda can share httpapi.NewRouter's signature with cmd/server.
	router := httpapi.NewRouter(verifier, db.New(pool), pool, store, wsstub.NewHub())
	adapter = httpadapter.NewV2(router)
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return adapter.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(handler)
}
