package twoway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fieldsync/internal/mockjira"
)

type clientFixture struct {
	client *HTTPClient
	jira   *mockjira.Server
	sleeps []time.Duration
}

func newClientFixture(t *testing.T, policy RetryPolicy) *clientFixture {
	t.Helper()
	jira := mockjira.New(mockjira.Options{})
	srv := httptest.NewServer(jira.Handler())
	t.Cleanup(srv.Close)

	f := &clientFixture{jira: jira, client: NewHTTPClient(srv.URL, policy)}
	f.client.sleep = func(_ context.Context, d time.Duration) error {
		f.sleeps = append(f.sleeps, d)
		return nil
	}
	f.client.rand = func() float64 { return 0.5 }
	return f
}

var testPolicy = RetryPolicy{MaxAttempts: 4, BaseDelay: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond}

func TestPatch_429HonoursRetryAfterThenSucceeds(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	issue := f.jira.Create("t", "Open", "")
	f.jira.FailNext(1, http.StatusTooManyRequests, 7*time.Second)

	got, err := f.client.Patch(context.Background(), issue.ID, IssuePatch{Description: strPtr("n")}, "key-1")

	if err != nil || got.Version != 2 {
		t.Fatalf("Patch() = %+v, %v, want version 2", got, err)
	}
	if len(f.sleeps) != 1 || f.sleeps[0] != 7*time.Second {
		t.Errorf("sleeps = %v, want exactly the server's Retry-After of 7s, not our own backoff", f.sleeps)
	}
}

func TestPatch_5xxBacksOffWithFullJitterAndCap(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	issue := f.jira.Create("t", "Open", "")
	f.jira.FailNext(3, http.StatusInternalServerError, 0)

	_, err := f.client.Patch(context.Background(), issue.ID, IssuePatch{Description: strPtr("n")}, "key-1")

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	// rand is pinned at 0.5; the ceilings are 100ms, 200ms, then 300ms (capped from 400ms).
	want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond}
	if len(f.sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", f.sleeps, want)
	}
	for i := range want {
		if f.sleeps[i] != want[i] {
			t.Errorf("sleep %d = %v, want %v", i, f.sleeps[i], want[i])
		}
	}
}

func TestPatch_JitterIsADrawFromZeroToCeiling(t *testing.T) {
	f := newClientFixture(t, testPolicy)

	f.client.rand = func() float64 { return 0 }
	low := f.client.delay(2, errors.New("boom"))
	f.client.rand = func() float64 { return 0.999999 }
	high := f.client.delay(2, errors.New("boom"))

	if low != 0 || high <= 190*time.Millisecond || high >= 200*time.Millisecond {
		t.Errorf("attempt 2 delay ranged %v..%v, want 0 up to just under the 200ms ceiling", low, high)
	}
}

func TestPatch_GivesUpAfterMaxAttemptsWithoutSleepingAfterTheLast(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	issue := f.jira.Create("t", "Open", "")
	f.jira.FailNext(10, http.StatusInternalServerError, 0)

	_, err := f.client.Patch(context.Background(), issue.ID, IssuePatch{}, "key-1")

	var exhausted *RetryExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Attempts != 4 {
		t.Fatalf("Patch() error = %v, want RetryExhaustedError after 4 attempts", err)
	}
	if len(f.sleeps) != 3 {
		t.Errorf("slept %d times, want 3 (between 4 attempts only)", len(f.sleeps))
	}
}

func TestPatch_PermanentClientErrorIsNotRetried(t *testing.T) {
	f := newClientFixture(t, testPolicy)

	_, err := f.client.Patch(context.Background(), "MJ-404", IssuePatch{}, "key-1")

	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusNotFound {
		t.Fatalf("Patch() error = %v, want a 404 APIError", err)
	}
	if len(f.sleeps) != 0 {
		t.Errorf("slept %v, want no retry for a 404", f.sleeps)
	}
}

func TestPatch_CancelledContextStopsRetrying(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	issue := f.jira.Create("t", "Open", "")
	f.jira.FailNext(10, http.StatusInternalServerError, 0)
	ctx, cancel := context.WithCancel(context.Background())
	f.client.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	_, err := f.client.Patch(ctx, issue.ID, IssuePatch{}, "key-1")

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Patch() error = %v, want context.Canceled", err)
	}
}

func TestPatch_SameIdempotencyKeyAppliesOnce(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	issue := f.jira.Create("t", "Open", "")

	first, _ := f.client.Patch(context.Background(), issue.ID, IssuePatch{Description: strPtr("n")}, "evt-1")
	second, _ := f.client.Patch(context.Background(), issue.ID, IssuePatch{Description: strPtr("n")}, "evt-1")

	if first.Version != 2 || second.Version != 2 {
		t.Errorf("versions = %d, %d, want 2 and 2: the replay must not bump again", first.Version, second.Version)
	}
}

func TestList_PagesThroughCursorAndSendsUpdatedSince(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	for i := 0; i < 3; i++ {
		f.jira.Create("t", "Open", "")
	}

	first, err := f.client.List(context.Background(), time.Time{}, "", 2)
	if err != nil || len(first.Issues) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v, want 2 issues and a cursor", first, err)
	}
	second, err := f.client.List(context.Background(), time.Time{}, first.NextCursor, 2)
	if err != nil || len(second.Issues) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %+v, %v, want the last issue and no cursor", second, err)
	}

	future, _ := f.client.List(context.Background(), time.Now().Add(time.Hour), "", 10)
	if len(future.Issues) != 0 {
		t.Errorf("updated_since in the future returned %d issues, want 0", len(future.Issues))
	}
}

func TestList_429ThenSuccess(t *testing.T) {
	f := newClientFixture(t, testPolicy)
	f.jira.FailNext(1, http.StatusTooManyRequests, 2*time.Second)

	_, err := f.client.List(context.Background(), time.Time{}, "", 10)

	if err != nil || len(f.sleeps) != 1 || f.sleeps[0] != 2*time.Second {
		t.Errorf("List() error = %v, sleeps = %v, want success after one 2s wait", err, f.sleeps)
	}
}
