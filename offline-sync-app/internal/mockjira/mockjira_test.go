package mockjira

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedDelivery struct {
	id        string
	timestamp string
	signature string
	body      []byte
}

type recorder struct {
	mu   sync.Mutex
	got  []recordedDelivery
	http *httptest.Server
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.got = append(rec.got, recordedDelivery{
			id:        r.Header.Get(HeaderDeliveryID),
			timestamp: r.Header.Get(HeaderTimestamp),
			signature: r.Header.Get(HeaderSignature),
			body:      body,
		})
		rec.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(rec.http.Close)
	return rec
}

func (r *recorder) deliveries() []recordedDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedDelivery(nil), r.got...)
}

func newServerWithWebhook(t *testing.T) (*Server, *recorder) {
	t.Helper()
	rec := newRecorder(t)
	return New(Options{WebhookURL: rec.http.URL, WebhookSecret: "s3cret"}), rec
}

func do(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPatch_IncrementsVersionServerSide(t *testing.T) {
	srv := New(Options{})
	issue := srv.Create("t", "Open", "d")

	w := do(t, srv.Handler(), http.MethodPatch, "/issues/"+issue.ID, `{"status":"Done"}`, nil)

	var got Issue
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != 2 || got.Status != "Done" || got.Summary != "t" {
		t.Errorf("got %+v, want version 2, status Done, summary untouched", got)
	}
}

func TestPatch_SameIdempotencyKeyDoesNotBumpVersionTwice(t *testing.T) {
	srv := New(Options{})
	issue := srv.Create("t", "Open", "d")
	h := srv.Handler()
	hdr := map[string]string{"Idempotency-Key": "evt-1"}

	do(t, h, http.MethodPatch, "/issues/"+issue.ID, `{"description":"a"}`, hdr)
	w := do(t, h, http.MethodPatch, "/issues/"+issue.ID, `{"description":"a"}`, hdr)

	var replay Issue
	json.Unmarshal(w.Body.Bytes(), &replay)
	stored, _ := srv.Get(issue.ID)
	if stored.Version != 2 || replay.Version != 2 {
		t.Errorf("stored version = %d, replay version = %d, want both 2", stored.Version, replay.Version)
	}
}

func TestList_KeysetPaginationAndUpdatedSince(t *testing.T) {
	var tick int64
	srv := New(Options{Now: func() time.Time {
		tick++
		return time.Unix(1_700_000_000+tick, 0)
	}})
	for i := 0; i < 5; i++ {
		srv.Create("t", "Open", "")
	}

	first, _ := srv.List(time.Time{}, "", 2)
	second, _ := srv.List(time.Time{}, first.NextCursor, 2)
	third, _ := srv.List(time.Time{}, second.NextCursor, 2)

	var ids []string
	for _, p := range []Page{first, second, third} {
		for _, i := range p.Issues {
			ids = append(ids, i.ID)
		}
	}
	if strings.Join(ids, ",") != "MJ-1,MJ-2,MJ-3,MJ-4,MJ-5" {
		t.Errorf("paged ids = %v, want MJ-1..MJ-5 in order with no repeats", ids)
	}
	if third.NextCursor != "" {
		t.Errorf("last page cursor = %q, want empty", third.NextCursor)
	}

	since := time.Unix(1_700_000_004, 0)
	recent, _ := srv.List(since, "", 10)
	if len(recent.Issues) != 2 {
		t.Errorf("updated_since returned %d issues, want 2", len(recent.Issues))
	}
}

func TestList_IncludesTombstones(t *testing.T) {
	srv := New(Options{})
	issue := srv.Create("t", "Open", "")
	srv.Delete(issue.ID)

	page, _ := srv.List(time.Time{}, "", 10)

	if len(page.Issues) != 1 || !page.Issues[0].Deleted || page.Issues[0].Version != 2 {
		t.Errorf("got %+v, want one tombstone at version 2", page.Issues)
	}
}

func TestFailNext_429CarriesRetryAfterThenRecovers(t *testing.T) {
	srv := New(Options{})
	srv.FailNext(1, http.StatusTooManyRequests, 3*time.Second)

	w := do(t, srv.Handler(), http.MethodGet, "/issues", "", nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "3" {
		t.Errorf("got %d Retry-After=%q, want 429 and 3", w.Code, w.Header().Get("Retry-After"))
	}

	w = do(t, srv.Handler(), http.MethodGet, "/issues", "", nil)
	if w.Code != http.StatusOK {
		t.Errorf("after the injected fault: got %d, want 200", w.Code)
	}
}

func TestFailNext_500DoesNotTouchState(t *testing.T) {
	srv := New(Options{})
	issue := srv.Create("t", "Open", "")
	srv.FailNext(1, http.StatusInternalServerError, 0)

	w := do(t, srv.Handler(), http.MethodPatch, "/issues/"+issue.ID, `{"status":"Done"}`, nil)

	stored, _ := srv.Get(issue.ID)
	if w.Code != http.StatusInternalServerError || stored.Version != 1 {
		t.Errorf("got %d, version %d, want 500 and untouched version 1", w.Code, stored.Version)
	}
}

func TestSign_MatchesOpenSSLVector(t *testing.T) {
	const want = "4115fa7ca537e9209469446d5e3b025790a8740dceff8a6a4fbe7059313b4b8a"

	if got := sign("s3cret", "1700000000", "dlv-1", []byte(`{"a":1}`)); got != want {
		t.Errorf("sign() = %s, want %s (vector from openssl dgst -sha256 -hmac)", got, want)
	}
}

func TestDeliver_HeadersCarryDeliveryIDTimestampAndSignature(t *testing.T) {
	srv, rec := newServerWithWebhook(t)
	srv.Create("t", "Open", "")

	if _, err := srv.Flush(FlushInOrder); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	got := rec.deliveries()[0]
	if got.id != "dlv-1" || got.timestamp == "" || !strings.HasPrefix(got.signature, "sha256=") {
		t.Errorf("headers = %+v, want delivery id dlv-1, a timestamp and a sha256= signature", got)
	}
	if !bytes.Contains(got.body, []byte(`"event":"issue.created"`)) {
		t.Errorf("body = %s, want an issue.created event", got.body)
	}
}

func TestFlush_Modes(t *testing.T) {
	tests := []struct {
		name      string
		mode      FlushMode
		wantOrder []string
	}{
		{"in order", FlushInOrder, []string{"dlv-1", "dlv-2"}},
		{"duplicated", FlushDuplicated, []string{"dlv-1", "dlv-1", "dlv-2", "dlv-2"}},
		{"reversed", FlushReversed, []string{"dlv-2", "dlv-1"}},
		{"dropped", FlushDropped, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, rec := newServerWithWebhook(t)
			srv.Create("a", "Open", "")
			srv.Create("b", "Open", "")

			if _, err := srv.Flush(tt.mode); err != nil {
				t.Fatalf("Flush() error = %v", err)
			}

			var ids []string
			for _, d := range rec.deliveries() {
				ids = append(ids, d.id)
			}
			if strings.Join(ids, ",") != strings.Join(tt.wantOrder, ",") {
				t.Errorf("delivered %v, want %v", ids, tt.wantOrder)
			}
			if len(srv.Pending()) != 0 {
				t.Errorf("pending = %d after flush, want 0", len(srv.Pending()))
			}
		})
	}
}

func TestDelivery_HeldUntilFlushed(t *testing.T) {
	srv, rec := newServerWithWebhook(t)
	srv.Create("t", "Open", "")

	if len(rec.deliveries()) != 0 || len(srv.Pending()) != 1 {
		t.Fatalf("delivered %d, pending %d, want nothing sent before Flush", len(rec.deliveries()), len(srv.Pending()))
	}
}
