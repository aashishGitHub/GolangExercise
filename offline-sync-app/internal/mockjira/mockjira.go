// Package mockjira is a small in-memory stand-in for a Jira-like SaaS, used to
// exercise internal/twoway without a real vendor. It is a test double: no
// auth, no persistence, and Jira's real wire format is not reproduced.
package mockjira

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	HeaderDeliveryID = "X-Webhook-Delivery"
	HeaderTimestamp  = "X-Webhook-Timestamp"
	HeaderSignature  = "X-Webhook-Signature"
)

var ErrNotFound = errors.New("issue not found")

type Issue struct {
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	Version     int64     `json:"version"`
	Deleted     bool      `json:"deleted"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Patch struct {
	Summary     *string `json:"summary,omitempty"`
	Status      *string `json:"status,omitempty"`
	Description *string `json:"description,omitempty"`
}

type Page struct {
	Issues     []Issue `json:"issues"`
	NextCursor string  `json:"nextCursor"`
}

type Delivery struct {
	ID   string
	Body []byte
}

type FlushMode int

const (
	FlushInOrder FlushMode = iota
	FlushDuplicated
	FlushReversed
	FlushDropped
)

type Options struct {
	WebhookURL    string
	WebhookSecret string
	Now           func() time.Time
}

type fault struct {
	status     int
	retryAfter time.Duration
}

type Server struct {
	opts   Options
	client *http.Client

	mu           sync.Mutex
	issues       map[string]*Issue
	nextIssue    int
	nextDelivery int
	idempotent   map[string]Issue
	pending      []Delivery
	faults       []fault
	patchCalls   int
}

func New(opts Options) *Server {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Server{
		opts:       opts,
		client:     &http.Client{Timeout: 5 * time.Second},
		issues:     map[string]*Issue{},
		idempotent: map[string]Issue{},
	}
}

func (s *Server) now() time.Time { return s.opts.Now().UTC() }

func (s *Server) Create(summary, status, description string) Issue {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextIssue++
	issue := &Issue{
		ID:          fmt.Sprintf("MJ-%d", s.nextIssue),
		Summary:     summary,
		Status:      status,
		Description: description,
		Version:     1,
		UpdatedAt:   s.now(),
	}
	s.issues[issue.ID] = issue
	s.enqueue("issue.created", *issue)
	return *issue
}

func (s *Server) Get(id string) (Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.issues[id]
	if !ok {
		return Issue{}, ErrNotFound
	}
	return *issue, nil
}

func (s *Server) Update(id string, p Patch) (Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(id, p)
}

func (s *Server) update(id string, p Patch) (Issue, error) {
	issue, ok := s.issues[id]
	if !ok {
		return Issue{}, ErrNotFound
	}
	if p.Summary != nil {
		issue.Summary = *p.Summary
	}
	if p.Status != nil {
		issue.Status = *p.Status
	}
	if p.Description != nil {
		issue.Description = *p.Description
	}
	issue.Version++
	issue.UpdatedAt = s.now()
	s.enqueue("issue.updated", *issue)
	return *issue, nil
}

func (s *Server) Delete(id string) (Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, ok := s.issues[id]
	if !ok {
		return Issue{}, ErrNotFound
	}
	issue.Deleted = true
	issue.Version++
	issue.UpdatedAt = s.now()
	s.enqueue("issue.deleted", *issue)
	return *issue, nil
}

// List is keyset-paginated on (UpdatedAt, ID); tombstones are included so an
// incremental pull can see deletes.
func (s *Server) List(since time.Time, cursor string, limit int) (Page, error) {
	after, err := decodeCursor(cursor)
	if err != nil {
		return Page{}, err
	}

	s.mu.Lock()
	matching := make([]Issue, 0, len(s.issues))
	for _, issue := range s.issues {
		if issue.UpdatedAt.Before(since) {
			continue
		}
		if cursor != "" && !after.before(*issue) {
			continue
		}
		matching = append(matching, *issue)
	}
	s.mu.Unlock()

	sort.Slice(matching, func(i, j int) bool { return less(matching[i], matching[j]) })

	page := Page{Issues: matching}
	if limit > 0 && len(matching) > limit {
		page.Issues = matching[:limit]
		page.NextCursor = encodeCursor(page.Issues[limit-1])
	}
	return page, nil
}

func less(a, b Issue) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.Before(b.UpdatedAt)
	}
	return a.ID < b.ID
}

type position struct {
	UpdatedAtNano int64  `json:"t"`
	ID            string `json:"id"`
}

func (p position) before(issue Issue) bool {
	return less(Issue{UpdatedAt: time.Unix(0, p.UpdatedAtNano), ID: p.ID}, issue)
}

func encodeCursor(issue Issue) string {
	raw, _ := json.Marshal(position{UpdatedAtNano: issue.UpdatedAt.UnixNano(), ID: issue.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(cursor string) (position, error) {
	if cursor == "" {
		return position{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return position{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var p position
	if err := json.Unmarshal(raw, &p); err != nil {
		return position{}, fmt.Errorf("invalid cursor: %w", err)
	}
	return p, nil
}

func (s *Server) PatchCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.patchCalls
}

// FailNext makes the next n REST calls answer with status (429 or 5xx) before
// touching any state. retryAfter is sent as whole seconds when positive.
func (s *Server) FailNext(n, status int, retryAfter time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < n; i++ {
		s.faults = append(s.faults, fault{status: status, retryAfter: retryAfter})
	}
}

func (s *Server) takeFault() (fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.faults) == 0 {
		return fault{}, false
	}
	f := s.faults[0]
	s.faults = s.faults[1:]
	return f, true
}

func (s *Server) enqueue(event string, issue Issue) {
	s.nextDelivery++
	body, _ := json.Marshal(map[string]any{"event": event, "issue": issue})
	s.pending = append(s.pending, Delivery{ID: fmt.Sprintf("dlv-%d", s.nextDelivery), Body: body})
}

func (s *Server) Pending() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivery(nil), s.pending...)
}

// Flush empties the pending queue and delivers it per mode. It returns the
// number of HTTP POSTs made and stops at the first transport error or non-2xx.
func (s *Server) Flush(mode FlushMode) (int, error) {
	s.mu.Lock()
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()

	switch mode {
	case FlushDropped:
		return 0, nil
	case FlushReversed:
		for i, j := 0, len(batch)-1; i < j; i, j = i+1, j-1 {
			batch[i], batch[j] = batch[j], batch[i]
		}
	}

	posts := 0
	for _, d := range batch {
		copies := 1
		if mode == FlushDuplicated {
			copies = 2
		}
		for i := 0; i < copies; i++ {
			status, err := s.Deliver(d, s.now())
			posts++
			if err != nil {
				return posts, err
			}
			if status < 200 || status > 299 {
				return posts, fmt.Errorf("delivery %s: receiver answered %d", d.ID, status)
			}
		}
	}
	return posts, nil
}

// Deliver POSTs one delivery signed as of signedAt, so a test can replay an
// old signature.
func (s *Server) Deliver(d Delivery, signedAt time.Time) (int, error) {
	ts := strconv.FormatInt(signedAt.Unix(), 10)
	req, err := http.NewRequest(http.MethodPost, s.opts.WebhookURL, bytes.NewReader(d.Body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderDeliveryID, d.ID)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, "sha256="+sign(s.opts.WebhookSecret, ts, d.ID, d.Body))

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// sign is HMAC-SHA256(secret, timestamp + "." + deliveryID + "." + body), hex encoded.
func sign(secret, ts, deliveryID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + deliveryID + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
