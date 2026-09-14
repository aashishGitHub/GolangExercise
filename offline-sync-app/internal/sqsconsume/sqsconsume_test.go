package sqsconsume

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

func sqsBody(eventID uuid.UUID) string {
	return `{"detail":{"id":"` + eventID.String() + `","aggregateType":"photo","aggregateId":"` +
		uuid.New().String() + `","eventType":"photo.upserted","schemaVersion":1,"payload":{"condition":"good"},"actorSub":"user-1"}}`
}

func TestHandle_ProcessesNewMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	eventID := uuid.New()

	q.EXPECT().IsEventProcessed(gomock.Any(), db.IsEventProcessedParams{EventID: eventID, ConsumerName: "test-consumer"}).Return(false, nil)
	q.EXPECT().MarkEventProcessed(gomock.Any(), db.MarkEventProcessedParams{EventID: eventID, ConsumerName: "test-consumer"}).Return(nil)

	var handledID uuid.UUID
	resp, err := Handle(context.Background(), q, "test-consumer", events.SQSEvent{
		Records: []events.SQSMessage{{MessageId: "m1", Body: sqsBody(eventID)}},
	}, func(ctx context.Context, evt db.DomainEvent) error {
		handledID = evt.ID
		return nil
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(resp.BatchItemFailures) != 0 {
		t.Errorf("BatchItemFailures = %v, want none", resp.BatchItemFailures)
	}
	if handledID != eventID {
		t.Errorf("handler received event %s, want %s", handledID, eventID)
	}
}

// TestHandle_SkipsAlreadyProcessed covers redelivery: SQS is at-least-once,
// so the same message can arrive twice — the second delivery must not call
// the handler again, and must not be reported as a failure either (it's
// already done, so it should just be acked).
func TestHandle_SkipsAlreadyProcessed(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	eventID := uuid.New()

	q.EXPECT().IsEventProcessed(gomock.Any(), gomock.Any()).Return(true, nil)
	// No MarkEventProcessed or handler call expected.

	called := false
	resp, err := Handle(context.Background(), q, "test-consumer", events.SQSEvent{
		Records: []events.SQSMessage{{MessageId: "m1", Body: sqsBody(eventID)}},
	}, func(ctx context.Context, evt db.DomainEvent) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if called {
		t.Error("handler was called for an already-processed event")
	}
	if len(resp.BatchItemFailures) != 0 {
		t.Errorf("BatchItemFailures = %v, want none (already-processed isn't a failure)", resp.BatchItemFailures)
	}
}

// TestHandle_ReportsFailedMessageOnly covers partial-batch-failure
// reporting: one bad message in a batch of two must not cause the good
// message to be redelivered too.
func TestHandle_ReportsFailedMessageOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	goodID := uuid.New()
	badID := uuid.New()

	q.EXPECT().IsEventProcessed(gomock.Any(), db.IsEventProcessedParams{EventID: goodID, ConsumerName: "test-consumer"}).Return(false, nil)
	q.EXPECT().MarkEventProcessed(gomock.Any(), db.MarkEventProcessedParams{EventID: goodID, ConsumerName: "test-consumer"}).Return(nil)
	q.EXPECT().IsEventProcessed(gomock.Any(), db.IsEventProcessedParams{EventID: badID, ConsumerName: "test-consumer"}).Return(false, nil)

	resp, err := Handle(context.Background(), q, "test-consumer", events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "good-msg", Body: sqsBody(goodID)},
			{MessageId: "bad-msg", Body: sqsBody(badID)},
		},
	}, func(ctx context.Context, evt db.DomainEvent) error {
		if evt.ID == badID {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(resp.BatchItemFailures) != 1 || resp.BatchItemFailures[0].ItemIdentifier != "bad-msg" {
		t.Errorf("BatchItemFailures = %v, want exactly [bad-msg]", resp.BatchItemFailures)
	}
}

func TestHandle_MalformedBodyReportsFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)

	resp, err := Handle(context.Background(), q, "test-consumer", events.SQSEvent{
		Records: []events.SQSMessage{{MessageId: "m1", Body: "not json"}},
	}, func(ctx context.Context, evt db.DomainEvent) error { return nil })
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(resp.BatchItemFailures) != 1 || resp.BatchItemFailures[0].ItemIdentifier != "m1" {
		t.Errorf("BatchItemFailures = %v, want exactly [m1]", resp.BatchItemFailures)
	}
}
