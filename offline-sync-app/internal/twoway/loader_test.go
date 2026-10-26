package twoway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

type patchCall struct {
	externalID string
	patch      IssuePatch
	key        string
}

type fakePusher struct {
	calls []patchCall
	reply ExternalIssue
	err   error
}

func (p *fakePusher) Patch(_ context.Context, externalID string, patch IssuePatch, key string) (ExternalIssue, error) {
	p.calls = append(p.calls, patchCall{externalID: externalID, patch: patch, key: key})
	return p.reply, p.err
}

type loaderFixture struct {
	loader *Loader
	q      *mocks.MockQuerier
	pusher *fakePusher
	evt    db.DomainEvent
}

func newLoaderFixture(t *testing.T) *loaderFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	itemID := uuid.New()
	payload, _ := json.Marshal(WorkItemPayload{ID: itemID, Title: "Roof", Status: "Open", Notes: "internal notes"})
	f := &loaderFixture{
		q:      mocks.NewMockQuerier(ctrl),
		pusher: &fakePusher{reply: ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Description: "internal notes", Version: 8}},
		evt:    db.DomainEvent{ID: uuid.New(), AggregateID: itemID, EventType: EventWorkItemUpdated, ActorSub: "user-1", Payload: payload},
	}
	f.loader = NewLoader(testSystem, f.q, f.pusher, DefaultOwnership())
	return f
}

func (f *loaderFixture) expectLink() {
	f.q.EXPECT().GetExternalLinkByInternal(gomock.Any(), db.GetExternalLinkByInternalParams{System: testSystem, InternalID: f.evt.AggregateID}).
		Return(db.ExternalIDMap{ExternalID: "MJ-1", InternalID: f.evt.AggregateID}, nil)
}

func TestHandle_PushesOnlyInternalOwnedFieldsWithTheEventIDAsIdempotencyKey(t *testing.T) {
	f := newLoaderFixture(t)
	f.expectLink()
	f.q.EXPECT().UpsertSyncPushed(gomock.Any(), db.UpsertSyncPushedParams{
		System: testSystem, InternalID: f.evt.AggregateID, ExternalVersion: 8, ContentHash: ContentOf(f.pusher.reply).Hash(),
	}).Return(nil)

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(f.pusher.calls) != 1 {
		t.Fatalf("Patch called %d times, want 1", len(f.pusher.calls))
	}
	call := f.pusher.calls[0]
	if call.externalID != "MJ-1" || call.key != f.evt.ID.String() {
		t.Errorf("call = %+v, want external id MJ-1 and the event id as key", call)
	}
	if call.patch.Summary != nil || call.patch.Status != nil || call.patch.Description == nil || *call.patch.Description != "internal notes" {
		t.Errorf("patch = %+v, want only description=internal notes", call.patch)
	}
}

func TestHandle_SkipsEventsTheExternalSystemCaused(t *testing.T) {
	f := newLoaderFixture(t)
	f.evt.ActorSub = OriginActor(testSystem)

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(f.pusher.calls) != 0 {
		t.Errorf("Patch called %d times, want 0: never reflect an external change back", len(f.pusher.calls))
	}
}

func TestHandle_UnlinkedWorkItemIsSkipped(t *testing.T) {
	f := newLoaderFixture(t)
	f.q.EXPECT().GetExternalLinkByInternal(gomock.Any(), gomock.Any()).Return(db.ExternalIDMap{}, pgx.ErrNoRows)

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(f.pusher.calls) != 0 {
		t.Errorf("Patch called %d times, want 0", len(f.pusher.calls))
	}
}

func TestHandle_NothingInternalOwnedMeansNothingToPush(t *testing.T) {
	f := newLoaderFixture(t)
	f.loader = NewLoader(testSystem, f.q, f.pusher, Ownership{FieldTitle: OwnerExternal, FieldStatus: OwnerExternal, FieldNotes: OwnerExternal})
	f.expectLink()

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(f.pusher.calls) != 0 {
		t.Errorf("Patch called %d times, want 0", len(f.pusher.calls))
	}
}

func TestHandle_ExhaustedRetriesGoToDeadLettersAndTheEventIsConsumed(t *testing.T) {
	f := newLoaderFixture(t)
	f.expectLink()
	f.pusher.err = &RetryExhaustedError{Attempts: 5, Last: &APIError{Status: 503}}
	f.q.EXPECT().InsertDeadLetter(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg db.InsertDeadLetterParams) error {
			if arg.Source != "loader" || arg.System != testSystem || arg.RefID != f.evt.ID.String() || arg.Attempts != 5 || arg.Reason == "" {
				t.Errorf("dead letter = %+v, want loader/%s/%s with 5 attempts and a reason", arg, testSystem, f.evt.ID)
			}
			return nil
		})

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Errorf("Handle() error = %v, want nil so consume marks the event processed", err)
	}
}

func TestHandle_PermanentClientErrorIsDeadLetteredImmediately(t *testing.T) {
	f := newLoaderFixture(t)
	f.expectLink()
	f.pusher.err = &APIError{Status: 404, Body: "issue not found"}
	f.q.EXPECT().InsertDeadLetter(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg db.InsertDeadLetterParams) error {
			if arg.Attempts != 1 {
				t.Errorf("attempts = %d, want 1", arg.Attempts)
			}
			return nil
		})

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Errorf("Handle() error = %v, want nil", err)
	}
}

func TestHandle_NonRetryableInfraErrorLeavesTheEventForTheNextTick(t *testing.T) {
	f := newLoaderFixture(t)
	f.expectLink()
	f.pusher.err = context.Canceled

	err := f.loader.Handle(context.Background(), f.evt)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Handle() error = %v, want it returned (and no InsertDeadLetter expected)", err)
	}
}

func TestHandle_MalformedPayloadIsDeadLettered(t *testing.T) {
	f := newLoaderFixture(t)
	f.evt.Payload = []byte(`not json`)
	f.q.EXPECT().InsertDeadLetter(gomock.Any(), gomock.Any()).Return(nil)

	if err := f.loader.Handle(context.Background(), f.evt); err != nil {
		t.Errorf("Handle() error = %v, want nil", err)
	}
}
