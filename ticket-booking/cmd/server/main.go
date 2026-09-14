// cmd/server is the local-dev chi lith: httpapi.NewRouter behind a plain
// net/http server. cmd/lambda (Phase 11) wraps the identical router with
// aws-lambda-go-api-proxy's httpadapter.NewV2 for API Gateway.
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/auth"
	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/httpapi"
	"ticketing/internal/storage"
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

	verifier := auth.NewVerifier(cfg.CognitoIssuerURL(), cfg.CognitoAudience)
	r := httpapi.NewRouter(verifier, q, cfg.S3LayoutsBucket, s3.PublicURL)

	log.Printf("ticketing server listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := http.ListenAndServe(cfg.Addr, r); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
