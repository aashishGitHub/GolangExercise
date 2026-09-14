package sync

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"fieldsync/internal/db"
	"fieldsync/internal/events"
)

// Beginner is satisfied by *pgxpool.Pool. Kept minimal and separate from
// Service's plain db.Querier dependency so Service's own unit tests stay
// exactly as they were — this wrapper is exercised by the integration suite
// instead, the same split Phase 2 used for the atomic-upsert SQL itself.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// TxRunner wraps Service so every batch runs in one transaction: the
// upserts, and — only for records that actually Applied — a domain_events
// row per record, via events.Publish. A record that was ignored-stale,
// conflicted, or hit the ≤10-photos cap has nothing to tell anyone; only a
// real state change is worth an event. Rolling back the whole batch on any
// infra error is safe specifically because every upsert is idempotent — the
// client's outbox retries the identical batch, no partial-application
// bookkeeping needed.
type TxRunner struct {
	beginner Beginner
}

func NewTxRunner(beginner Beginner) *TxRunner {
	return &TxRunner{beginner: beginner}
}

func (r *TxRunner) Sync(ctx context.Context, actor string, req Request) (Response, error) {
	tx, err := r.beginner.Begin(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	q := db.New(tx)
	resp, err := NewService(q).Sync(ctx, actor, req)
	if err != nil {
		return Response{}, err
	}

	for i, result := range resp.Locations {
		if result.Outcome != OutcomeApplied {
			continue
		}
		in := req.Locations[i]
		if err := events.Publish(ctx, q, events.Event{
			AggregateType: "location",
			AggregateID:   in.ID,
			Type:          "location.upserted",
			ActorSub:      actor,
			Payload:       in,
		}); err != nil {
			return Response{}, err
		}
	}

	for i, result := range resp.SiteAssessments {
		if result.Outcome != OutcomeApplied {
			continue
		}
		in := req.SiteAssessments[i]
		if err := events.Publish(ctx, q, events.Event{
			AggregateType: "site_assessment",
			AggregateID:   in.ID,
			Type:          "site_assessment.upserted",
			ActorSub:      actor,
			Payload:       in,
		}); err != nil {
			return Response{}, err
		}
	}

	for i, result := range resp.Photos {
		if result.Outcome != OutcomeApplied {
			continue
		}
		in := req.Photos[i]
		if err := events.Publish(ctx, q, events.Event{
			AggregateType: "photo",
			AggregateID:   in.ID,
			Type:          "photo.upserted",
			ActorSub:      actor,
			Payload:       in,
		}); err != nil {
			return Response{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Response{}, fmt.Errorf("commit tx: %w", err)
	}
	return resp, nil
}
