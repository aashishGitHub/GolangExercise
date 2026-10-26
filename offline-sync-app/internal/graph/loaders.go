package graph

import (
	"context"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

type Loaders struct {
	Locations       Loader[uuid.UUID, *db.DisasterLocation]
	SiteAssessments Loader[uuid.UUID, []db.SiteAssessment]
	Photos          Loader[uuid.UUID, []db.Photo]
	Stats           Loader[uuid.UUID, *db.LocationStat]
}

func NewLoaders(s Store, cfg LoaderConfig) *Loaders {
	return &Loaders{
		Locations:       NewDataLoader(locationsBatch(s), cfg),
		SiteAssessments: NewDataLoader(siteAssessmentsBatch(s), cfg),
		Photos:          NewDataLoader(photosBatch(s), cfg),
		Stats:           NewDataLoader(statsBatch(s), cfg),
	}
}

func locationsBatch(s Store) BatchFunc[uuid.UUID, *db.DisasterLocation] {
	return func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*db.DisasterLocation, error) {
		rows, err := s.LocationsByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		return indexBy(rows, func(l db.DisasterLocation) uuid.UUID { return l.ID }), nil
	}
}

func siteAssessmentsBatch(s Store) BatchFunc[uuid.UUID, []db.SiteAssessment] {
	return func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]db.SiteAssessment, error) {
		rows, err := s.SiteAssessmentsByLocationIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		return groupBy(rows, func(a db.SiteAssessment) uuid.UUID { return a.LocationID }), nil
	}
}

func photosBatch(s Store) BatchFunc[uuid.UUID, []db.Photo] {
	return func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]db.Photo, error) {
		rows, err := s.PhotosBySiteAssessmentIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		return groupBy(rows, func(p db.Photo) uuid.UUID { return p.SiteAssessmentID }), nil
	}
}

func statsBatch(s Store) BatchFunc[uuid.UUID, *db.LocationStat] {
	return func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*db.LocationStat, error) {
		rows, err := s.LocationStatsByLocationIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		return indexBy(rows, func(st db.LocationStat) uuid.UUID { return st.LocationID }), nil
	}
}

func indexBy[K comparable, T any](rows []T, key func(T) K) map[K]*T {
	out := make(map[K]*T, len(rows))
	for i := range rows {
		out[key(rows[i])] = &rows[i]
	}
	return out
}

func groupBy[K comparable, T any](rows []T, key func(T) K) map[K][]T {
	out := make(map[K][]T)
	for _, r := range rows {
		k := key(r)
		out[k] = append(out[k], r)
	}
	return out
}

type loadersKey struct{}

func WithLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, loadersKey{}, l)
}

// Panics when absent: that is a wiring bug (handler mounted without WithLoaders), not a request error.
func loadersFrom(ctx context.Context) *Loaders {
	l, ok := ctx.Value(loadersKey{}).(*Loaders)
	if !ok {
		panic("graph: loaders missing from request context")
	}
	return l
}
