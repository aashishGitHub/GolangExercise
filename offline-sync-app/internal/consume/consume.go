// Package consume is the shared "poll domain_events, dispatch, mark
// processed" loop every consumer (photo-processor, dashboard-aggregator,
// notifier) uses. In prod, EventBridge rules fan each event_type out to an
// SQS queue that triggers a Lambda; locally, since no free SQS/Lambda-event
// emulator survived the LocalStack licensing discovery in Phase 3, each
// consumer instead polls domain_events directly, guarded by the same
// processed_events idempotency table a real SQS-triggered Lambda would use.
// The dispatch/idempotency contract is identical either way — only the
// transport differs.
package consume

import (
	"context"
	"fmt"
	"log"

	"fieldsync/internal/db"
)

type Handler func(ctx context.Context, evt db.DomainEvent) error

// RunOnce dispatches up to one batch of events of the given types that this
// consumerName hasn't processed yet. A handler error doesn't block the rest
// of the batch — that event just isn't marked processed, so it's retried
// next tick (at-least-once, same as the outbox-relay's EventBridge delivery).
func RunOnce(ctx context.Context, q db.Querier, consumerName string, eventTypes []string, handle Handler) (int, error) {
	events, err := q.ListUnprocessedEventsForConsumer(ctx, db.ListUnprocessedEventsForConsumerParams{
		ConsumerName: consumerName,
		EventTypes:   eventTypes,
		Limit:        25,
	})
	if err != nil {
		return 0, fmt.Errorf("list unprocessed events for %s: %w", consumerName, err)
	}

	processed := 0
	for _, evt := range events {
		if err := handle(ctx, evt); err != nil {
			log.Printf("%s: handle event %s failed, will retry next tick: %v", consumerName, evt.ID, err)
			continue
		}
		if err := q.MarkEventProcessed(ctx, db.MarkEventProcessedParams{
			EventID:      evt.ID,
			ConsumerName: consumerName,
		}); err != nil {
			return processed, fmt.Errorf("mark event %s processed by %s: %w", evt.ID, consumerName, err)
		}
		processed++
	}
	return processed, nil
}
