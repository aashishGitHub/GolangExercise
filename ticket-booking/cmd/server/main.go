// cmd/server is the local-dev chi lith: httpapi.NewRouter behind a plain
// net/http server. cmd/lambda (Phase 11) wraps the identical router with
// aws-lambda-go-api-proxy's httpadapter.NewV2 for API Gateway.
package main

import (
	"log"
	"net/http"

	"ticketing/internal/auth"
	"ticketing/internal/config"
	"ticketing/internal/httpapi"
)

func main() {
	cfg := config.Load()

	verifier := auth.NewVerifier(cfg.CognitoIssuerURL(), cfg.CognitoAudience)
	r := httpapi.NewRouter(verifier)

	log.Printf("ticketing server listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := http.ListenAndServe(cfg.Addr, r); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
