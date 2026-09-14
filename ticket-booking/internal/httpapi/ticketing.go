package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"ticketing/internal/auth"
	"ticketing/internal/db"
	"ticketing/internal/ticketing"
)

type ticketingAPI struct {
	svc *ticketing.Service
	q   db.Querier
}

// getTicketQR implements GET /tickets/{id}/qr — a short-TTL presigned URL,
// never the image bytes directly (docs/plan.md: the tickets bucket is
// private with no CDN, presigned URLs are its ONLY serving mechanism).
// Ownership-checked: only the order's own user_sub may fetch its QR.
func (h *ticketingAPI) getTicketQR(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, err := uuid.Parse(chi.URLParam(r, "ticketID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "ticketID must be a UUID")
		return
	}
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}

	ticket, err := h.q.GetTicket(ctx, ticketID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "ticket not found")
		return
	}
	order, err := h.q.GetOrder(ctx, ticket.OrderID)
	if err != nil || order.UserSub != claims.Sub {
		// Same 404 whether the ticket doesn't exist or belongs to someone
		// else — a 403 would confirm the ticket ID is real to a caller
		// probing IDs they don't own.
		writeError(w, http.StatusNotFound, "not_found", "ticket not found")
		return
	}

	url, expiresAt, err := h.svc.PresignQR(ctx, ticketID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "presign failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url, "expiresAt": expiresAt.Format(timeFormat)})
}

type redeemRequest struct {
	Token string `json:"token"`
}

// redeemGate implements POST /gate/redeem — deliberately UNAUTHENTICATED
// (mounted outside the auth-gated group in router.go): the scanned QR
// token IS the credential, the same way a paper ticket's barcode is. A
// gate scanner device has no reason to carry an attendee's own JWT.
func (h *ticketingAPI) redeemGate(w http.ResponseWriter, r *http.Request) {
	var req redeemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "malformed request")
		return
	}

	err := h.svc.Redeem(r.Context(), req.Token)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "redeemed"})
	case errors.Is(err, ticketing.ErrAlreadyRedeemed):
		writeError(w, http.StatusConflict, "already_redeemed", "ticket already redeemed or revoked")
	case errors.Is(err, ticketing.ErrBadSignature), errors.Is(err, ticketing.ErrMalformed):
		writeError(w, http.StatusForbidden, "bad_signature", "invalid or tampered ticket")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "redeem failed")
	}
}
