package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"fieldsync/internal/db"
)

// Inbox persists one raw delivery, deduplicated by delivery id.
type Inbox interface {
	Insert(ctx context.Context, system, deliveryID string, body []byte) (inserted bool, err error)
}

type DBInbox struct {
	q db.Querier
}

func NewDBInbox(q db.Querier) *DBInbox {
	return &DBInbox{q: q}
}

func (i *DBInbox) Insert(ctx context.Context, system, deliveryID string, body []byte) (bool, error) {
	n, err := i.q.InsertWebhookInbox(ctx, db.InsertWebhookInboxParams{
		System:     system,
		DeliveryID: deliveryID,
		Body:       body,
	})
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// NewHandler authenticates before it touches the inbox, and answers 202 for a
// duplicate delivery as well: the sender must stop retrying, and the first
// copy is already stored.
func NewHandler(system string, verifier *Verifier, inbox Inbox, maxBodyBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveryID := r.Header.Get(HeaderDeliveryID)

		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				reply(w, http.StatusRequestEntityTooLarge, "payload_too_large")
				return
			}
			reply(w, http.StatusBadRequest, "unreadable_body")
			return
		}

		if err := verifier.Verify(r.Header.Get(HeaderTimestamp), deliveryID, r.Header.Get(HeaderSignature), body); err != nil {
			log.Printf("webhook %s: rejected delivery %q: %v", system, deliveryID, err)
			reply(w, http.StatusUnauthorized, rejectionCode(err))
			return
		}

		if deliveryID == "" {
			reply(w, http.StatusBadRequest, "missing_delivery_id")
			return
		}

		inserted, err := inbox.Insert(r.Context(), system, deliveryID, body)
		if err != nil {
			log.Printf("webhook %s: persist delivery %s: %v", system, deliveryID, err)
			reply(w, http.StatusInternalServerError, "inbox_unavailable")
			return
		}
		if !inserted {
			reply(w, http.StatusAccepted, "duplicate")
			return
		}
		reply(w, http.StatusAccepted, "accepted")
	})
}

// NewRouter serves NewHandler at POST /webhooks/{system}; the local process and the Lambda both mount it.
func NewRouter(system string, verifier *Verifier, inbox Inbox, maxBodyBytes int64) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/"+system, NewHandler(system, verifier, inbox, maxBodyBytes))
	return mux
}

func rejectionCode(err error) string {
	switch {
	case errors.Is(err, ErrStaleTimestamp):
		return "stale_timestamp"
	case errors.Is(err, ErrMalformedHeaders):
		return "malformed_headers"
	default:
		return "bad_signature"
	}
}

func reply(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	key := "status"
	if status >= 400 {
		key = "error"
	}
	json.NewEncoder(w).Encode(map[string]string{key: code})
}
