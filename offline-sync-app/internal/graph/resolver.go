package graph

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

//go:generate go tool gqlgen generate --config gqlgen.yml

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	codeBadInput    = "BAD_USER_INPUT"
)

type Resolver struct {
	Store Store
}

func pageSize(first *int) (int, error) {
	if first == nil {
		return DefaultPageSize, nil
	}
	if *first < 1 || *first > MaxPageSize {
		return 0, badInput(fmt.Sprintf("first must be between 1 and %d", MaxPageSize))
	}
	return *first, nil
}

func badInput(msg string) *gqlerror.Error {
	return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": codeBadInput}}
}

func loadLocation(ctx context.Context, id uuid.UUID) (*Location, error) {
	row, err := loadersFrom(ctx).Locations.Load(ctx, id)
	if err != nil || row == nil {
		return nil, err
	}
	return newLocation(*row), nil
}

func mapSlice[T, U any](in []T, f func(T) *U) []*U {
	out := make([]*U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}
