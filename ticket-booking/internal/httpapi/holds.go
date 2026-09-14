package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"ticketing/internal/auth"
	"ticketing/internal/db"
	"ticketing/internal/inventory"
)

// holdsAPI holds the routes that mutate event_seats — all authenticated
// (mounted under the auth-gated group in router.go), unlike catalogAPI's
// open reads.
type holdsAPI struct {
	inv *inventory.Service
	q   db.Querier
}

type createHoldRequest struct {
	SeatOrdinals  []int32 `json:"seatOrdinals"`
	Quantity      int     `json:"quantity"`
	MaxPriceCents int32   `json:"maxPriceCents"`
	BestAvailable bool    `json:"bestAvailable"`
}

// createHold implements POST /events/{eventID}/holds — docs/plan.md's
// single endpoint handling both explicit seatOrdinals and bestAvailable.
// X-Admission-Token verification is Phase 8 (the waiting room); not yet
// wired here.
func (h *holdsAPI) createHold(w http.ResponseWriter, r *http.Request) {
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

	var req createHoldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	var hold *inventory.Hold
	if req.BestAvailable {
		hold, err = h.inv.BestAvailable(ctx, eventID, req.Quantity, req.MaxPriceCents, claims.Sub)
	} else {
		seatIDs, mapErr := h.resolveOrdinals(ctx, eventID, req.SeatOrdinals)
		if mapErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_seats", mapErr.Error())
			return
		}
		hold, err = h.inv.AcquireHold(ctx, eventID, seatIDs, claims.Sub)
	}

	writeHoldResultOrError(w, hold, err)
}

// resolveOrdinals maps client-facing seat ordinals to internal seat_ids —
// the wire/DB boundary docs/plan.md decision #5 draws: ordinals are the
// ONLY seat identity the client ever sees.
func (h *holdsAPI) resolveOrdinals(ctx context.Context, eventID int64, ordinals []int32) ([]int64, error) {
	if len(ordinals) == 0 {
		return nil, fmt.Errorf("seatOrdinals must not be empty")
	}
	rows, err := h.q.GetSeatIDsByOrdinals(ctx, db.GetSeatIDsByOrdinalsParams{EventID: eventID, Ordinals: ordinals})
	if err != nil {
		return nil, fmt.Errorf("resolve ordinals: %w", err)
	}
	if len(rows) != len(ordinals) {
		return nil, fmt.Errorf("one or more seatOrdinals do not exist for this event")
	}
	seatIDs := make([]int64, len(rows))
	for i, r := range rows {
		seatIDs[i] = r.SeatID
	}
	return seatIDs, nil
}

// getHold implements GET /holds/{holdID} — server-computed
// secondsRemaining, never a client clock (docs/plan.md).
func (h *holdsAPI) getHold(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	holdID, err := uuid.Parse(chi.URLParam(r, "holdID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "holdID must be a UUID")
		return
	}
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}

	rows, err := h.q.GetSeatsByHoldID(ctx, pgtype.UUID{Bytes: holdID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	if len(rows) == 0 {
		// 410, not 404 — nothing to retry against (docs/plan.md "Status-code semantics").
		writeError(w, http.StatusGone, "gone", "hold not found or already released/confirmed")
		return
	}
	// Ownership check: defense in depth beyond the UUID's non-enumerability
	// (fixed gap #13) — a hold's held_by must match the caller.
	if rows[0].HeldBy.String != claims.Sub {
		writeError(w, http.StatusGone, "gone", "hold not found or already released/confirmed")
		return
	}

	expiresAt := rows[0].HoldExpiresAt.Time
	remaining := int(time.Until(expiresAt).Seconds())
	if remaining < 0 {
		remaining = 0
	}

	seats := make([]map[string]any, len(rows))
	var totalCents int64
	for i, r := range rows {
		seats[i] = map[string]any{"seatId": r.SeatID, "seatOrdinal": r.SeatOrdinal, "priceCents": r.HoldPriceCents.Int32}
		totalCents += int64(r.HoldPriceCents.Int32)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"holdId": holdID.String(), "eventId": rows[0].EventID, "status": "HELD",
		"expiresAt": expiresAt.Format(timeFormat), "secondsRemaining": remaining,
		"seats": seats, "totalCents": totalCents,
	})
}

// deleteHold implements DELETE /holds/{holdID} — always 204, idempotent
// even if the hold is already gone (docs/plan.md "Release").
func (h *holdsAPI) deleteHold(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	holdID, err := uuid.Parse(chi.URLParam(r, "holdID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "holdID must be a UUID")
		return
	}
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}

	rows, err := h.q.GetSeatsByHoldID(ctx, pgtype.UUID{Bytes: holdID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	if len(rows) == 0 {
		w.WriteHeader(http.StatusNoContent) // already gone — still 204
		return
	}
	if rows[0].HeldBy.String != claims.Sub {
		// Ownership check (fixed gap #13). Report 204 anyway rather than
		// leaking whether a hold with this id exists to a non-owner.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	eventID := rows[0].EventID
	seatIDs := make([]int64, len(rows))
	for i, r := range rows {
		seatIDs[i] = r.SeatID
	}
	if err := h.inv.ReleaseHold(ctx, eventID, seatIDs, holdID, claims.Sub); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "release failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type extendHoldRequest struct {
	ExtendSeconds int `json:"extendSeconds"`
}

// extendHold implements POST /holds/{holdID}/extend — called at payment
// initiation (docs/plan.md "Extend at payment initiation").
func (h *holdsAPI) extendHold(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	holdID, err := uuid.Parse(chi.URLParam(r, "holdID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "holdID must be a UUID")
		return
	}
	claims, ok := auth.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing auth claims")
		return
	}

	var req extendHoldRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // zero value (default extension) if body is empty/absent
	extend := 180 * time.Second
	if req.ExtendSeconds > 0 {
		extend = time.Duration(req.ExtendSeconds) * time.Second
	}

	rows, err := h.q.GetSeatsByHoldID(ctx, pgtype.UUID{Bytes: holdID, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	if len(rows) == 0 || rows[0].HeldBy.String != claims.Sub {
		writeError(w, http.StatusGone, "gone", "hold not found or already released/confirmed")
		return
	}

	eventID := rows[0].EventID
	seatIDs := make([]int64, len(rows))
	for i, r := range rows {
		seatIDs[i] = r.SeatID
	}

	if _, err := h.inv.ExtendHold(ctx, eventID, seatIDs, holdID, extend); err != nil {
		writeInventoryError(w, err)
		return
	}

	newRows, err := h.q.GetSeatsByHoldID(ctx, pgtype.UUID{Bytes: holdID, Valid: true})
	if err != nil || len(newRows) == 0 {
		writeError(w, http.StatusInternalServerError, "internal", "post-extend lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expiresAt":  newRows[0].HoldExpiresAt.Time.Format(timeFormat),
		"serverTime": time.Now().UTC().Format(timeFormat),
	})
}

func writeHoldResultOrError(w http.ResponseWriter, hold *inventory.Hold, err error) {
	if err != nil {
		writeInventoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, holdResponse(hold))
}

func holdResponse(hold *inventory.Hold) map[string]any {
	seats := make([]map[string]any, len(hold.Seats))
	fenceTokens := make(map[string]string, len(hold.Seats))
	var totalCents int64
	for i, s := range hold.Seats {
		seats[i] = map[string]any{
			"seatId":      s.SeatID,
			"seatOrdinal": s.SeatOrdinal,
			"priceCents":  s.PriceCents,
		}
		fenceTokens[strconv.FormatInt(s.SeatID, 10)] = strconv.FormatInt(s.FenceToken, 10)
		totalCents += int64(s.PriceCents)
	}
	return map[string]any{
		"holdId":      hold.HoldID.String(),
		"eventId":     hold.EventID,
		"seats":       seats,
		"expiresAt":   hold.ExpiresAt.Format(timeFormat),
		"serverTime":  time.Now().UTC().Format(timeFormat),
		"totalCents":  totalCents,
		"fenceTokens": fenceTokens,
	}
}

// writeInventoryError maps internal/inventory's sentinel errors to the
// exact status codes docs/plan.md's "Status-code semantics" specifies —
// 409 SEAT_TAKEN is the dominant non-2xx during an on-sale and must be
// fast and cheap; it is the system working, not a failure.
func writeInventoryError(w http.ResponseWriter, err error) {
	var conflictErr *inventory.ConflictError
	switch {
	case errors.As(err, &conflictErr):
		writeJSON(w, http.StatusConflict, map[string]any{
			"code": "SEAT_TAKEN", "message": "one or more seats are no longer available",
			"conflicts": conflictErr.Conflicts,
		})
	case errors.Is(err, inventory.ErrHoldExpired):
		writeError(w, http.StatusConflict, "HOLD_EXPIRED", "the hold has expired")
	case errors.Is(err, inventory.ErrTooManySeats):
		writeError(w, http.StatusUnprocessableEntity, "too_many_seats", "too many seats requested in one hold")
	case errors.Is(err, inventory.ErrDuplicateSeat):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_seat", "duplicate seat in request")
	case errors.Is(err, inventory.ErrNoContiguousSeats):
		writeError(w, http.StatusConflict, "no_contiguous_seats", "no contiguous seats available for that quantity/price")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
