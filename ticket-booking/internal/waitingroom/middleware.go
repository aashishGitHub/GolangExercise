package waitingroom

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"ticketing/internal/auth"
)

// RequireAdmission gates a route behind a valid X-Admission-Token — mount
// it ONLY on the write path the waiting room protects (POST .../holds),
// never on browsing/catalog routes. Must run after auth.Middleware: it
// needs claims.Sub to bind the token to the authenticated caller, the same
// "cannot be traded" property VerifyToken enforces.
func (q *Queue) RequireAdmission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
		if err != nil {
			http.Error(w, `{"code":"invalid_id","message":"eventID must be an integer"}`, http.StatusBadRequest)
			return
		}
		claims, ok := auth.FromContext(r.Context())
		if !ok {
			http.Error(w, `{"code":"unauthorized","message":"missing auth claims"}`, http.StatusUnauthorized)
			return
		}
		token := r.Header.Get("X-Admission-Token")
		if token == "" {
			http.Error(w, `{"code":"not_admitted","message":"join the queue first"}`, http.StatusForbidden)
			return
		}
		if err := q.VerifyToken(token, claims.Sub, eventID); err != nil {
			http.Error(w, `{"code":"not_admitted","message":"invalid or expired admission token"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
