// Package notifier reacts to "photo.upserted" events and pushes to every
// WebSocket connection subscribed to the affected location — real-time
// visibility for other field workers/dashboard viewers, instead of only
// pull-on-login (per the original architecture's real-time-push driver).
package notifier

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

const ConsumerName = "notifier"

var EventTypes = []string{"photo.upserted"}

// Pusher mirrors the API Gateway Management API's PostToConnection contract
// (connection id + payload) so this logic doesn't change if wsstub.Hub is
// ever swapped for a real Management API client in a genuinely distributed
// deployment.
type Pusher interface {
	Push(ctx context.Context, connectionID string, message []byte) error
}

type photoPayload struct {
	ID               uuid.UUID `json:"id"`
	SiteAssessmentID uuid.UUID `json:"siteAssessmentId"`
	Condition        string    `json:"condition"`
}

type pushMessage struct {
	Type      string    `json:"type"`
	PhotoID   uuid.UUID `json:"photoId"`
	Condition string    `json:"condition"`
}

func Handle(ctx context.Context, q db.Querier, pusher Pusher, evt db.DomainEvent) error {
	var payload photoPayload
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal photo payload: %w", err)
	}

	site, err := q.GetSiteAssessment(ctx, payload.SiteAssessmentID)
	if err != nil {
		return fmt.Errorf("get site assessment %s: %w", payload.SiteAssessmentID, err)
	}

	conns, err := q.ListWSConnectionsByLocation(ctx, site.LocationID)
	if err != nil {
		return fmt.Errorf("list ws connections for location %s: %w", site.LocationID, err)
	}
	if len(conns) == 0 {
		return nil
	}

	msg, err := json.Marshal(pushMessage{Type: "photo.upserted", PhotoID: payload.ID, Condition: payload.Condition})
	if err != nil {
		return fmt.Errorf("marshal push message: %w", err)
	}

	for _, conn := range conns {
		if err := pusher.Push(ctx, conn.ConnectionID, msg); err != nil {
			return fmt.Errorf("push to connection %s: %w", conn.ConnectionID, err)
		}
	}
	return nil
}
