package graph

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

type Location struct {
	ID        uuid.UUID
	Name      string
	Latitude  float64
	Longitude float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SiteAssessment struct {
	ID         uuid.UUID
	LocationID uuid.UUID
	Name       string
	Notes      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Photo struct {
	ID        uuid.UUID
	S3Key     string
	Latitude  float64
	Longitude float64
	Condition PhotoCondition
	CreatedAt time.Time
	UpdatedAt time.Time
}

type LocationStats struct {
	GoodCount     int
	ModerateCount int
	BadCount      int
	UpdatedAt     time.Time
}

func newLocation(l db.DisasterLocation) *Location {
	return &Location{
		ID:        l.ID,
		Name:      l.Name,
		Latitude:  l.Latitude,
		Longitude: l.Longitude,
		CreatedAt: l.CreatedAt.Time,
		UpdatedAt: l.UpdatedAt.Time,
	}
}

func newSiteAssessment(s db.SiteAssessment) *SiteAssessment {
	return &SiteAssessment{
		ID:         s.ID,
		LocationID: s.LocationID,
		Name:       s.Name,
		Notes:      s.Notes,
		CreatedAt:  s.CreatedAt.Time,
		UpdatedAt:  s.UpdatedAt.Time,
	}
}

func newPhoto(p db.Photo) *Photo {
	return &Photo{
		ID:        p.ID,
		S3Key:     p.S3Key,
		Latitude:  p.Latitude,
		Longitude: p.Longitude,
		Condition: PhotoCondition(strings.ToUpper(p.Condition)),
		CreatedAt: p.CreatedAt.Time,
		UpdatedAt: p.UpdatedAt.Time,
	}
}

func newLocationStats(s db.LocationStat) *LocationStats {
	return &LocationStats{
		GoodCount:     int(s.GoodCount),
		ModerateCount: int(s.ModerateCount),
		BadCount:      int(s.BadCount),
		UpdatedAt:     s.UpdatedAt.Time,
	}
}
