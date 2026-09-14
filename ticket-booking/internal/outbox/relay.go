// Package outbox is the relay: polls domain_events for published_at IS
// NULL and PutEvents them to EventBridge. This is the safety net a direct
// dual-write would need without it — if the process crashed between
// committing the seat-state write and calling EventBridge directly, the
// row would sit there forever; the relay's next tick picks it up instead.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/google/uuid"

	"ticketing/internal/db"
)

// Publisher is the one eventbridge.Client method the relay needs — small
// enough to fake by hand in tests instead of mocking the whole SDK client.
type Publisher interface {
	PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

type Relay struct {
	q         db.Querier
	eb        Publisher
	busName   string
	batchSize int32
}

func NewRelay(q db.Querier, eb Publisher, busName string) *Relay {
	return &Relay{q: q, eb: eb, busName: busName, batchSize: 25}
}

// Envelope is the wire shape every consumer decodes — schemaVersion lets
// them evolve independently of what's already sitting unpublished.
type Envelope struct {
	EventID       uuid.UUID       `json:"eventId"`
	AggregateID   string          `json:"aggregateId"`
	EventType     string          `json:"eventType"`
	SchemaVersion int32           `json:"schemaVersion"`
	Payload       json.RawMessage `json:"payload"`
}

// RunOnce publishes up to one batch of unpublished events and reports how
// many succeeded. A failure on one entry (EventBridge error, or a rejected
// entry within an otherwise-successful call — checked via FailedEntryCount,
// not just the absence of a Go error) doesn't block the rest of the batch —
// that row simply stays unpublished for the next tick. Delivery is
// at-least-once by design, matching every future consumer's own
// idempotency requirement (processed_events dedup, Phase 7+).
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	unpublished, err := r.q.ListUnpublishedDomainEvents(ctx, r.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list unpublished domain events: %w", err)
	}

	published := 0
	for _, evt := range unpublished {
		detail, err := json.Marshal(Envelope{
			EventID: evt.EventID, AggregateID: evt.AggregateID, EventType: evt.EventType,
			SchemaVersion: evt.SchemaVersion, Payload: json.RawMessage(evt.Payload),
		})
		if err != nil {
			return published, fmt.Errorf("marshal envelope for event %s: %w", evt.EventID, err)
		}

		out, err := r.eb.PutEvents(ctx, &eventbridge.PutEventsInput{
			Entries: []types.PutEventsRequestEntry{{
				Source:       aws.String("ticketing.inventory"),
				DetailType:   aws.String(evt.EventType),
				Detail:       aws.String(string(detail)),
				EventBusName: aws.String(r.busName),
			}},
		})
		if err != nil {
			log.Printf("outbox-relay: publish event %s failed, will retry next tick: %v", evt.EventID, err)
			continue
		}
		if out.FailedEntryCount > 0 {
			log.Printf("outbox-relay: event %s rejected by EventBridge, will retry next tick", evt.EventID)
			continue
		}

		if err := r.q.MarkDomainEventPublished(ctx, evt.EventID); err != nil {
			return published, fmt.Errorf("mark event %s published: %w", evt.EventID, err)
		}
		published++
	}
	return published, nil
}
