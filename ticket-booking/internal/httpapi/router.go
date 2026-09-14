// Package httpapi is the chi router shared by cmd/server (local dev) and
// cmd/lambda (API Gateway, via aws-lambda-go-api-proxy) — mirrors
// offline-sync-app/internal/httpapi's split.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the full route tree. /health is open; everything under
// /api/v1 will be wrapped by the auth verifier once Phase 1 lands it.
func NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/health", handleHealth)

	return r
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
