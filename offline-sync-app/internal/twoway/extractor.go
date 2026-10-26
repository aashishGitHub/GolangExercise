package twoway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"fieldsync/internal/db"
)

type Lister interface {
	List(ctx context.Context, since time.Time, cursor string, limit int) (Page, error)
}

type IssueApplier interface {
	Apply(ctx context.Context, q db.Querier, src Source, issue ExternalIssue) (Outcome, error)
}

type ExtractorConfig struct {
	Budget   time.Duration
	Overlap  time.Duration
	PageSize int
}

type Result struct {
	Pages  int
	Issues int
	Done   bool
}

// Extractor is the forward incremental pull. Each page and its checkpoint share
// one transaction, so a crash replays at most one page, and replay is a no-op.
type Extractor struct {
	system  string
	lister  Lister
	q       db.Querier
	tx      Transactor
	applier IssueApplier
	clock   func() time.Time
	cfg     ExtractorConfig
}

func NewExtractor(system string, lister Lister, q db.Querier, tx Transactor, applier IssueApplier, clock func() time.Time, cfg ExtractorConfig) *Extractor {
	return &Extractor{system: system, lister: lister, q: q, tx: tx, applier: applier, clock: clock, cfg: cfg}
}

// Run checks the budget before each page, reserving the slowest page so far;
// running out is a clean exit and the next Run resumes from the stored cursor.
func (e *Extractor) Run(ctx context.Context) (Result, error) {
	deadline := e.clock().Add(e.cfg.Budget)

	state, err := e.q.GetSyncState(ctx, e.system)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("get sync state for %s: %w", e.system, err)
	}

	since, cursor, runMax := state.RunSince.Time, state.NextCursor, state.RunMax
	if cursor == "" {
		since = time.Time{}
		if state.Watermark.Valid {
			since = state.Watermark.Time.Add(-e.cfg.Overlap)
		}
		runMax = state.Watermark
	}

	var res Result
	var slowest time.Duration
	for {
		remaining := deadline.Sub(e.clock())
		if remaining <= 0 || remaining < slowest {
			return res, nil
		}
		started := e.clock()

		page, err := e.lister.List(ctx, since, cursor, e.cfg.PageSize)
		if err != nil {
			return res, fmt.Errorf("list page of %s: %w", e.system, err)
		}

		for _, issue := range page.Issues {
			if !runMax.Valid || issue.UpdatedAt.After(runMax.Time) {
				runMax = pgtype.Timestamptz{Time: issue.UpdatedAt, Valid: true}
			}
		}
		next := page.NextCursor

		err = e.tx.InTx(ctx, func(q db.Querier) error {
			for _, issue := range page.Issues {
				if _, err := e.applier.Apply(ctx, q, SourcePoll, issue); err != nil {
					return err
				}
			}
			return q.UpsertSyncState(ctx, e.checkpoint(state, since, next, runMax))
		})
		if err != nil {
			return res, fmt.Errorf("apply page of %s: %w", e.system, err)
		}

		res.Pages++
		res.Issues += len(page.Issues)
		if next == "" {
			res.Done = true
			return res, nil
		}
		cursor = next
		slowest = max(slowest, e.clock().Sub(started))
	}
}

func (e *Extractor) checkpoint(prior db.SyncState, since time.Time, next string, runMax pgtype.Timestamptz) db.UpsertSyncStateParams {
	if next == "" {
		return db.UpsertSyncStateParams{System: e.system, Watermark: runMax}
	}
	return db.UpsertSyncStateParams{
		System:     e.system,
		Watermark:  prior.Watermark,
		RunSince:   pgtype.Timestamptz{Time: since, Valid: true},
		NextCursor: next,
		RunMax:     runMax,
	}
}
