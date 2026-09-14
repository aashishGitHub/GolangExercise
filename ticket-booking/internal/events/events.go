// Package events is the transactional-outbox writer — docs/plan.md's
// mechanism for guaranteeing an EventBridge publish is never lost and
// never duplicated: Publish must be called with a Querier bound to the
// SAME pgx transaction as the seat-state write it accompanies, so a crash
// between commit and the relay's PutEvents leaves an unpublished row
// instead of a silently dropped event.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"ticketing/internal/db"
)

// SchemaVersion is stamped on every event — the relay and every consumer
// decode through this same constant, so a schema change is a version bump,
// not a silent format break.
const SchemaVersion = 1

// Publish writes one domain_events row. q MUST be bound to the same
// transaction as the write this event describes (e.g. db.New(tx), not the
// service's long-lived pool-backed Querier) — that's what makes this an
// outbox and not just an audit log.
func Publish(ctx context.Context, q db.Querier, aggregateID, eventType string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events.Publish: marshal payload: %w", err)
	}
	return q.InsertDomainEvent(ctx, db.InsertDomainEventParams{
		EventID: uuid.New(), AggregateID: aggregateID, EventType: eventType,
		SchemaVersion: SchemaVersion, Payload: body,
	})
}
