// Package events implements the durable half of the transactional outbox:
// Publish writes one domain_events row using whatever db.Querier the caller
// is already inside a transaction with — the row is the thing that's
// transactionally consistent with the domain write, not an EventBridge call.
// The actual PutEvents to EventBridge happens later, out of band, in
// cmd/outbox-relay, which re-scans published_at IS NULL rows. This is what
// makes the whole thing crash-safe: if the process dies between committing
// the domain write and calling EventBridge, the relay picks it up on its
// next tick instead of the event being silently lost.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

// SchemaVersion is stamped on every event so consumers (and the relay) can
// evolve the payload shape without breaking rows already sitting unpublished.
const SchemaVersion = 1

type Event struct {
	AggregateType string
	AggregateID   uuid.UUID
	Type          string
	Payload       any
	ActorSub      string
}

func Publish(ctx context.Context, q db.Querier, evt Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	_, err = q.InsertDomainEvent(ctx, db.InsertDomainEventParams{
		AggregateType: evt.AggregateType,
		AggregateID:   evt.AggregateID,
		EventType:     evt.Type,
		SchemaVersion: SchemaVersion,
		Payload:       payload,
		ActorSub:      evt.ActorSub,
	})
	if err != nil {
		return fmt.Errorf("insert domain event %s for %s %s: %w", evt.Type, evt.AggregateType, evt.AggregateID, err)
	}
	return nil
}
