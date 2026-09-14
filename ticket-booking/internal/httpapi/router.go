// Package httpapi is the chi router shared by cmd/server (local dev) and
// cmd/lambda (API Gateway, via aws-lambda-go-api-proxy) — mirrors
// offline-sync-app/internal/httpapi's split.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"ticketing/internal/auth"
	"ticketing/internal/db"
	"ticketing/internal/inventory"
	"ticketing/internal/order"
	"ticketing/internal/projector"
	"ticketing/internal/wshub"
)

// NewRouter builds the full route tree. /health and the catalog/
// availability/pricing/layout routes are open; identity-bearing routes
// (whoami, holds) are wrapped by the auth verifier — "auth gates the API,
// not browsing the static seat map assets", mirroring the sibling's "auth
// gates sync, not capture" split.
func NewRouter(verifier *auth.Verifier, q db.Querier, inv *inventory.Service, orders *order.Service, hub *wshub.Hub, proj *projector.Projector, layoutsBucket string, publicURL func(bucket, key string) string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	// Not in the original phase list, but required — curl doesn't enforce
	// CORS, a browser does (mirrors offline-sync-app's own finding, same
	// fix). CloudFront's native cors_configuration replaces this in prod
	// (docs/plan.md "CORS" — Phase 11); local dev keeps this chi middleware
	// since there's no API Gateway locally.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	r.Get("/health", handleHealth)
	// Outside /api/v1 deliberately: this is not a REST resource, and a
	// real API Gateway WebSocket API is a completely separate endpoint
	// from the HTTP API in front of everything else here.
	r.Get("/ws", hub.ServeWS)

	cat := &catalogAPI{q: q, proj: proj, layoutsBucket: layoutsBucket, publicURL: publicURL}
	holds := &holdsAPI{inv: inv, q: q}
	ord := &ordersAPI{orders: orders, q: q}

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/events", cat.listEvents)
		api.Get("/events/{eventID}", cat.getEvent)
		api.Get("/events/{eventID}/availability", cat.getAvailability)
		api.Get("/events/{eventID}/pricing", cat.getPricing)
		api.Get("/venues/{venueID}/layout", cat.getVenueLayout)

		api.Group(func(authed chi.Router) {
			authed.Use(verifier.Middleware)
			authed.Get("/whoami", handleWhoami)

			authed.Post("/events/{eventID}/holds", holds.createHold)
			authed.Get("/holds/{holdID}", holds.getHold)
			authed.Delete("/holds/{holdID}", holds.deleteHold)
			authed.Post("/holds/{holdID}/extend", holds.extendHold)

			authed.Post("/orders", ord.createOrder)
			authed.Get("/orders/{orderID}", ord.getOrder)
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
