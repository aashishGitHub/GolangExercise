package webhook

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

func TestDBInbox_Insert(t *testing.T) {
	tests := []struct {
		name         string
		rowsAffected int64
		want         bool
	}{
		{"first delivery inserts one row", 1, true},
		{"duplicate delivery inserts none", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := mocks.NewMockQuerier(ctrl)
			q.EXPECT().InsertWebhookInbox(gomock.Any(), db.InsertWebhookInboxParams{System: "mockjira", DeliveryID: "dlv-1", Body: []byte("raw")}).
				Return(tt.rowsAffected, nil)

			got, err := NewDBInbox(q).Insert(context.Background(), "mockjira", "dlv-1", []byte("raw"))

			if err != nil || got != tt.want {
				t.Errorf("Insert() = %v, %v, want %v", got, err, tt.want)
			}
		})
	}
}
