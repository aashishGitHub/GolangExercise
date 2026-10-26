package twoway

import (
	"context"
	"fmt"

	"fieldsync/internal/db"
	fsync "fieldsync/internal/sync"
)

// Transactor runs fn inside one transaction. Components depend on it rather
// than on pgx so their unit tests can run against a mocked Querier.
type Transactor interface {
	InTx(ctx context.Context, fn func(q db.Querier) error) error
}

type PgxTransactor struct {
	beginner fsync.Beginner
}

func NewPgxTransactor(beginner fsync.Beginner) *PgxTransactor {
	return &PgxTransactor{beginner: beginner}
}

func (t *PgxTransactor) InTx(ctx context.Context, fn func(q db.Querier) error) error {
	tx, err := t.beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := fn(db.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
