package twoway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

const testSystem = "mockjira"

type applyFixture struct {
	applier    *Applier
	q          *mocks.MockQuerier
	internalID uuid.UUID
}

func newApplyFixture(t *testing.T) *applyFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &applyFixture{q: mocks.NewMockQuerier(ctrl), internalID: uuid.New()}
	f.applier = NewApplier(testSystem, DefaultOwnership())
	f.applier.newID = func() uuid.UUID { return f.internalID }
	return f
}

func (f *applyFixture) expectLink(issueID string) {
	f.q.EXPECT().InsertExternalLink(gomock.Any(), db.InsertExternalLinkParams{System: testSystem, ExternalID: issueID, InternalID: f.internalID}).Return(nil)
	f.q.EXPECT().GetExternalLink(gomock.Any(), db.GetExternalLinkParams{System: testSystem, ExternalID: issueID}).Return(db.ExternalIDMap{InternalID: f.internalID}, nil)
}

func (f *applyFixture) expectVersionAdvance(issueID string, version int64, err error) {
	f.q.EXPECT().AdvanceLinkVersion(gomock.Any(), db.AdvanceLinkVersionParams{System: testSystem, ExternalID: issueID, Version: version}).Return(db.ExternalIDMap{}, err)
}

func (f *applyFixture) expectExistingItem(item db.WorkItem) {
	item.ID = f.internalID
	f.q.EXPECT().GetWorkItemForUpdate(gomock.Any(), f.internalID).Return(item, nil)
}

func (f *applyFixture) expectEvent(t *testing.T, wantType string, wantPayload WorkItemPayload) {
	t.Helper()
	f.q.EXPECT().InsertDomainEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg db.InsertDomainEventParams) (db.DomainEvent, error) {
			var got WorkItemPayload
			if err := json.Unmarshal(arg.Payload, &got); err != nil {
				t.Fatalf("event payload: %v", err)
			}
			if arg.EventType != wantType || arg.ActorSub != OriginActor(testSystem) || got != wantPayload || arg.AggregateID != f.internalID {
				t.Errorf("event = %s actor=%s payload=%+v, want %s actor=%s payload=%+v",
					arg.EventType, arg.ActorSub, got, wantType, OriginActor(testSystem), wantPayload)
			}
			return db.DomainEvent{}, nil
		})
}

func TestApply_NewIssueCreatesItemAndEvent(t *testing.T) {
	f := newApplyFixture(t)
	issue := ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Description: "from jira", Version: 1}
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 1, nil)
	f.q.EXPECT().GetWorkItemForUpdate(gomock.Any(), f.internalID).Return(db.WorkItem{}, pgx.ErrNoRows)
	f.q.EXPECT().UpsertWorkItem(gomock.Any(), db.UpsertWorkItemParams{ID: f.internalID, Title: "Roof", Status: "Open", Notes: "from jira"}).Return(db.WorkItem{}, nil)
	f.expectEvent(t, EventWorkItemUpdated, WorkItemPayload{ID: f.internalID, Title: "Roof", Status: "Open", Notes: "from jira"})

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook, issue)

	if err != nil || got != OutcomeApplied {
		t.Errorf("Apply() = %q, %v, want applied", got, err)
	}
}

func TestApply_OlderVersionIsIgnoredStaleAndWritesNothing(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 2, pgx.ErrNoRows)

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook, ExternalIssue{ID: "MJ-1", Version: 2})

	if err != nil || got != OutcomeIgnoredStale {
		t.Errorf("Apply() = %q, %v, want ignored_stale", got, err)
	}
}

func TestApply_ExternalFieldsApplyInternalFieldsKept(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 3, nil)
	f.expectExistingItem(db.WorkItem{Title: "Roof", Status: "Open", Notes: "internal notes"})
	f.q.EXPECT().UpsertWorkItem(gomock.Any(), db.UpsertWorkItemParams{ID: f.internalID, Title: "Roof", Status: "Done", Notes: "internal notes"}).Return(db.WorkItem{}, nil)
	f.expectEvent(t, EventWorkItemUpdated, WorkItemPayload{ID: f.internalID, Title: "Roof", Status: "Done", Notes: "internal notes"})

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook,
		ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Done", Description: "internal notes", Version: 3})

	if err != nil || got != OutcomeApplied {
		t.Errorf("Apply() = %q, %v, want applied", got, err)
	}
}

func TestApply_ExternalEditOfInternalFieldIsConflictAndInternalValueSurvives(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 3, nil)
	f.expectExistingItem(db.WorkItem{Title: "Roof", Status: "Open", Notes: "internal notes"})
	f.q.EXPECT().UpsertWorkItem(gomock.Any(), db.UpsertWorkItemParams{ID: f.internalID, Title: "Roof", Status: "Done", Notes: "internal notes"}).Return(db.WorkItem{}, nil)
	f.q.EXPECT().InsertDomainEvent(gomock.Any(), gomock.Any()).Return(db.DomainEvent{}, nil)

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook,
		ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Done", Description: "overwritten in jira", Version: 3})

	if err != nil || got != OutcomeConflict {
		t.Errorf("Apply() = %q, %v, want conflict", got, err)
	}
}

func TestApply_NoContentChangeAdvancesVersionButPublishesNoEvent(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 4, nil)
	f.expectExistingItem(db.WorkItem{Title: "Roof", Status: "Open", Notes: "n"})
	f.q.EXPECT().UpsertWorkItem(gomock.Any(), gomock.Any()).Return(db.WorkItem{}, nil)

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook,
		ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Description: "n", Version: 4})

	if err != nil || got != OutcomeApplied {
		t.Errorf("Apply() = %q, %v, want applied (and, with no InsertDomainEvent expected, no event)", got, err)
	}
}

func TestApply_DeleteIsATombstoneNotARemoval(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 5, nil)
	f.expectExistingItem(db.WorkItem{Title: "Roof", Status: "Open", Notes: "n"})
	f.q.EXPECT().UpsertWorkItem(gomock.Any(), db.UpsertWorkItemParams{ID: f.internalID, Title: "Roof", Status: "Open", Notes: "n", Deleted: true}).Return(db.WorkItem{}, nil)
	f.expectEvent(t, EventWorkItemDeleted, WorkItemPayload{ID: f.internalID, Title: "Roof", Status: "Open", Notes: "n", Deleted: true})

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook,
		ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Description: "n", Version: 5, Deleted: true})

	if err != nil || got != OutcomeApplied {
		t.Errorf("Apply() = %q, %v, want applied", got, err)
	}
}

func TestApply_AlreadyTombstonedDeleteReplayPublishesNothing(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 6, nil)
	f.expectExistingItem(db.WorkItem{Title: "Roof", Status: "Open", Notes: "n", DeletedAt: pgtype.Timestamptz{Valid: true}})
	f.q.EXPECT().UpsertWorkItem(gomock.Any(), gomock.Any()).Return(db.WorkItem{}, nil)

	_, err := f.applier.Apply(context.Background(), f.q, SourceWebhook,
		ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Description: "n", Version: 6, Deleted: true})

	if err != nil {
		t.Errorf("Apply() error = %v", err)
	}
}

func TestApply_WebhookMatchingPushedMemoryIsEchoAndTouchesNothing(t *testing.T) {
	f := newApplyFixture(t)
	issue := ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Description: "pushed notes", Version: 7}
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), db.GetSyncPushedParams{System: testSystem, InternalID: f.internalID}).
		Return(db.SyncPushed{ExternalVersion: 7, ContentHash: ContentOf(issue).Hash()}, nil)

	got, err := f.applier.Apply(context.Background(), f.q, SourceWebhook, issue)

	if err != nil || got != OutcomeEcho {
		t.Errorf("Apply() = %q, %v, want echo", got, err)
	}
}

func TestApply_SameVersionButDifferentContentIsNotAnEcho(t *testing.T) {
	f := newApplyFixture(t)
	issue := ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Done", Description: "n", Version: 7}
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).
		Return(db.SyncPushed{ExternalVersion: 7, ContentHash: "hash-of-something-else"}, nil)
	f.expectVersionAdvance("MJ-1", 7, pgx.ErrNoRows)

	got, _ := f.applier.Apply(context.Background(), f.q, SourceWebhook, issue)

	if got != OutcomeIgnoredStale {
		t.Errorf("Apply() = %q, want it to fall through to the version gate", got)
	}
}

func TestApply_PollDoesNotConsultEchoMemory(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.expectVersionAdvance("MJ-1", 7, pgx.ErrNoRows)

	got, err := f.applier.Apply(context.Background(), f.q, SourcePoll, ExternalIssue{ID: "MJ-1", Version: 7})

	if err != nil || got != OutcomeIgnoredStale {
		t.Errorf("Apply() = %q, %v, want ignored_stale (no GetSyncPushed expected for a poll)", got, err)
	}
}

func TestResolveInternalID_ReturnsTheStoredIDNotTheMintedOne(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	minted, winner := uuid.New(), uuid.New()
	q.EXPECT().InsertExternalLink(gomock.Any(), db.InsertExternalLinkParams{System: testSystem, ExternalID: "MJ-1", InternalID: minted}).Return(nil)
	q.EXPECT().GetExternalLink(gomock.Any(), db.GetExternalLinkParams{System: testSystem, ExternalID: "MJ-1"}).Return(db.ExternalIDMap{InternalID: winner}, nil)

	got, err := ResolveInternalID(context.Background(), q, testSystem, "MJ-1", func() uuid.UUID { return minted })

	if err != nil || got != winner {
		t.Errorf("ResolveInternalID() = %v, %v, want the concurrent winner %v", got, err, winner)
	}
}

func TestProcessWebhook(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"not json", `not json`, true},
		{"no issue id", `{"event":"issue.updated","issue":{"version":2}}`, true},
		{"no version", `{"event":"issue.updated","issue":{"id":"MJ-1"}}`, true},
		{"version zero", `{"event":"issue.updated","issue":{"id":"MJ-1","version":0}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newApplyFixture(t)

			_, err := f.applier.ProcessWebhook(context.Background(), f.q, []byte(tt.body))

			if (err != nil) != tt.wantErr {
				t.Errorf("ProcessWebhook() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProcessWebhook_ReturnsOutcomeString(t *testing.T) {
	f := newApplyFixture(t)
	f.expectLink("MJ-1")
	f.q.EXPECT().GetSyncPushed(gomock.Any(), gomock.Any()).Return(db.SyncPushed{}, pgx.ErrNoRows)
	f.expectVersionAdvance("MJ-1", 2, pgx.ErrNoRows)

	outcome, err := f.applier.ProcessWebhook(context.Background(), f.q, []byte(`{"event":"issue.updated","issue":{"id":"MJ-1","version":2}}`))

	if err != nil || outcome != "ignored_stale" {
		t.Errorf("ProcessWebhook() = %q, %v, want ignored_stale", outcome, err)
	}
}
