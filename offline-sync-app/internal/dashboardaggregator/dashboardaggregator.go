// Package dashboardaggregator reacts to "photo.upserted" events and
// recomputes location_stats — the server-side equivalent of the condition
// counts Phase 4's dashboard already computes client-side from Dexie. A
// future cross-device/reporting view can read this cache without re-scanning
// every photo.
package dashboardaggregator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

const ConsumerName = "dashboard-aggregator"

var EventTypes = []string{"photo.upserted"}

type photoPayload struct {
	SiteAssessmentID uuid.UUID `json:"siteAssessmentId"`
}

func Handle(ctx context.Context, q db.Querier, evt db.DomainEvent) error {
	var payload photoPayload
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal photo payload: %w", err)
	}

	site, err := q.GetSiteAssessment(ctx, payload.SiteAssessmentID)
	if err != nil {
		return fmt.Errorf("get site assessment %s: %w", payload.SiteAssessmentID, err)
	}

	rows, err := q.CountPhotosByConditionForLocation(ctx, site.LocationID)
	if err != nil {
		return fmt.Errorf("count photos for location %s: %w", site.LocationID, err)
	}

	var good, moderate, bad int32
	for _, row := range rows {
		switch row.Condition {
		case "good":
			good = int32(row.Count)
		case "moderate":
			moderate = int32(row.Count)
		case "bad":
			bad = int32(row.Count)
		}
	}

	if err := q.UpsertLocationStats(ctx, db.UpsertLocationStatsParams{
		LocationID:    site.LocationID,
		GoodCount:     good,
		ModerateCount: moderate,
		BadCount:      bad,
	}); err != nil {
		return fmt.Errorf("upsert location stats for %s: %w", site.LocationID, err)
	}
	return nil
}
