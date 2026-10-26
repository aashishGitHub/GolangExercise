package webhook

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"fieldsync/internal/db"
)

// Processor applies one stored delivery using the transaction's Querier; its
// writes commit together with the inbox row's processed mark, or not at all.
type Processor func(ctx context.Context, q db.Querier, body []byte) (outcome string, err error)

// Pool is satisfied by *pgxpool.Pool.
type Pool interface {
	db.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

const batchSize = 25

type Worker struct {
	system      string
	pool        Pool
	process     Processor
	maxAttempts int32
}

func NewWorker(system string, pool Pool, process Processor, maxAttempts int) *Worker {
	return &Worker{system: system, pool: pool, process: process, maxAttempts: int32(maxAttempts)}
}

// RunOnce handles up to one batch of pending deliveries and reports how many
// were processed. A failing delivery does not block the rest of the batch: its
// attempt count goes up, and at maxAttempts it moves to sync_dead_letters.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	ids, err := db.New(w.pool).ListPendingInbox(ctx, db.ListPendingInboxParams{System: w.system, Limit: batchSize})
	if err != nil {
		return 0, fmt.Errorf("list pending inbox for %s: %w", w.system, err)
	}

	processed := 0
	for _, id := range ids {
		done, err := w.processOne(ctx, id)
		if err != nil {
			log.Printf("webhook-worker %s: delivery %s failed: %v", w.system, id, err)
			if ferr := w.recordFailure(ctx, id, err); ferr != nil {
				return processed, ferr
			}
			continue
		}
		if done {
			processed++
		}
	}
	return processed, nil
}

func (w *Worker) processOne(ctx context.Context, deliveryID string) (bool, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	row, err := q.ClaimInboxRow(ctx, db.ClaimInboxRowParams{System: w.system, DeliveryID: deliveryID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim inbox row: %w", err)
	}

	outcome, err := w.process(ctx, q, row.Body)
	if err != nil {
		return false, err
	}
	if err := q.MarkInboxProcessed(ctx, db.MarkInboxProcessedParams{System: w.system, DeliveryID: deliveryID, Outcome: outcome}); err != nil {
		return false, fmt.Errorf("mark inbox processed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit tx: %w", err)
	}
	return true, nil
}

func (w *Worker) recordFailure(ctx context.Context, deliveryID string, cause error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	row, err := q.RecordInboxFailure(ctx, db.RecordInboxFailureParams{System: w.system, DeliveryID: deliveryID, LastError: cause.Error()})
	if err != nil {
		return fmt.Errorf("record inbox failure: %w", err)
	}
	if row.Attempts >= w.maxAttempts {
		if err := q.InsertDeadLetter(ctx, db.InsertDeadLetterParams{
			Source:   "inbox",
			System:   w.system,
			RefID:    deliveryID,
			Payload:  row.Body,
			Reason:   cause.Error(),
			Attempts: row.Attempts,
		}); err != nil {
			return fmt.Errorf("insert dead letter: %w", err)
		}
		if err := q.MarkInboxDead(ctx, db.MarkInboxDeadParams{System: w.system, DeliveryID: deliveryID}); err != nil {
			return fmt.Errorf("mark inbox dead: %w", err)
		}
		log.Printf("webhook-worker %s: delivery %s dead-lettered after %d attempts: %v", w.system, deliveryID, row.Attempts, cause)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
