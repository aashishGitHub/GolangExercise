package graph

import (
	"context"
	"time"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

type Store interface {
	ListLocations(ctx context.Context, p ListParams) ([]db.DisasterLocation, error)
	LocationsByIDs(ctx context.Context, ids []uuid.UUID) ([]db.DisasterLocation, error)
	SiteAssessmentsByLocationIDs(ctx context.Context, ids []uuid.UUID) ([]db.SiteAssessment, error)
	PhotosBySiteAssessmentIDs(ctx context.Context, ids []uuid.UUID) ([]db.Photo, error)
	LocationStatsByLocationIDs(ctx context.Context, ids []uuid.UUID) ([]db.LocationStat, error)
}

type ListParams struct {
	Limit        int
	After        *Cursor
	UpdatedSince *time.Time
}

type Cursor struct {
	UpdatedAt time.Time
	ID        uuid.UUID
}
