package dashboardaggregator

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

func TestHandle_RecomputesAndUpsertsStats(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)

	siteID := uuid.New()
	locationID := uuid.New()
	photoID := uuid.New()

	q.EXPECT().GetSiteAssessment(gomock.Any(), siteID).Return(db.SiteAssessment{ID: siteID, LocationID: locationID}, nil)
	q.EXPECT().CountPhotosByConditionForLocation(gomock.Any(), locationID).Return([]db.CountPhotosByConditionForLocationRow{
		{Condition: "good", Count: 3},
		{Condition: "moderate", Count: 1},
		{Condition: "bad", Count: 2},
	}, nil)
	q.EXPECT().UpsertLocationStats(gomock.Any(), db.UpsertLocationStatsParams{
		LocationID: locationID, GoodCount: 3, ModerateCount: 1, BadCount: 2,
	}).Return(nil)

	evt := db.DomainEvent{
		ID:      photoID,
		Payload: []byte(`{"id":"` + photoID.String() + `","siteAssessmentId":"` + siteID.String() + `"}`),
	}

	if err := Handle(context.Background(), q, evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

func TestHandle_MalformedPayloadErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	evt := db.DomainEvent{ID: uuid.New(), Payload: []byte(`not json`)}

	if err := Handle(context.Background(), q, evt); err == nil {
		t.Error("Handle() with malformed payload: want error, got nil")
	}
}
