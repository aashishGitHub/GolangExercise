package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"fieldsync/internal/auth"
	"fieldsync/internal/storage"
	"fieldsync/internal/sync"
)

const presignExpiry = 15 * time.Minute

type presignRequest struct {
	PhotoID     uuid.UUID `json:"photoId"`
	ContentType string    `json:"contentType"`
}

type presignResponse struct {
	UploadURL string `json:"uploadUrl"`
	S3Key     string `json:"s3Key"`
}

// presignHandler mints a presigned PUT URL for one photo. The site id in the
// path only namespaces the S3 key (photos/{siteId}/{photoId}) — the actual
// site_assessment_id association is set on confirm, where it's persisted.
func presignHandler(store *storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		siteID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "invalid site id", http.StatusBadRequest)
			return
		}
		var req presignRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PhotoID == uuid.Nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ContentType == "" {
			req.ContentType = "image/jpeg"
		}

		key := fmt.Sprintf("photos/%s/%s", siteID, req.PhotoID)
		url, err := store.PresignPut(r.Context(), key, req.ContentType, presignExpiry)
		if err != nil {
			http.Error(w, "presign failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(presignResponse{UploadURL: url, S3Key: key})
	}
}

type confirmRequest struct {
	S3Key     string    `json:"s3Key"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Condition string    `json:"condition"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// confirmHandler verifies the object actually landed in S3, then commits the
// photo row through the same sync logic every /api/sync call uses — one
// LWW/idempotency/≤10-rule implementation, not two.
func confirmHandler(store *storage.Storage, svc sync.Syncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		siteID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "invalid site id", http.StatusBadRequest)
			return
		}
		photoID, err := uuid.Parse(chi.URLParam(r, "photoId"))
		if err != nil {
			http.Error(w, "invalid photo id", http.StatusBadRequest)
			return
		}
		var req confirmRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		exists, err := store.Exists(r.Context(), req.S3Key)
		if err != nil {
			http.Error(w, "storage check failed", http.StatusInternalServerError)
			return
		}
		if !exists {
			http.Error(w, "upload not found in storage — confirm called before the PUT completed", http.StatusConflict)
			return
		}

		claims, _ := auth.FromContext(r.Context())
		resp, err := svc.Sync(r.Context(), claims.Sub, sync.Request{
			Photos: []sync.PhotoInput{{
				ID:               photoID,
				SiteAssessmentID: siteID,
				S3Key:            req.S3Key,
				Latitude:         req.Latitude,
				Longitude:        req.Longitude,
				Condition:        req.Condition,
				CreatedAt:        req.CreatedAt,
				UpdatedAt:        req.UpdatedAt,
			}},
		})
		if err != nil {
			http.Error(w, "confirm failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp.Photos[0])
	}
}
