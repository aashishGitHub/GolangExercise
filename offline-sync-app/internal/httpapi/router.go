// Package httpapi wires the chi router shared by the local HTTP server
// (cmd/server) and, later, the Lambda entrypoint via aws-lambda-go-api-proxy —
// one handler, both runtimes, so local dev matches prod behavior.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"fieldsync/internal/auth"
	"fieldsync/internal/db"
	"fieldsync/internal/storage"
	"fieldsync/internal/sync"
	"fieldsync/internal/wsstub"
)

// NewRouter wires the chi-lith. verifier gates every route mounted under
// /api — "auth gates sync, not capture" means capture/offline routes (added
// in later phases) must NOT be mounted inside this group. beginner drives
// every write through one transaction (upserts + domain_events rows); q
// serves the plain read-only routes, which need no transaction. hub is the
// local WebSocket stub (see internal/wsstub's package comment on why this
// can't be a separate process the way the other consumers are).
func NewRouter(verifier *auth.Verifier, q db.Querier, beginner sync.Beginner, store *storage.Storage, hub *wsstub.Hub) http.Handler {
	syncSvc := sync.NewTxRunner(beginner)
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		// Vite dev server origin; CloudFront origin replaces this in prod.
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	r.Get("/health", healthHandler)
	// Outside the header-authenticated group: a WebSocket handshake can't
	// carry an Authorization header, so wsHandler verifies the token itself
	// from a query param.
	r.Get("/ws", wsHandler(verifier, q, hub))

	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Get("/api/whoami", whoamiHandler)
		r.Post("/api/sync", syncHandler(syncSvc))
		r.Get("/api/locations/{id}", getLocationHandler(q))
		r.Get("/api/sites/{id}", getSiteHandler(q))
		r.Post("/api/sites/{id}/photos/presign", presignHandler(store))
		r.Post("/api/sites/{id}/photos/{photoId}/confirm", confirmHandler(store, syncSvc))
	})

	return r
}

// whoamiHandler is a minimal proof that JWT verification + claim extraction
// works end-to-end; /api/sync and friends land on top of this in Phase 2.
func whoamiHandler(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"sub": claims.Sub, "email": claims.Email})
}
