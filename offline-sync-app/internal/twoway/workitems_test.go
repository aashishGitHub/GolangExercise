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

func TestEditNotes_UpdatesAndPublishesInternalEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	id := uuid.New()
	q.EXPECT().UpdateWorkItemNotes(gomock.Any(), db.UpdateWorkItemNotesParams{ID: id, Notes: "new"}).
		Return(db.WorkItem{ID: id, Title: "Roof", Status: "Open", Notes: "new"}, nil)
	q.EXPECT().InsertDomainEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg db.InsertDomainEventParams) (db.DomainEvent, error) {
			var got WorkItemPayload
			json.Unmarshal(arg.Payload, &got)
			if arg.EventType != EventWorkItemUpdated || arg.ActorSub != "user-1" || arg.AggregateID != id || got.Notes != "new" {
				t.Errorf("event = %s actor=%s payload=%+v", arg.EventType, arg.ActorSub, got)
			}
			return db.DomainEvent{}, nil
		})

	err := NewWorkItems(passthroughTx{q: q}).EditNotes(context.Background(), id, "new", "user-1")

	if err != nil {
		t.Errorf("EditNotes() error = %v", err)
	}
}

func TestEditNotes_MissingOrDeletedItemPublishesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	q.EXPECT().UpdateWorkItemNotes(gomock.Any(), gomock.Any()).Return(db.WorkItem{}, pgx.ErrNoRows)

	err := NewWorkItems(passthroughTx{q: q}).EditNotes(context.Background(), uuid.New(), "new", "user-1")

	if !errors.Is(err, ErrWorkItemNotFound) {
		t.Errorf("EditNotes() error = %v, want ErrWorkItemNotFound (and no InsertDomainEvent)", err)
	}
}
