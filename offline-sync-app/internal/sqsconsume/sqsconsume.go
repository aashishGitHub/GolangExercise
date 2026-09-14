// Package sqsconsume is the real-AWS counterpart to internal/consume: in
// prod, EventBridge rules route each event_type to an SQS queue (with a
// DLQ), and that queue triggers a consumer Lambda directly — the message
// itself is the trigger, so there's no need to poll domain_events the way
// local dev's cmd/photo-processor etc. do. It still checks processed_events
// before calling the handler and marks it after: SQS is at-least-once, so a
// Lambda can see the same message twice.
package sqsconsume

import (
	"context"
	"encoding/json"
	"log"

	"github.com/aws/aws-lambda-go/events"

	"fieldsync/internal/consume"
	"fieldsync/internal/db"
	"fieldsync/internal/outbox"
)

// Handle processes one SQS batch and reports per-message failures via the
// partial-batch-response contract (function_response_types =
// ["ReportBatchItemFailures"] on the Lambda event source mapping in
// Terraform) — only the messages that actually failed get redelivered, not
// the whole batch.
func Handle(ctx context.Context, q db.Querier, consumerName string, sqsEvent events.SQSEvent, handle consume.Handler) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure

	for _, record := range sqsEvent.Records {
		// EventBridge-to-SQS delivers the full EventBridge event as the SQS
		// message body; "detail" is the outbox.Envelope the relay marshaled.
		var wrapper struct {
			Detail outbox.Envelope `json:"detail"`
		}
		if err := json.Unmarshal([]byte(record.Body), &wrapper); err != nil {
			log.Printf("%s: unmarshal SQS message %s: %v", consumerName, record.MessageId, err)
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
			continue
		}
		evt := wrapper.Detail

		alreadyProcessed, err := q.IsEventProcessed(ctx, db.IsEventProcessedParams{
			EventID: evt.ID, ConsumerName: consumerName,
		})
		if err != nil {
			log.Printf("%s: check processed for event %s: %v", consumerName, evt.ID, err)
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
			continue
		}
		if alreadyProcessed {
			continue // ack it — nothing to do, matches at-least-once redelivery
		}

		domainEvt := db.DomainEvent{
			ID:            evt.ID,
			AggregateType: evt.AggregateType,
			AggregateID:   evt.AggregateID,
			EventType:     evt.EventType,
			SchemaVersion: evt.SchemaVersion,
			Payload:       evt.Payload,
			ActorSub:      evt.ActorSub,
		}
		if err := handle(ctx, domainEvt); err != nil {
			log.Printf("%s: handle event %s: %v", consumerName, evt.ID, err)
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
			continue
		}

		if err := q.MarkEventProcessed(ctx, db.MarkEventProcessedParams{
			EventID: evt.ID, ConsumerName: consumerName,
		}); err != nil {
			log.Printf("%s: mark event %s processed: %v", consumerName, evt.ID, err)
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}

	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}
