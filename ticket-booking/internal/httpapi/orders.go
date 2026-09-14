package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"ticketing/internal/auth"
	"ticketing/internal/db"
	"ticketing/internal/order"
)

type ordersAPI struct {
	orders *order.Service
	q      db.Querier
}

type createOrderRequest struct {
	HoldID string `json:"holdId"`
}

// createOrder implements POST /orders — docs/plan.md: writes the order +
// saga steps and returns 202 immediately; the saga itself runs in a
// goroutine so a slow payment provider never holds the HTTP connection
// open (mirrors cmd/saga-worker's own catch-up sweep for anything that
// doesn't finish inline).
func (o *ordersAPI) createOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}

	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	holdID, err := uuid.Parse(req.HoldID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_hold_id", "holdId must be a UUID")
		return
	}

	orderID, err := o.orders.CreateOrder(ctx, holdID, claims.Sub)
	if err != nil {
		switch {
		case errors.Is(err, order.ErrHoldNotFound):
			writeError(w, http.StatusGone, "gone", "hold not found or already released")
		case errors.Is(err, order.ErrNotHoldOwner):
			writeError(w, http.StatusForbidden, "forbidden", "caller does not own this hold")
		default:
			writeError(w, http.StatusInternalServerError, "internal", "create order failed")
		}
		return
	}

	// Fire the saga in the background; POST returns immediately regardless
	// of how long the (fake, injectable-latency) payment provider takes.
	go func() {
		if err := o.orders.RunSaga(context.WithoutCancel(ctx), orderID); err != nil {
			// Logged, not surfaced — GET /orders/{id} is the poll target;
			// cmd/saga-worker's periodic sweep catches anything a crashed
			// goroutine left mid-flight.
			_ = err
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"orderId": orderID.String(), "status": "PENDING", "pollAfterMs": 500})
}

func (o *ordersAPI) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}
	orderID, err := uuid.Parse(chi.URLParam(r, "orderID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "orderID must be a UUID")
		return
	}

	ord, err := o.q.GetOrder(ctx, orderID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "order not found")
		return
	}
	if ord.UserSub != claims.Sub {
		writeError(w, http.StatusNotFound, "not_found", "order not found") // don't leak existence to a non-owner
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"orderId": ord.OrderID, "status": ord.Status, "seatIds": ord.SeatIds,
		"amountCents": ord.AmountCents, "reallocated": ord.Reallocated,
		"failureCode": ord.FailureCode.String,
	})
}
