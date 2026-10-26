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

var ErrWorkItemNotFound = errors.New("work item not found or deleted")

// WorkItems is the internal write path: an edit and its outbox event commit
// together, and the Loader later pushes that event to the external system.
type WorkItems struct {
	tx Transactor
}

func NewWorkItems(tx Transactor) *WorkItems {
	return &WorkItems{tx: tx}
}

func (w *WorkItems) EditNotes(ctx context.Context, id uuid.UUID, notes, actor string) error {
	return w.tx.InTx(ctx, func(q db.Querier) error {
		item, err := q.UpdateWorkItemNotes(ctx, db.UpdateWorkItemNotesParams{ID: id, Notes: notes})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWorkItemNotFound
		}
		if err != nil {
			return fmt.Errorf("update notes of work item %s: %w", id, err)
		}
		return events.Publish(ctx, q, events.Event{
			AggregateType: "work_item",
			AggregateID:   id,
			Type:          EventWorkItemUpdated,
			ActorSub:      actor,
			Payload:       WorkItemPayload{ID: id, Title: item.Title, Status: item.Status, Notes: item.Notes},
		})
	})
}
