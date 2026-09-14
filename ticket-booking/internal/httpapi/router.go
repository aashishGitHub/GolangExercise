// Package httpapi is the chi router shared by cmd/server (local dev) and
// cmd/lambda (API Gateway, via aws-lambda-go-api-proxy) — mirrors
// offline-sync-app/internal/httpapi's split.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"ticketing/internal/auth"
)

// NewRouter builds the full route tree. /health is open; everything under
// /api/v1 is wrapped by the auth verifier — "auth gates the API, not
// browsing the static seat map assets", mirroring the sibling's "auth gates
// sync, not capture" split (catalog/availability reads are Phase 2 and
// stay outside this group; only identity-bearing routes need it).
func NewRouter(verifier *auth.Verifier) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/health", handleHealth)

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(verifier.Middleware)
		api.Get("/whoami", handleWhoami)
	})

	return r
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleWhoami(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sub":   claims.Sub,
		"email": claims.Email,
	})
}
