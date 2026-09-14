package notifier

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

type fakePusher struct {
	pushed []string
	failOn string
}

func (f *fakePusher) Push(ctx context.Context, connectionID string, message []byte) error {
	if connectionID == f.failOn {
		return context.DeadlineExceeded
	}
	f.pushed = append(f.pushed, connectionID)
	return nil
}

func TestHandle_PushesToAllConnectionsForLocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)

	siteID := uuid.New()
	locationID := uuid.New()
	photoID := uuid.New()

	q.EXPECT().GetSiteAssessment(gomock.Any(), siteID).Return(db.SiteAssessment{ID: siteID, LocationID: locationID}, nil)
	q.EXPECT().ListWSConnectionsByLocation(gomock.Any(), locationID).Return([]db.WsConnection{
		{ConnectionID: "conn-1", LocationID: locationID},
		{ConnectionID: "conn-2", LocationID: locationID},
	}, nil)

	pusher := &fakePusher{}
	evt := db.DomainEvent{
		ID:      photoID,
		Payload: []byte(`{"id":"` + photoID.String() + `","siteAssessmentId":"` + siteID.String() + `","condition":"bad"}`),
	}

	if err := Handle(context.Background(), q, pusher, evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(pusher.pushed) != 2 {
		t.Fatalf("pushed to %d connections, want 2", len(pusher.pushed))
	}
}

func TestHandle_NoConnectionsIsNotAnError(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)

	siteID := uuid.New()
	locationID := uuid.New()

	q.EXPECT().GetSiteAssessment(gomock.Any(), siteID).Return(db.SiteAssessment{ID: siteID, LocationID: locationID}, nil)
	q.EXPECT().ListWSConnectionsByLocation(gomock.Any(), locationID).Return([]db.WsConnection{}, nil)

	evt := db.DomainEvent{
		ID:      uuid.New(),
		Payload: []byte(`{"id":"` + uuid.New().String() + `","siteAssessmentId":"` + siteID.String() + `","condition":"good"}`),
	}

	if err := Handle(context.Background(), q, &fakePusher{}, evt); err != nil {
		t.Fatalf("Handle() error = %v, want nil (no subscribers isn't a failure)", err)
	}
}

func TestHandle_MalformedPayloadErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	evt := db.DomainEvent{ID: uuid.New(), Payload: []byte(`not json`)}

	if err := Handle(context.Background(), q, &fakePusher{}, evt); err == nil {
		t.Error("Handle() with malformed payload: want error, got nil")
	}
}
