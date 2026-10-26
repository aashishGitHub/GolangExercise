package twoway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"fieldsync/internal/db"
)

type Pusher interface {
	Patch(ctx context.Context, externalID string, p IssuePatch, idempotencyKey string) (ExternalIssue, error)
}

// Loader is the reverse (internal -> external) path. Handle is a
// consume.Handler: the domain event id is the Idempotency-Key, so the retry of
// a crashed push replays the original answer instead of writing twice.
type Loader struct {
	system    string
	q         db.Querier
	pusher    Pusher
	ownership Ownership
}

func NewLoader(system string, q db.Querier, pusher Pusher, ownership Ownership) *Loader {
	return &Loader{system: system, q: q, pusher: pusher, ownership: ownership}
}

func (l *Loader) ConsumerName() string { return "twoway-loader-" + l.system }

func (l *Loader) EventTypes() []string { return []string{EventWorkItemUpdated} }

func (l *Loader) Handle(ctx context.Context, evt db.DomainEvent) error {
	if evt.ActorSub == OriginActor(l.system) {
		return nil
	}

	var payload WorkItemPayload
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return l.deadLetter(ctx, evt, 1, fmt.Errorf("decode event payload: %w", err))
	}

	link, err := l.q.GetExternalLinkByInternal(ctx, db.GetExternalLinkByInternalParams{System: l.system, InternalID: evt.AggregateID})
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("twoway-loader %s: work item %s has no external link yet, skipping event %s", l.system, evt.AggregateID, evt.ID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get external link for %s: %w", evt.AggregateID, err)
	}

	patch := l.ownership.PatchFor(Content{Title: payload.Title, Status: payload.Status, Notes: payload.Notes})
	if patch.empty() {
		return nil
	}

	issue, err := l.pusher.Patch(ctx, link.ExternalID, patch, evt.ID.String())
	if err != nil {
		return l.onPushError(ctx, evt, err)
	}

	if err := l.q.UpsertSyncPushed(ctx, db.UpsertSyncPushedParams{
		System:          l.system,
		InternalID:      evt.AggregateID,
		ExternalVersion: issue.Version,
		ContentHash:     ContentOf(issue).Hash(),
	}); err != nil {
		return fmt.Errorf("remember pushed version of %s: %w", evt.AggregateID, err)
	}
	return nil
}

// onPushError dead-letters failures that retrying cannot fix (exhausted
// retries, a permanent 4xx) and returns anything else, e.g. a cancelled
// context, so the event stays unprocessed for the next tick.
func (l *Loader) onPushError(ctx context.Context, evt db.DomainEvent, err error) error {
	var exhausted *RetryExhaustedError
	var api *APIError
	switch {
	case errors.As(err, &exhausted):
		return l.deadLetter(ctx, evt, exhausted.Attempts, err)
	case errors.As(err, &api) && !api.retryable():
		return l.deadLetter(ctx, evt, 1, err)
	default:
		return fmt.Errorf("push event %s: %w", evt.ID, err)
	}
}

func (l *Loader) deadLetter(ctx context.Context, evt db.DomainEvent, attempts int, cause error) error {
	if err := l.q.InsertDeadLetter(ctx, db.InsertDeadLetterParams{
		Source:   "loader",
		System:   l.system,
		RefID:    evt.ID.String(),
		Payload:  evt.Payload,
		Reason:   cause.Error(),
		Attempts: int32(attempts),
	}); err != nil {
		return fmt.Errorf("dead-letter event %s: %w", evt.ID, err)
	}
	log.Printf("twoway-loader %s: event %s dead-lettered after %d attempt(s): %v", l.system, evt.ID, attempts, cause)
	return nil
}
