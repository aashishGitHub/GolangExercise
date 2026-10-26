// Package twoway keeps FieldSync work items in sync with an external SaaS in
// both directions: inbound (webhooks and an incremental pull) through Applier,
// outbound (domain events) through Loader.
package twoway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	fsync "fieldsync/internal/sync"
)

type Outcome = fsync.Outcome

const (
	OutcomeApplied              = fsync.OutcomeApplied
	OutcomeIgnoredStale         = fsync.OutcomeIgnoredStale
	OutcomeConflict             = fsync.OutcomeConflict
	OutcomeEcho         Outcome = "echo"
)

type ExternalIssue struct {
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	Version     int64     `json:"version"`
	Deleted     bool      `json:"deleted"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type IssuePatch struct {
	Summary     *string `json:"summary,omitempty"`
	Status      *string `json:"status,omitempty"`
	Description *string `json:"description,omitempty"`
}

type Page struct {
	Issues     []ExternalIssue `json:"issues"`
	NextCursor string          `json:"nextCursor"`
}

// Content is the field-mapped subset of a work item that the two systems
// share: title <-> summary, status <-> status, notes <-> description.
type Content struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	Notes  string `json:"notes"`
}

func ContentOf(issue ExternalIssue) Content {
	return Content{Title: issue.Summary, Status: issue.Status, Notes: issue.Description}
}

func (c Content) Hash() string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
