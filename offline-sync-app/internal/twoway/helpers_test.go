package twoway

import (
	"context"

	"fieldsync/internal/db"
)

type passthroughTx struct {
	q db.Querier
}

func (p passthroughTx) InTx(_ context.Context, fn func(q db.Querier) error) error {
	return fn(p.q)
}

func strPtr(s string) *string { return &s }
