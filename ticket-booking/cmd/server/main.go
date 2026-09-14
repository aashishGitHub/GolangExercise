// cmd/server is the local-dev chi lith: internal/wiring's router behind a
// plain net/http server. cmd/server-lambda (Phase 11) wraps the IDENTICAL
// router with aws-lambda-go-api-proxy's httpadapter.NewV2 for API Gateway
// — internal/wiring exists specifically so both entrypoints share one
// dependency graph instead of two copies quietly drifting apart.
package main

import (
	"context"
	"log"
	"net/http"

	"ticketing/internal/config"
	"ticketing/internal/wiring"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	app, err := wiring.New(ctx, cfg)
	if err != nil {
		log.Fatalf("wiring: %v", err)
	}
	defer app.Pool.Close()
	defer app.Redis.Close()

	log.Printf("ticketing server listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := http.ListenAndServe(cfg.Addr, app.Router); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
