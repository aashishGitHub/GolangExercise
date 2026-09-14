// Package sync implements the server side of the offline-first sync
// protocol: idempotent GUID upsert, Last-Write-Wins on updated_at, and the
// <=10-photos-per-site rule. Every entity is applied through a single atomic
// SQL statement (see sqlc/queries.sql) so the outcome classification below
// never races against a concurrent sync touching the same row.
package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"fieldsync/internal/db"
)

type Outcome string

const (
	OutcomeApplied       Outcome = "applied"
	OutcomeIgnoredStale  Outcome = "ignored_stale"
	OutcomeConflict      Outcome = "conflict"
	OutcomeLimitExceeded Outcome = "limit_exceeded" // photos only, ≤10 rule
)

type RecordResult struct {
	ID      uuid.UUID `json:"id"`
	Outcome Outcome   `json:"outcome"`
}

type LocationInput struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SiteAssessmentInput struct {
	ID         uuid.UUID `json:"id"`
	LocationID uuid.UUID `json:"locationId"`
	Name       string    `json:"name"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type PhotoInput struct {
	ID               uuid.UUID `json:"id"`
	SiteAssessmentID uuid.UUID `json:"siteAssessmentId"`
	S3Key            string    `json:"s3Key"`
	Latitude         float64   `json:"latitude"`
	Longitude        float64   `json:"longitude"`
	Condition        string    `json:"condition"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type Request struct {
	Locations       []LocationInput       `json:"locations"`
	SiteAssessments []SiteAssessmentInput `json:"siteAssessments"`
	Photos          []PhotoInput          `json:"photos"`
}

type Response struct {
	Locations       []RecordResult `json:"locations"`
	SiteAssessments []RecordResult `json:"siteAssessments"`
	Photos          []RecordResult `json:"photos"`
}

// Syncer is satisfied by both Service (plain, unit-tested against a mocked
// Querier) and TxRunner (transactional, wraps Service + event publishing) —
// httpapi's handlers depend on this, not a concrete type, so swapping which
// one they get doesn't touch handler code.
type Syncer interface {
	Sync(ctx context.Context, actor string, req Request) (Response, error)
}

type Service struct {
	q db.Querier
}

func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// Sync applies every record in the batch and classifies the outcome of each.
// actor is the Cognito `sub` of the authenticated caller — stamped as
// created_by on first insert only; the atomic upserts never touch
// created_by/created_at on an existing row, so attribution can't be
// overwritten by a later editor. A non-nil error means an infrastructure
// failure (DB unreachable, etc), not a business-level rejection — those are
// always represented as an Outcome, never an error.
func (s *Service) Sync(ctx context.Context, actor string, req Request) (Response, error) {
	var resp Response

	for _, in := range req.Locations {
		r, err := s.applyLocation(ctx, actor, in)
		if err != nil {
			return resp, err
		}
		resp.Locations = append(resp.Locations, r)
	}
	for _, in := range req.SiteAssessments {
		r, err := s.applySiteAssessment(ctx, actor, in)
		if err != nil {
			return resp, err
		}
		resp.SiteAssessments = append(resp.SiteAssessments, r)
	}
	for _, in := range req.Photos {
		r, err := s.applyPhoto(ctx, actor, in)
		if err != nil {
			return resp, err
		}
		resp.Photos = append(resp.Photos, r)
	}

	return resp, nil
}

func (s *Service) applyLocation(ctx context.Context, actor string, in LocationInput) (RecordResult, error) {
	_, err := s.q.UpsertLocation(ctx, db.UpsertLocationParams{
		ID:        in.ID,
		Name:      in.Name,
		Latitude:  in.Latitude,
		Longitude: in.Longitude,
		CreatedAt: toTimestamptz(in.CreatedAt),
		UpdatedAt: toTimestamptz(in.UpdatedAt),
		CreatedBy: actor,
	})
	if err == nil {
		return RecordResult{ID: in.ID, Outcome: OutcomeApplied}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RecordResult{}, fmt.Errorf("upsert location %s: %w", in.ID, err)
	}

	existing, err := s.q.GetLocation(ctx, in.ID)
	if err != nil {
		return RecordResult{}, fmt.Errorf("get location %s after blocked upsert: %w", in.ID, err)
	}
	contentEqual := in.Name == existing.Name && in.Latitude == existing.Latitude && in.Longitude == existing.Longitude
	return RecordResult{ID: in.ID, Outcome: classifyBlocked(in.UpdatedAt, existing.UpdatedAt, contentEqual)}, nil
}

func (s *Service) applySiteAssessment(ctx context.Context, actor string, in SiteAssessmentInput) (RecordResult, error) {
	_, err := s.q.UpsertSiteAssessment(ctx, db.UpsertSiteAssessmentParams{
		ID:         in.ID,
		LocationID: in.LocationID,
		Name:       in.Name,
		Notes:      in.Notes,
		CreatedAt:  toTimestamptz(in.CreatedAt),
		UpdatedAt:  toTimestamptz(in.UpdatedAt),
		CreatedBy:  actor,
	})
	if err == nil {
		return RecordResult{ID: in.ID, Outcome: OutcomeApplied}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RecordResult{}, fmt.Errorf("upsert site assessment %s: %w", in.ID, err)
	}

	existing, err := s.q.GetSiteAssessment(ctx, in.ID)
	if err != nil {
		return RecordResult{}, fmt.Errorf("get site assessment %s after blocked upsert: %w", in.ID, err)
	}
	contentEqual := in.Name == existing.Name && in.Notes == existing.Notes
	return RecordResult{ID: in.ID, Outcome: classifyBlocked(in.UpdatedAt, existing.UpdatedAt, contentEqual)}, nil
}

func (s *Service) applyPhoto(ctx context.Context, actor string, in PhotoInput) (RecordResult, error) {
	_, err := s.q.UpsertPhoto(ctx, db.UpsertPhotoParams{
		ID:               in.ID,
		SiteAssessmentID: in.SiteAssessmentID,
		S3Key:            in.S3Key,
		Latitude:         in.Latitude,
		Longitude:        in.Longitude,
		Condition:        in.Condition,
		CreatedAt:        toTimestamptz(in.CreatedAt),
		UpdatedAt:        toTimestamptz(in.UpdatedAt),
		CreatedBy:        actor,
	})
	if err == nil {
		return RecordResult{ID: in.ID, Outcome: OutcomeApplied}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RecordResult{}, fmt.Errorf("upsert photo %s: %w", in.ID, err)
	}

	existing, err := s.q.GetPhoto(ctx, in.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The row never existed, so the upsert wasn't blocked by LWW — it was
		// blocked by the site already being at the 10-photo cap.
		return RecordResult{ID: in.ID, Outcome: OutcomeLimitExceeded}, nil
	}
	if err != nil {
		return RecordResult{}, fmt.Errorf("get photo %s after blocked upsert: %w", in.ID, err)
	}
	contentEqual := in.S3Key == existing.S3Key && in.Latitude == existing.Latitude &&
		in.Longitude == existing.Longitude && in.Condition == existing.Condition
	return RecordResult{ID: in.ID, Outcome: classifyBlocked(in.UpdatedAt, existing.UpdatedAt, contentEqual)}, nil
}

// classifyBlocked explains why an upsert was blocked, given the incoming
// timestamp lost to (or tied with) the stored one. It's diagnostic only —
// the atomic upsert already made the data-correctness decision; this just
// labels it for the client.
//
// An equal timestamp with identical content is treated as Applied, not
// Conflict: it's the common case of a client retrying a sync batch whose ack
// never arrived (outbox + retry/backoff), replaying the exact same record —
// that's idempotency working as intended, not a real conflict. Equal
// timestamps with *different* content means two edits genuinely raced and
// can't be time-ordered, which is a real Conflict worth surfacing.
func classifyBlocked(incoming time.Time, stored pgtype.Timestamptz, contentEqual bool) Outcome {
	switch {
	case incoming.After(stored.Time):
		// The blocking upsert's WHERE clause should have already applied this;
		// reaching here means something else won a race after our read. Rare,
		// and harmless to report as applied since the data is in fact current.
		return OutcomeApplied
	case incoming.Equal(stored.Time):
		if contentEqual {
			return OutcomeApplied
		}
		return OutcomeConflict
	default:
		return OutcomeIgnoredStale
	}
}

func toTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
