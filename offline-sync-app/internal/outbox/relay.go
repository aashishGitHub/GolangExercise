// Package outbox implements the relay half of the transactional outbox: it
// re-scans domain_events for published_at IS NULL rows (written durably by
// internal/events.Publish inside the same transaction as the domain write)
// and PutEvents them to EventBridge. This is the safety net a dual-write
// would need without it — if the process crashed between committing the
// domain write and calling EventBridge directly, the row would sit there
// forever; the relay's next tick picks it up instead.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/google/uuid"

	"fieldsync/internal/db"
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

// Envelope is the wire shape consumers (photo-processor,
// dashboard-aggregator, notifier) receive — schemaVersion lets them evolve
// independently of what's already sitting unpublished.
type Envelope struct {
	ID            uuid.UUID       `json:"id"`
	AggregateType string          `json:"aggregateType"`
	AggregateID   uuid.UUID       `json:"aggregateId"`
	EventType     string          `json:"eventType"`
	SchemaVersion int32           `json:"schemaVersion"`
	Payload       json.RawMessage `json:"payload"`
	ActorSub      string          `json:"actorSub"`
	OccurredAt    time.Time       `json:"occurredAt"`
}

// RunOnce publishes up to one batch of unpublished events and reports how
// many succeeded. A failure on one entry (EventBridge error, or a rejected
// entry within an otherwise-successful call) doesn't block the rest of the
// batch — that row simply stays unpublished for the next tick. EventBridge
// delivery is at-least-once by design here, matching every consumer's own
// idempotency requirement (processed_events dedup).
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	events, err := r.q.ListUnpublishedEvents(ctx, r.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list unpublished events: %w", err)
	}

	published := 0
	for _, evt := range events {
		detail, err := json.Marshal(Envelope{
			ID:            evt.ID,
			AggregateType: evt.AggregateType,
			AggregateID:   evt.AggregateID,
			EventType:     evt.EventType,
			SchemaVersion: evt.SchemaVersion,
			Payload:       json.RawMessage(evt.Payload),
			ActorSub:      evt.ActorSub,
			OccurredAt:    evt.OccurredAt.Time,
		})
		if err != nil {
			return published, fmt.Errorf("marshal envelope for event %s: %w", evt.ID, err)
		}

		out, err := r.eb.PutEvents(ctx, &eventbridge.PutEventsInput{
			Entries: []types.PutEventsRequestEntry{{
				Source:       aws.String("fieldsync.sync"),
				DetailType:   aws.String(evt.EventType),
				Detail:       aws.String(string(detail)),
				EventBusName: aws.String(r.busName),
			}},
		})
		if err != nil {
			log.Printf("outbox-relay: publish event %s failed, will retry next tick: %v", evt.ID, err)
			continue
		}
		if out.FailedEntryCount > 0 {
			log.Printf("outbox-relay: event %s rejected by EventBridge, will retry next tick", evt.ID)
			continue
		}

		if err := r.q.MarkEventPublished(ctx, evt.ID); err != nil {
			return published, fmt.Errorf("mark event %s published: %w", evt.ID, err)
		}
		published++
	}
	return published, nil
}
