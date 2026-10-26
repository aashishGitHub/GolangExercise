// cmd/graphql serves the read-only GraphQL API over FieldSync's data on its own port, so it can
// scale and be rate-limited independently of cmd/server's REST sync API.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/auth"
	"fieldsync/internal/config"
	"fieldsync/internal/graph"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	serverCfg := graph.DefaultServerConfig()
	serverCfg.Introspection = cfg.Env == "local"

	verifier := auth.NewVerifier(cfg.CognitoIssuer, cfg.CognitoAudience)
	gql := graph.NewHandler(graph.NewPostgresStore(pool), verifier.Middleware, serverCfg)

	mux := http.NewServeMux()
	mux.Handle("POST /graphql", gql)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	addr := getenv("GRAPHQL_ADDR", ":8081")
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      serverCfg.RequestTimeout + 5*time.Second,
	}
	log.Printf("fieldsync graphql listening on %s (env=%s, introspection=%t)", addr, cfg.Env, serverCfg.Introspection)
	log.Fatal(server.ListenAndServe())
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
