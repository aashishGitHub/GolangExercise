package twoway

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fieldsync/internal/db"
	"fieldsync/internal/events"
)

const (
	EventWorkItemUpdated = "work_item.updated"
	EventWorkItemDeleted = "work_item.deleted"
)

// OriginActor is the actor_sub stamped on events caused by the external
// system, so its own Loader can skip them.
func OriginActor(system string) string {
	return "system:" + system
}

type WorkItemPayload struct {
	ID      uuid.UUID `json:"id"`
	Title   string    `json:"title"`
	Status  string    `json:"status"`
	Notes   string    `json:"notes"`
	Deleted bool      `json:"deleted"`
}

type Source int

const (
	SourceWebhook Source = iota
	SourcePoll
)

type Applier struct {
	system    string
	ownership Ownership
	newID     func() uuid.UUID
}

func NewApplier(system string, ownership Ownership) *Applier {
	return &Applier{system: system, ownership: ownership, newID: uuid.New}
}

// Apply needs a transaction-scoped q: version gate, row lock and event insert
// must commit together. Echo detection is webhook-only so a poll can repair
// what a dropped webhook left behind.
func (a *Applier) Apply(ctx context.Context, q db.Querier, src Source, issue ExternalIssue) (Outcome, error) {
	internalID, err := ResolveInternalID(ctx, q, a.system, issue.ID, a.newID)
	if err != nil {
		return "", err
	}

	if src == SourceWebhook {
		echo, err := a.isEcho(ctx, q, internalID, issue)
		if err != nil {
			return "", err
		}
		if echo {
			return OutcomeEcho, nil
		}
	}

	_, err = q.AdvanceLinkVersion(ctx, db.AdvanceLinkVersionParams{System: a.system, ExternalID: issue.ID, Version: issue.Version})
	if errors.Is(err, pgx.ErrNoRows) {
		return OutcomeIgnoredStale, nil
	}
	if err != nil {
		return "", fmt.Errorf("advance version of %s/%s: %w", a.system, issue.ID, err)
	}

	current, err := q.GetWorkItemForUpdate(ctx, internalID)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("lock work item %s: %w", internalID, err)
	}

	merged, rejected := ContentOf(issue), []Field(nil)
	if exists {
		merged, rejected = a.ownership.Merge(contentOfRow(current), ContentOf(issue))
	}

	if _, err := q.UpsertWorkItem(ctx, db.UpsertWorkItemParams{
		ID: internalID, Title: merged.Title, Status: merged.Status, Notes: merged.Notes, Deleted: issue.Deleted,
	}); err != nil {
		return "", fmt.Errorf("upsert work item %s: %w", internalID, err)
	}

	changed := !exists || merged != contentOfRow(current) || issue.Deleted != current.DeletedAt.Valid
	if changed {
		if err := a.publish(ctx, q, internalID, merged, issue.Deleted); err != nil {
			return "", err
		}
	}

	if len(rejected) > 0 {
		return OutcomeConflict, nil
	}
	return OutcomeApplied, nil
}

func (a *Applier) isEcho(ctx context.Context, q db.Querier, internalID uuid.UUID, issue ExternalIssue) (bool, error) {
	pushed, err := q.GetSyncPushed(ctx, db.GetSyncPushedParams{System: a.system, InternalID: internalID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get pushed memory for %s: %w", internalID, err)
	}
	return pushed.ExternalVersion == issue.Version && pushed.ContentHash == ContentOf(issue).Hash(), nil
}

func (a *Applier) publish(ctx context.Context, q db.Querier, id uuid.UUID, c Content, deleted bool) error {
	eventType := EventWorkItemUpdated
	if deleted {
		eventType = EventWorkItemDeleted
	}
	return events.Publish(ctx, q, events.Event{
		AggregateType: "work_item",
		AggregateID:   id,
		Type:          eventType,
		ActorSub:      OriginActor(a.system),
		Payload:       WorkItemPayload{ID: id, Title: c.Title, Status: c.Status, Notes: c.Notes, Deleted: deleted},
	})
}

func contentOfRow(item db.WorkItem) Content {
	return Content{Title: item.Title, Status: item.Status, Notes: item.Notes}
}

// ResolveInternalID returns the internal id linked to (system, externalID),
// minting and storing one on first sight. Insert-then-select is race-safe
// under READ COMMITTED: a concurrent insert of the same key blocks until the
// winner commits, then both read the winner's id.
func ResolveInternalID(ctx context.Context, q db.Querier, system, externalID string, newID func() uuid.UUID) (uuid.UUID, error) {
	if err := q.InsertExternalLink(ctx, db.InsertExternalLinkParams{System: system, ExternalID: externalID, InternalID: newID()}); err != nil {
		return uuid.Nil, fmt.Errorf("insert link %s/%s: %w", system, externalID, err)
	}
	link, err := q.GetExternalLink(ctx, db.GetExternalLinkParams{System: system, ExternalID: externalID})
	if err != nil {
		return uuid.Nil, fmt.Errorf("get link %s/%s: %w", system, externalID, err)
	}
	return link.InternalID, nil
}
