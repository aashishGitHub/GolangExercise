package twoway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type APIError struct {
	Status     int
	RetryAfter time.Duration
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("external API answered %d: %s", e.Status, e.Body)
}

func (e *APIError) retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// RetryExhaustedError means every attempt hit a retryable failure.
type RetryExhaustedError struct {
	Attempts int
	Last     error
}

func (e *RetryExhaustedError) Error() string {
	return fmt.Sprintf("gave up after %d attempts: %v", e.Attempts, e.Last)
}

func (e *RetryExhaustedError) Unwrap() error { return e.Last }

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

var DefaultRetryPolicy = RetryPolicy{MaxAttempts: 5, BaseDelay: 200 * time.Millisecond, MaxDelay: 10 * time.Second}

type HTTPClient struct {
	baseURL string
	http    *http.Client
	policy  RetryPolicy
	sleep   func(ctx context.Context, d time.Duration) error
	rand    func() float64
}

func NewHTTPClient(baseURL string, policy RetryPolicy) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
		policy:  policy,
		sleep:   sleepContext,
		rand:    rand.Float64,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *HTTPClient) List(ctx context.Context, since time.Time, cursor string, limit int) (Page, error) {
	q := url.Values{}
	if !since.IsZero() {
		q.Set("updated_since", since.UTC().Format(time.RFC3339Nano))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var page Page
	err := c.do(ctx, http.MethodGet, "/issues?"+q.Encode(), nil, "", &page)
	return page, err
}

// Patch sends only the fields set in p. A repeated idempotencyKey makes the
// server replay its first answer instead of applying the change again.
func (c *HTTPClient) Patch(ctx context.Context, externalID string, p IssuePatch, idempotencyKey string) (ExternalIssue, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return ExternalIssue{}, fmt.Errorf("marshal patch: %w", err)
	}
	var issue ExternalIssue
	err = c.do(ctx, http.MethodPatch, "/issues/"+url.PathEscape(externalID), body, idempotencyKey, &issue)
	return issue, err
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body []byte, idempotencyKey string, out any) error {
	var last error
	for attempt := 1; attempt <= c.policy.MaxAttempts; attempt++ {
		err := c.once(ctx, method, path, body, idempotencyKey, out)
		if err == nil {
			return nil
		}
		if !c.retryable(ctx, err) {
			return err
		}
		last = err
		if attempt == c.policy.MaxAttempts {
			break
		}
		if err := c.sleep(ctx, c.delay(attempt, err)); err != nil {
			return err
		}
	}
	return &RetryExhaustedError{Attempts: c.policy.MaxAttempts, Last: last}
}

func (c *HTTPClient) retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.retryable()
	}
	return true
}

// delay honours the server's Retry-After when it sent one, otherwise full
// jitter: a uniform draw from [0, min(MaxDelay, BaseDelay * 2^(attempt-1))].
func (c *HTTPClient) delay(attempt int, err error) time.Duration {
	var api *APIError
	if errors.As(err, &api) && api.RetryAfter > 0 {
		return api.RetryAfter
	}
	ceiling := c.policy.BaseDelay
	for i := 1; i < attempt && ceiling < c.policy.MaxDelay; i++ {
		ceiling *= 2
	}
	ceiling = min(ceiling, c.policy.MaxDelay)
	return time.Duration(c.rand() * float64(ceiling))
}

func (c *HTTPClient) once(ctx context.Context, method, path string, body []byte, idempotencyKey string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), Body: truncate(string(raw), 200)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// parseRetryAfter reads the delta-seconds form only; an HTTP-date value is
// treated as absent.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
