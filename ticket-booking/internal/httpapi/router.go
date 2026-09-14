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
	"ticketing/internal/db"
)

// NewRouter builds the full route tree. /health and the catalog/
// availability/pricing/layout routes are open; identity-bearing routes
// (whoami now, holds/orders from Phase 3) are wrapped by the auth verifier —
// "auth gates the API, not browsing the static seat map assets", mirroring
// the sibling's "auth gates sync, not capture" split.
func NewRouter(verifier *auth.Verifier, q db.Querier, layoutsBucket string, publicURL func(bucket, key string) string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/health", handleHealth)

	cat := &catalogAPI{q: q, layoutsBucket: layoutsBucket, publicURL: publicURL}

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/events", cat.listEvents)
		api.Get("/events/{eventID}", cat.getEvent)
		api.Get("/events/{eventID}/availability", cat.getAvailability)
		api.Get("/events/{eventID}/pricing", cat.getPricing)
		api.Get("/venues/{venueID}/layout", cat.getVenueLayout)

		api.Group(func(authed chi.Router) {
			authed.Use(verifier.Middleware)
			authed.Get("/whoami", handleWhoami)
		})
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
