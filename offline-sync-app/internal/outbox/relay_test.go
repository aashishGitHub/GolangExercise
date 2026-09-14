package outbox

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

// fakePublisher lets each call return a scripted response/error — enough
// control for these tests without mocking the whole eventbridge.Client.
type fakePublisher struct {
	calls      int
	failNext   bool
	rejectNext bool
}

func (f *fakePublisher) PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	f.calls++
	if f.failNext {
		return nil, errors.New("eventbridge unreachable")
	}
	if f.rejectNext {
		return &eventbridge.PutEventsOutput{FailedEntryCount: 1}, nil
	}
	return &eventbridge.PutEventsOutput{FailedEntryCount: 0}, nil
}

func sampleEvent(id uuid.UUID) db.DomainEvent {
	return db.DomainEvent{
		ID:            id,
		AggregateType: "location",
		AggregateID:   uuid.New(),
		EventType:     "location.upserted",
		SchemaVersion: 1,
		Payload:       []byte(`{"name":"Loc A"}`),
		ActorSub:      "user-1",
		OccurredAt:    pgtype.Timestamptz{Valid: true},
	}
}

func TestRelay_RunOnce_PublishesAndMarks(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	evt := sampleEvent(uuid.New())

	q.EXPECT().ListUnpublishedEvents(gomock.Any(), int32(25)).Return([]db.DomainEvent{evt}, nil)
	q.EXPECT().MarkEventPublished(gomock.Any(), evt.ID).Return(nil)

	relay := NewRelay(q, &fakePublisher{}, "fieldsync-events")
	published, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d, want 1", published)
	}
}

// TestRelay_RunOnce_LeavesUnmarkedOnPublishFailure covers the safety-net
// behavior: a transient EventBridge failure must NOT mark the event
// published — it has to still be there for the next tick.
func TestRelay_RunOnce_LeavesUnmarkedOnPublishFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	evt := sampleEvent(uuid.New())

	q.EXPECT().ListUnpublishedEvents(gomock.Any(), int32(25)).Return([]db.DomainEvent{evt}, nil)
	// No MarkEventPublished expectation — gomock fails the test if it's called.

	relay := NewRelay(q, &fakePublisher{failNext: true}, "fieldsync-events")
	published, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}

func TestRelay_RunOnce_LeavesUnmarkedOnRejectedEntry(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	evt := sampleEvent(uuid.New())

	q.EXPECT().ListUnpublishedEvents(gomock.Any(), int32(25)).Return([]db.DomainEvent{evt}, nil)

	relay := NewRelay(q, &fakePublisher{rejectNext: true}, "fieldsync-events")
	published, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}

func TestRelay_RunOnce_MultipleEventsIndependent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	ok := sampleEvent(uuid.New())
	bad := sampleEvent(uuid.New())

	q.EXPECT().ListUnpublishedEvents(gomock.Any(), int32(25)).Return([]db.DomainEvent{ok, bad}, nil)
	q.EXPECT().MarkEventPublished(gomock.Any(), ok.ID).Return(nil)

	calls := 0
	pub := &fakePublisher{}
	relay := NewRelay(q, publisherFunc(func(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("boom")
		}
		return pub.PutEvents(ctx, params, optFns...)
	}), "fieldsync-events")

	published, err := relay.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d, want 1 (second event's failure must not affect the first)", published)
	}
}

type publisherFunc func(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)

func (f publisherFunc) PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	return f(ctx, params, optFns...)
}
