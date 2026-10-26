package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testSecret = "whsec-test"

var testNow = time.Unix(1_700_000_000, 0)

type fakeInbox struct {
	rows map[string][]byte
	err  error
}

func newFakeInbox() *fakeInbox { return &fakeInbox{rows: map[string][]byte{}} }

func (f *fakeInbox) Insert(_ context.Context, system, deliveryID string, body []byte) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	key := system + "/" + deliveryID
	if _, dup := f.rows[key]; dup {
		return false, nil
	}
	f.rows[key] = body
	return true, nil
}

func newTestHandler(inbox Inbox) http.Handler {
	v := NewVerifier(testSecret, 5*time.Minute, func() time.Time { return testNow })
	return NewHandler("mockjira", v, inbox, 1024)
}

func signAt(ts time.Time, deliveryID, body string) (timestamp, signature string) {
	timestamp = strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(timestamp + "." + deliveryID + "." + body))
	return timestamp, "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func post(h http.Handler, deliveryID, timestamp, signature, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockjira", strings.NewReader(body))
	req.Header.Set(HeaderDeliveryID, deliveryID)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderSignature, signature)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHandler_ValidDeliveryIs202AndStoredRaw(t *testing.T) {
	inbox := newFakeInbox()
	body := `{"event": "issue.updated",   "issue":{"id":"MJ-1"}}`
	ts, sig := signAt(testNow, "dlv-1", body)

	w := post(newTestHandler(inbox), "dlv-1", ts, sig, body)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if got := string(inbox.rows["mockjira/dlv-1"]); got != body {
		t.Errorf("stored body = %q, want the raw bytes %q", got, body)
	}
}

func TestHandler_BadSignatureIs401AndNothingStored(t *testing.T) {
	inbox := newFakeInbox()
	body := `{"issue":{"id":"MJ-1"}}`
	ts, _ := signAt(testNow, "dlv-1", body)

	w := post(newTestHandler(inbox), "dlv-1", ts, "sha256="+strings.Repeat("0", 64), body)

	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "bad_signature") {
		t.Errorf("got %d %s, want 401 bad_signature", w.Code, w.Body.String())
	}
	if len(inbox.rows) != 0 {
		t.Errorf("inbox has %d rows, want 0: an unauthenticated delivery must never be persisted", len(inbox.rows))
	}
}

func TestHandler_TamperedBodyIs401(t *testing.T) {
	inbox := newFakeInbox()
	ts, sig := signAt(testNow, "dlv-1", `{"issue":{"id":"MJ-1"}}`)

	w := post(newTestHandler(inbox), "dlv-1", ts, sig, `{"issue":{"id":"MJ-2"}}`)

	if w.Code != http.StatusUnauthorized || len(inbox.rows) != 0 {
		t.Errorf("got %d with %d stored, want 401 and nothing stored", w.Code, len(inbox.rows))
	}
}

func TestHandler_DeliveryIDSwappedAfterSigningIs401SoTheRealDeliveryIsNotSwallowed(t *testing.T) {
	inbox := newFakeInbox()
	h := newTestHandler(inbox)
	captured := `{"issue":{"id":"MJ-1"},"version":1}`
	ts, sig := signAt(testNow, "dlv-1", captured)

	forged := post(h, "dlv-2", ts, sig, captured)

	if forged.Code != http.StatusUnauthorized || !strings.Contains(forged.Body.String(), "bad_signature") || len(inbox.rows) != 0 {
		t.Fatalf("forged id: got %d %s with %d stored, want 401 bad_signature and nothing stored", forged.Code, forged.Body.String(), len(inbox.rows))
	}
	genuine := `{"issue":{"id":"MJ-1"},"version":2}`
	ts, sig = signAt(testNow, "dlv-2", genuine)
	w := post(h, "dlv-2", ts, sig, genuine)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "accepted") || string(inbox.rows["mockjira/dlv-2"]) != genuine {
		t.Errorf("genuine dlv-2: got %d %s, stored %q, want 202 accepted with its own body", w.Code, w.Body.String(), inbox.rows["mockjira/dlv-2"])
	}
}

func TestHandler_StaleTimestampIs401EvenWithValidSignature(t *testing.T) {
	inbox := newFakeInbox()
	body := `{"issue":{"id":"MJ-1"}}`
	ts, sig := signAt(testNow.Add(-10*time.Minute), "dlv-1", body)

	w := post(newTestHandler(inbox), "dlv-1", ts, sig, body)

	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "stale_timestamp") {
		t.Errorf("got %d %s, want 401 stale_timestamp", w.Code, w.Body.String())
	}
	if len(inbox.rows) != 0 {
		t.Errorf("inbox has %d rows, want 0", len(inbox.rows))
	}
}

func TestHandler_DuplicateDeliveryIs202AndStoredOnce(t *testing.T) {
	inbox := newFakeInbox()
	h := newTestHandler(inbox)
	body := `{"issue":{"id":"MJ-1"}}`
	ts, sig := signAt(testNow, "dlv-1", body)

	first := post(h, "dlv-1", ts, sig, body)
	second := post(h, "dlv-1", ts, sig, body)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("got %d then %d, want 202 both times", first.Code, second.Code)
	}
	if !strings.Contains(second.Body.String(), "duplicate") || len(inbox.rows) != 1 {
		t.Errorf("second body = %s, stored = %d, want duplicate and exactly 1 row", second.Body.String(), len(inbox.rows))
	}
}

func TestHandler_OversizedBodyIs413(t *testing.T) {
	inbox := newFakeInbox()
	body := strings.Repeat("x", 2048)
	ts, sig := signAt(testNow, "dlv-1", body)

	w := post(newTestHandler(inbox), "dlv-1", ts, sig, body)

	if w.Code != http.StatusRequestEntityTooLarge || len(inbox.rows) != 0 {
		t.Errorf("got %d with %d stored, want 413 and nothing stored", w.Code, len(inbox.rows))
	}
}

func TestHandler_MissingDeliveryIDIs400(t *testing.T) {
	body := `{"issue":{"id":"MJ-1"}}`
	ts, sig := signAt(testNow, "", body)

	w := post(newTestHandler(newFakeInbox()), "", ts, sig, body)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandler_InboxFailureIs500SoSenderRetries(t *testing.T) {
	inbox := newFakeInbox()
	inbox.err = errors.New("db down")
	body := `{"issue":{"id":"MJ-1"}}`
	ts, sig := signAt(testNow, "dlv-1", body)

	w := post(newTestHandler(inbox), "dlv-1", ts, sig, body)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
