package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

const actor = "cognito-sub-123"

func newTestService(t *testing.T) (*Service, *mocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	return NewService(q), q
}

func TestSync_Location_Applied(t *testing.T) {
	svc, q := newTestService(t)
	in := LocationInput{ID: uuid.New(), Name: "Loc A", Latitude: 1, Longitude: 2, UpdatedAt: time.Now()}

	q.EXPECT().UpsertLocation(gomock.Any(), gomock.Any()).Return(db.DisasterLocation{}, nil)

	resp, err := svc.Sync(context.Background(), actor, Request{Locations: []LocationInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Locations[0].Outcome; got != OutcomeApplied {
		t.Errorf("Outcome = %q, want %q", got, OutcomeApplied)
	}
}

func TestSync_Location_IgnoredStale(t *testing.T) {
	svc, q := newTestService(t)
	id := uuid.New()
	newer := time.Now()
	older := newer.Add(-time.Hour)
	in := LocationInput{ID: id, Name: "Loc A", UpdatedAt: older}

	q.EXPECT().UpsertLocation(gomock.Any(), gomock.Any()).Return(db.DisasterLocation{}, pgx.ErrNoRows)
	q.EXPECT().GetLocation(gomock.Any(), id).Return(db.DisasterLocation{
		Name: "Loc A (server copy)", UpdatedAt: pgtype.Timestamptz{Time: newer, Valid: true},
	}, nil)

	resp, err := svc.Sync(context.Background(), actor, Request{Locations: []LocationInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Locations[0].Outcome; got != OutcomeIgnoredStale {
		t.Errorf("Outcome = %q, want %q", got, OutcomeIgnoredStale)
	}
}

func TestSync_Location_Conflict(t *testing.T) {
	svc, q := newTestService(t)
	id := uuid.New()
	ts := time.Now()
	in := LocationInput{ID: id, Name: "Loc A (client edit)", Latitude: 1, Longitude: 1, UpdatedAt: ts}

	q.EXPECT().UpsertLocation(gomock.Any(), gomock.Any()).Return(db.DisasterLocation{}, pgx.ErrNoRows)
	q.EXPECT().GetLocation(gomock.Any(), id).Return(db.DisasterLocation{
		Name: "Loc A (someone else's edit)", Latitude: 1, Longitude: 1,
		UpdatedAt: pgtype.Timestamptz{Time: ts, Valid: true},
	}, nil)

	resp, err := svc.Sync(context.Background(), actor, Request{Locations: []LocationInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Locations[0].Outcome; got != OutcomeConflict {
		t.Errorf("Outcome = %q, want %q (same timestamp, different content)", got, OutcomeConflict)
	}
}

// TestSync_Location_IdempotentReplay covers the outbox-retry case: the exact
// same record, same timestamp, same content, applied twice because the first
// ack never reached the client. This must NOT be reported as a Conflict.
func TestSync_Location_IdempotentReplay(t *testing.T) {
	svc, q := newTestService(t)
	id := uuid.New()
	ts := time.Now()
	in := LocationInput{ID: id, Name: "Loc A", Latitude: 1, Longitude: 2, UpdatedAt: ts}

	q.EXPECT().UpsertLocation(gomock.Any(), gomock.Any()).Return(db.DisasterLocation{}, pgx.ErrNoRows)
	q.EXPECT().GetLocation(gomock.Any(), id).Return(db.DisasterLocation{
		Name: "Loc A", Latitude: 1, Longitude: 2,
		UpdatedAt: pgtype.Timestamptz{Time: ts, Valid: true},
	}, nil)

	resp, err := svc.Sync(context.Background(), actor, Request{Locations: []LocationInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Locations[0].Outcome; got != OutcomeApplied {
		t.Errorf("Outcome = %q, want %q (identical replay is idempotent, not a conflict)", got, OutcomeApplied)
	}
}

func TestSync_Photo_Applied(t *testing.T) {
	svc, q := newTestService(t)
	in := PhotoInput{ID: uuid.New(), SiteAssessmentID: uuid.New(), Condition: "good", UpdatedAt: time.Now()}

	q.EXPECT().UpsertPhoto(gomock.Any(), gomock.Any()).Return(db.Photo{}, nil)

	resp, err := svc.Sync(context.Background(), actor, Request{Photos: []PhotoInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Photos[0].Outcome; got != OutcomeApplied {
		t.Errorf("Outcome = %q, want %q", got, OutcomeApplied)
	}
}

// TestSync_Photo_LimitExceeded covers the <=10-photos-per-site rule: the
// upsert is blocked and the photo id has never existed, which is how the
// service distinguishes "rejected by the cap" from "rejected as stale".
func TestSync_Photo_LimitExceeded(t *testing.T) {
	svc, q := newTestService(t)
	id := uuid.New()
	in := PhotoInput{ID: id, SiteAssessmentID: uuid.New(), Condition: "bad", UpdatedAt: time.Now()}

	q.EXPECT().UpsertPhoto(gomock.Any(), gomock.Any()).Return(db.Photo{}, pgx.ErrNoRows)
	q.EXPECT().GetPhoto(gomock.Any(), id).Return(db.Photo{}, pgx.ErrNoRows)

	resp, err := svc.Sync(context.Background(), actor, Request{Photos: []PhotoInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Photos[0].Outcome; got != OutcomeLimitExceeded {
		t.Errorf("Outcome = %q, want %q", got, OutcomeLimitExceeded)
	}
}

func TestSync_Photo_IgnoredStale(t *testing.T) {
	svc, q := newTestService(t)
	id := uuid.New()
	newer := time.Now()
	older := newer.Add(-time.Hour)
	in := PhotoInput{ID: id, SiteAssessmentID: uuid.New(), Condition: "good", UpdatedAt: older}

	q.EXPECT().UpsertPhoto(gomock.Any(), gomock.Any()).Return(db.Photo{}, pgx.ErrNoRows)
	q.EXPECT().GetPhoto(gomock.Any(), id).Return(db.Photo{
		Condition: "moderate", UpdatedAt: pgtype.Timestamptz{Time: newer, Valid: true},
	}, nil)

	resp, err := svc.Sync(context.Background(), actor, Request{Photos: []PhotoInput{in}})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := resp.Photos[0].Outcome; got != OutcomeIgnoredStale {
		t.Errorf("Outcome = %q, want %q", got, OutcomeIgnoredStale)
	}
}

func TestSync_InfraErrorAborts(t *testing.T) {
	svc, q := newTestService(t)
	in := LocationInput{ID: uuid.New(), UpdatedAt: time.Now()}
	boom := errors.New("connection reset")

	q.EXPECT().UpsertLocation(gomock.Any(), gomock.Any()).Return(db.DisasterLocation{}, boom)

	_, err := svc.Sync(context.Background(), actor, Request{Locations: []LocationInput{in}})
	if !errors.Is(err, boom) {
		t.Fatalf("Sync() error = %v, want wrapped %v", err, boom)
	}
}
