package photoprocessor

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

func TestValidateGeotag(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lng     float64
		wantErr bool
	}{
		{"valid", 12.34, 56.78, false},
		{"boundary valid", 90, 180, false},
		{"boundary valid negative", -90, -180, false},
		{"latitude too high", 90.1, 0, true},
		{"latitude too low", -90.1, 0, true},
		{"longitude too high", 0, 180.1, true},
		{"longitude too low", 0, -180.1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGeotag(tt.lat, tt.lng)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGeotag(%v, %v) error = %v, wantErr %v", tt.lat, tt.lng, err, tt.wantErr)
			}
		})
	}
}

func TestHandle_MalformedPayloadErrors(t *testing.T) {
	evt := db.DomainEvent{ID: uuid.New(), Payload: []byte(`not json`)}
	if err := Handle(context.Background(), evt); err == nil {
		t.Error("Handle() with malformed payload: want error, got nil")
	}
}

func TestHandle_BadGeotagDoesNotError(t *testing.T) {
	evt := db.DomainEvent{ID: uuid.New(), Payload: []byte(`{"id":"` + uuid.New().String() + `","latitude":999,"longitude":0}`)}
	if err := Handle(context.Background(), evt); err != nil {
		t.Errorf("Handle() with bad geotag: want nil (logged finding, not a retryable error), got %v", err)
	}
}
