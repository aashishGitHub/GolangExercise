package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"ticketing/internal/db"
	"ticketing/internal/seatmap"
)

// catalogAPI holds the dependencies the catalog/availability/pricing/layout
// routes need. These routes stay OUTSIDE the auth-gated group — "auth gates
// the API, not browsing the static seat map assets" (router.go).
type catalogAPI struct {
	q             db.Querier
	layoutsBucket string
	publicURL     func(bucket, key string) string
}

func (c *catalogAPI) listEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	limit := int32(20)
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = int32(v)
	}
	var cursor int64
	if v, err := strconv.ParseInt(q.Get("cursor"), 10, 64); err == nil {
		cursor = v
	}

	events, err := c.q.ListEvents(ctx, db.ListEventsParams{
		Search: q.Get("q"), AfterID: cursor, RowLimit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list events failed")
		return
	}

	type eventSummary struct {
		EventID        int64  `json:"eventId"`
		Title          string `json:"title"`
		Artist         string `json:"artist"`
		VenueID        int64  `json:"venueId"`
		StartsAt       string `json:"startsAt"`
		OnsaleAt       string `json:"onsaleAt"`
		MinPriceCents  int32  `json:"minPriceCents"`
		AvailableCount int64  `json:"availableCount"`
	}

	out := make([]eventSummary, 0, len(events))
	var nextCursor int64
	for _, e := range events {
		minPrice, _ := c.q.MinEventPriceCents(ctx, e.EventID)
		available, _ := c.q.CountAvailableSeats(ctx, e.EventID)
		out = append(out, eventSummary{
			EventID: e.EventID, Title: e.Title, Artist: e.Artist, VenueID: e.VenueID,
			StartsAt: e.StartsAt.Time.Format(timeFormat), OnsaleAt: e.OnsaleAt.Time.Format(timeFormat),
			MinPriceCents: minPrice, AvailableCount: available,
		})
		nextCursor = e.EventID
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": out, "nextCursor": nextCursor})
}

const timeFormat = "2006-01-02T15:04:05Z07:00"

func (c *catalogAPI) getEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "eventID must be an integer")
		return
	}

	event, err := c.q.GetEvent(ctx, eventID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "event not found")
		return
	}

	layoutKey := fmt.Sprintf("venues/%d/%d/layout.json", event.VenueID, event.LayoutVersion)

	writeJSON(w, http.StatusOK, map[string]any{
		"eventId":       event.EventID,
		"title":         event.Title,
		"artist":        event.Artist,
		"venueId":       event.VenueID,
		"startsAt":      event.StartsAt.Time.Format(timeFormat),
		"onsaleAt":      event.OnsaleAt.Time.Format(timeFormat),
		"layoutVersion": event.LayoutVersion,
		"layoutUrl":     c.publicURL(c.layoutsBucket, layoutKey),
		"pricingUrl":    fmt.Sprintf("/api/v1/events/%d/pricing", event.EventID),
		// Filled in once cmd/projector + /ws land (Phase 7).
		"wsUrl":     "",
		"saleState": saleStateOf(event.Status),
	})
}

func saleStateOf(dbStatus string) string {
	switch dbStatus {
	case "ON_SALE":
		return "onsale"
	case "CLOSED":
		return "closed"
	default:
		return "pre"
	}
}

// getVenueLayout redirects to the immutable layout.json object rather than
// proxying it — in prod this is a CloudFront/OAC-fronted URL; locally it's
// MinIO's anonymous-download URL. Same-prefix convention: seats.bin sits
// next to layout.json at the same venues/{id}/{version}/ path, so a client
// following this redirect can derive the second URL without a second route.
func (c *catalogAPI) getVenueLayout(w http.ResponseWriter, r *http.Request) {
	venueID, err := strconv.ParseInt(chi.URLParam(r, "venueID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "venueID must be an integer")
		return
	}

	version := r.URL.Query().Get("v")
	if version == "" {
		venue, err := c.q.GetVenue(r.Context(), venueID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "venue not found")
			return
		}
		version = strconv.FormatInt(int64(venue.LayoutVersion), 10)
	}

	key := fmt.Sprintf("venues/%d/%s/layout.json", venueID, version)
	http.Redirect(w, r, c.publicURL(c.layoutsBucket, key), http.StatusFound)
}

// getAvailability is the Phase 2 DB-scan path — Index Only Scan on
// event_seats_cover_idx. Superseded by the Redis-backed projector in
// Phase 7; this stays as the cold/down fallback.
func (c *catalogAPI) getAvailability(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "eventID must be an integer")
		return
	}

	rows, err := c.q.ListEventSeatsForAvailability(r.Context(), eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "availability query failed")
		return
	}
	if len(rows) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "event has no seats")
		return
	}

	packed := seatmap.New(len(rows))
	for _, row := range rows {
		// ORDER BY seat_ordinal in the query means row i IS ordinal i.
		seatmap.Set(packed, int(row.SeatOrdinal), seatmap.StateFromDB(row.Status, row.Sellable))
	}

	etag := `"` + sha256hex(packed) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Seat-Count", strconv.Itoa(len(rows)))
	// X-Seatmap-Version: a real monotonic seq is Phase 7's projector's job
	// (Redis INCR per event); the DB-scan path has no cheap equivalent, so
	// it's omitted here rather than faked — absence, not a wrong value.
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(packed)
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (c *catalogAPI) getPricing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "eventID must be an integer")
		return
	}

	tiers, err := c.q.ListEventPriceTiers(ctx, eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list price tiers failed")
		return
	}
	closedSections, err := c.q.ListClosedSectionsForEvent(ctx, eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list closed sections failed")
		return
	}

	type tierOut struct {
		TierID     string `json:"tierId"`
		Name       string `json:"name"`
		PriceCents int32  `json:"priceCents"`
	}
	out := make([]tierOut, len(tiers))
	for i, t := range tiers {
		out[i] = tierOut{TierID: t.Tier, Name: t.Tier, PriceCents: t.PriceCents}
	}

	w.Header().Set("Cache-Control", "max-age=60, stale-while-revalidate=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"priceVersion":   1,
		"tiers":          out,
		"closedSections": closedSections,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
