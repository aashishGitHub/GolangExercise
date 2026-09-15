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
	"ticketing/internal/ticketing"
	"ticketing/internal/waitingroom"
	"ticketing/internal/wshub"
)

// NewRouter builds the full route tree. /health and the catalog/
// availability/pricing/layout routes are open; identity-bearing routes
// (whoami, holds) are wrapped by the auth verifier — "auth gates the API,
// not browsing the static seat map assets", mirroring the sibling's "auth
// gates sync, not capture" split. wq (nil-safe) additionally gates
// POST .../holds behind X-Admission-Token — docs/plan.md's waiting room —
// and metrics (nil-safe) feeds the AIMD controller's hold-latency input.
func NewRouter(verifier *auth.Verifier, q db.Querier, inv *inventory.Service, orders *order.Service, hub *wshub.Hub, proj *projector.Projector, wq *waitingroom.Queue, metrics *waitingroom.HoldMetrics, tix *ticketing.Service, layoutsBucket string, publicURL func(bucket, key string) string) *chi.Mux {
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
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		// X-Admission-Token: RequireAdmission gates every POST .../holds
		// unconditionally (internal/waitingroom/middleware.go), not just
		// under real contention, so the browser client always sends it —
		// without it in AllowedHeaders the CORS preflight itself fails and
		// no hold request ever reaches the handler.
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Admission-Token"},
		AllowCredentials: false,
	}))

	cat := &catalogAPI{q: q, proj: proj, layoutsBucket: layoutsBucket, publicURL: publicURL}
	holds := &holdsAPI{inv: inv, q: q, metrics: metrics}
	ord := &ordersAPI{orders: orders, q: q}
	wr := &waitingRoomAPI{q: wq}
	tixAPI := &ticketingAPI{svc: tix, q: q}

	r.Get("/health", handleHealth)
	// Outside /api/v1 deliberately: this is not a REST resource, and a
	// real API Gateway WebSocket API is a completely separate endpoint
	// from the HTTP API in front of everything else here.
	r.Get("/ws", hub.ServeWS)
	// Deliberately unauthenticated: the scanned QR token IS the
	// credential, same as a paper ticket's barcode — a gate scanner
	// device carries no attendee JWT.
	r.Post("/gate/redeem", tixAPI.redeemGate)

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/events", cat.listEvents)
		api.Get("/events/{eventID}", cat.getEvent)
		api.Get("/events/{eventID}/availability", cat.getAvailability)
		api.Get("/events/{eventID}/pricing", cat.getPricing)
		api.Get("/venues/{venueID}/layout", cat.getVenueLayout)

		api.Group(func(authed chi.Router) {
			authed.Use(verifier.Middleware)
			authed.Get("/whoami", handleWhoami)

			authed.Post("/events/{eventID}/queue", wr.joinQueue)

			// POST .../holds ALONE is gated behind admission — GET/DELETE
			// and extend act on a hold the caller already legitimately
			// holds, so re-checking admission there protects nothing.
			if wq != nil {
				authed.With(wq.RequireAdmission).Post("/events/{eventID}/holds", holds.createHold)
			} else {
				authed.Post("/events/{eventID}/holds", holds.createHold)
			}
			authed.Get("/holds/{holdID}", holds.getHold)
			authed.Delete("/holds/{holdID}", holds.deleteHold)
			authed.Post("/holds/{holdID}/extend", holds.extendHold)

			authed.Post("/orders", ord.createOrder)
			authed.Get("/orders/{orderID}", ord.getOrder)

			authed.Get("/tickets/{ticketID}/qr", tixAPI.getTicketQR)
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
