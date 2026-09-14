package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"ticketing/internal/auth"
	"ticketing/internal/waitingroom"
)

type waitingRoomAPI struct {
	q *waitingroom.Queue
}

// joinQueue implements POST /events/{eventID}/queue — docs/plan.md: 202
// with a position while queued, 200 with an admissionToken once past the
// cursor. A retry with the same sub is safe: Queue.Join's ZADD NX means
// re-joining never loses your place in line.
func (h *waitingRoomAPI) joinQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "eventID must be an integer")
		return
	}
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}

	result, err := h.q.Join(ctx, eventID, claims.Sub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "join queue failed")
		return
	}

	if result.Admitted {
		writeJSON(w, http.StatusOK, map[string]string{"admissionToken": result.Token})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"position": result.Position, "etaSeconds": result.EtaSeconds, "pollAfterMs": 2000,
	})
}
