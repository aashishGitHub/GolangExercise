//go:build integration

// Run with: DATABASE_URL=postgres://fieldsync:fieldsync@localhost:5432/fieldsync?sslmode=disable go test -tags=integration ./internal/sync/...
// Requires `make compose-up` + migrations applied. Exercises the real SQL in
// sqlc/queries.sql end to end — sync_test.go covers the Go control flow
// against a mocked Querier; this covers the atomic-upsert behavior itself.
package sync

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/db"
)

func newIntegrationService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://fieldsync:fieldsync@localhost:5432/fieldsync?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewService(db.New(pool)), pool
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func TestIntegration_FullSyncFlow(t *testing.T) {
	svc, pool := newIntegrationService(t)
	ctx := context.Background()

	locationID := uuid.New()
	siteID := uuid.New()
	t.Cleanup(func() {
		mustExec(t, pool, "DELETE FROM photos WHERE site_assessment_id = $1", siteID)
		mustExec(t, pool, "DELETE FROM site_assessments WHERE id = $1", siteID)
		mustExec(t, pool, "DELETE FROM disaster_locations WHERE id = $1", locationID)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)

	// New location + site: both should apply.
	resp, err := svc.Sync(ctx, "tester", Request{
		Locations: []LocationInput{{ID: locationID, Name: "Loc A", Latitude: 1, Longitude: 2, CreatedAt: now, UpdatedAt: now}},
		SiteAssessments: []SiteAssessmentInput{
			{ID: siteID, LocationID: locationID, Name: "Site A", CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if resp.Locations[0].Outcome != OutcomeApplied || resp.SiteAssessments[0].Outcome != OutcomeApplied {
		t.Fatalf("initial creation: got %+v / %+v, want applied/applied", resp.Locations[0], resp.SiteAssessments[0])
	}

	// Stale re-send of the location (older updated_at) must be ignored.
	resp, err = svc.Sync(ctx, "tester", Request{
		Locations: []LocationInput{{ID: locationID, Name: "Loc A STALE", Latitude: 9, Longitude: 9, CreatedAt: now, UpdatedAt: now.Add(-time.Hour)}},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if resp.Locations[0].Outcome != OutcomeIgnoredStale {
		t.Fatalf("stale resend: got %q, want %q", resp.Locations[0].Outcome, OutcomeIgnoredStale)
	}

	// Fill the site to the 10-photo cap.
	for i := 0; i < 10; i++ {
		resp, err := svc.Sync(ctx, "tester", Request{
			Photos: []PhotoInput{{
				ID: uuid.New(), SiteAssessmentID: siteID, S3Key: "k", Condition: "good",
				CreatedAt: now, UpdatedAt: now,
			}},
		})
		if err != nil {
			t.Fatalf("Sync() photo %d error = %v", i, err)
		}
		if resp.Photos[0].Outcome != OutcomeApplied {
			t.Fatalf("photo %d: got %q, want applied", i, resp.Photos[0].Outcome)
		}
	}

	// The 11th NEW photo must be rejected by the cap.
	resp, err = svc.Sync(ctx, "tester", Request{
		Photos: []PhotoInput{{ID: uuid.New(), SiteAssessmentID: siteID, S3Key: "k11", Condition: "bad", CreatedAt: now, UpdatedAt: now}},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if resp.Photos[0].Outcome != OutcomeLimitExceeded {
		t.Fatalf("11th photo: got %q, want %q", resp.Photos[0].Outcome, OutcomeLimitExceeded)
	}

	count, err := db.New(pool).CountPhotosBySite(ctx, siteID)
	if err != nil {
		t.Fatalf("CountPhotosBySite: %v", err)
	}
	if count != 10 {
		t.Fatalf("photo count = %d, want 10 (cap must hold)", count)
	}
}

// TestIntegration_TxRunnerEventsOnlyOnApplied covers the transactional-outbox
// half: a domain_events row must exist for an Applied write, and must NOT
// exist for an ignored-stale one — events describe state changes, not
// rejected writes. Also proves the whole batch (upsert + event) commits
// together, not as two separate round trips that could observably diverge.
func TestIntegration_TxRunnerEventsOnlyOnApplied(t *testing.T) {
	_, pool := newIntegrationService(t)
	ctx := context.Background()
	runner := NewTxRunner(pool)

	locationID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	t.Cleanup(func() {
		mustExec(t, pool, "DELETE FROM domain_events WHERE aggregate_id = $1", locationID)
		mustExec(t, pool, "DELETE FROM disaster_locations WHERE id = $1", locationID)
	})

	resp, err := runner.Sync(ctx, "tester", Request{
		Locations: []LocationInput{{ID: locationID, Name: "Loc A", Latitude: 1, Longitude: 2, CreatedAt: now, UpdatedAt: now}},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if resp.Locations[0].Outcome != OutcomeApplied {
		t.Fatalf("got %q, want applied", resp.Locations[0].Outcome)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM domain_events WHERE aggregate_id = $1 AND event_type = 'location.upserted'",
		locationID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query domain_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("domain_events count after applied write = %d, want 1", eventCount)
	}

	// Stale resend: must NOT add a second event.
	resp, err = runner.Sync(ctx, "tester", Request{
		Locations: []LocationInput{{ID: locationID, Name: "STALE", Latitude: 9, Longitude: 9, CreatedAt: now, UpdatedAt: now.Add(-time.Hour)}},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if resp.Locations[0].Outcome != OutcomeIgnoredStale {
		t.Fatalf("got %q, want ignored_stale", resp.Locations[0].Outcome)
	}

	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM domain_events WHERE aggregate_id = $1", locationID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query domain_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("domain_events count after ignored-stale write = %d, want still 1 (no event for a rejected write)", eventCount)
	}
}
