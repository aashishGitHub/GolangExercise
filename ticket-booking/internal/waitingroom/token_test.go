package waitingroom

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testQueue() *Queue {
	return &Queue{secret: []byte("test-secret")}
}

func TestToken_IssueAndVerifyRoundTrip(t *testing.T) {
	q := testQueue()
	token, err := q.issueToken("user-1", 42, 0)
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	if err := q.VerifyToken(token, "user-1", 42); err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
}

func TestToken_TamperedSignatureRejected(t *testing.T) {
	q := testQueue()
	token, err := q.issueToken("user-1", 42, 0)
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	parts := strings.Split(token, ".")
	// Flip a byte in the signature — the tamper docs/plan.md's Phase 8
	// verification specifically calls out ("tamper a signature byte").
	// Deliberately NOT the last character: a base64 RawURLEncoding of a
	// 32-byte digest has 2 unused padding bits in its final character,
	// and Go's decoder doesn't reject an unconventional-but-decodable
	// padding pattern there — flipping that position can silently decode
	// to the SAME real bytes, making the tamper undetectable by accident.
	// flipFirstChar always touches a real, fully-used 6-bit group.
	tampered := parts[0] + "." + parts[1] + "." + flipFirstChar(parts[2])
	if err := q.VerifyToken(tampered, "user-1", 42); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("VerifyToken(tampered) = %v, want ErrInvalidToken", err)
	}
}

func TestToken_WrongSubjectRejected(t *testing.T) {
	q := testQueue()
	token, err := q.issueToken("user-1", 42, 0)
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	// A different user replaying user-1's real, un-tampered token — the
	// "bound to the Cognito sub so it cannot be traded" property.
	if err := q.VerifyToken(token, "user-2", 42); !errors.Is(err, ErrSubMismatch) {
		t.Fatalf("VerifyToken(wrong sub) = %v, want ErrSubMismatch", err)
	}
}

func TestToken_WrongEventRejected(t *testing.T) {
	q := testQueue()
	token, err := q.issueToken("user-1", 42, 0)
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	if err := q.VerifyToken(token, "user-1", 99); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("VerifyToken(wrong event) = %v, want ErrInvalidToken", err)
	}
}

func TestToken_ExpiredRejected(t *testing.T) {
	q := testQueue()
	// Build a token by hand with an already-past expiry, since issueToken
	// always sets a future one.
	payload := tokenPayload{Sub: "user-1", EventID: 42, Pos: 0,
		IssuedAt: time.Now().Add(-2 * tokenTTL).Unix(), ExpAt: time.Now().Add(-time.Minute).Unix(), Nonce: "abcd"}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b64body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	sig := q.sign(b64body)
	token := "v1." + b64body + "." + base64.RawURLEncoding.EncodeToString(sig)
	if err := q.VerifyToken(token, "user-1", 42); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("VerifyToken(expired) = %v, want ErrTokenExpired", err)
	}
}

func TestToken_MalformedTokenRejected(t *testing.T) {
	q := testQueue()
	cases := []string{"", "not-a-token", "v1.onlyonepart", "v2." + "x" + "." + "y"}
	for _, c := range cases {
		if err := q.VerifyToken(c, "user-1", 42); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("VerifyToken(%q) = %v, want ErrInvalidToken", c, err)
		}
	}
}

func flipFirstChar(s string) string {
	if len(s) == 0 {
		return "x"
	}
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}
