package httpapi

import (
	"encoding/json"
	"net/http"

	"fieldsync/internal/auth"
	"fieldsync/internal/sync"
)

func syncHandler(svc sync.Syncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req sync.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		claims, _ := auth.FromContext(r.Context())
		resp, err := svc.Sync(r.Context(), claims.Sub, req)
		if err != nil {
			http.Error(w, "sync failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
