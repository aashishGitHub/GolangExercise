// Package photoprocessor reacts to "photo.upserted" domain events — the
// pragmatic local substitute for a real S3 ObjectCreated trigger, which
// needs an actual S3-to-EventBridge bridge we have no free local emulator
// for (see docker-compose.yml). Implements geotag range validation, the
// testable slice of "thumbnail/geotag validation" from the plan doc;
// thumbnail generation is out of scope here — it would need an image
// library and a second S3 write path disproportionate to this phase.
package photoprocessor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"fieldsync/internal/db"
)

const ConsumerName = "photo-processor"

var EventTypes = []string{"photo.upserted"}

type photoPayload struct {
	ID        uuid.UUID `json:"id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
}

// Handle validates the geotag and logs a finding on failure. It does not
// return an error for a bad geotag: that's a data-quality issue to surface,
// not a transient failure — returning an error would just retry forever on
// a photo whose geotag will never become valid. It returns an error only for
// genuine processing failures (e.g. a malformed payload).
func Handle(ctx context.Context, evt db.DomainEvent) error {
	var payload photoPayload
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal photo payload: %w", err)
	}

	if err := ValidateGeotag(payload.Latitude, payload.Longitude); err != nil {
		log.Printf("photo-processor: photo %s failed geotag validation: %v", payload.ID, err)
	}
	return nil
}

func ValidateGeotag(lat, lng float64) error {
	if lat < -90 || lat > 90 {
		return fmt.Errorf("latitude %f out of range [-90, 90]", lat)
	}
	if lng < -180 || lng > 180 {
		return fmt.Errorf("longitude %f out of range [-180, 180]", lng)
	}
	return nil
}
