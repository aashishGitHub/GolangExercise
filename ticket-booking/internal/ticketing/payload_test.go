package ticketing

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestQR_EncodeDecodeRoundTrip(t *testing.T) {
	signer := NewHMACSigner("test-secret")
	p := qrPayload{TicketID: uuid.New(), OrderID: uuid.New(), EventID: 1, SeatID: 500}

	token, err := encodeQR(signer, p)
	if err != nil {
		t.Fatalf("encodeQR: %v", err)
	}
	got, err := decodeQR(signer, token)
	if err != nil {
		t.Fatalf("decodeQR: %v", err)
	}
	if got != p {
		t.Fatalf("decoded payload = %+v, want %+v", got, p)
	}
}

func TestQR_TamperedPayloadRejected(t *testing.T) {
	signer := NewHMACSigner("test-secret")
	p := qrPayload{TicketID: uuid.New(), OrderID: uuid.New(), EventID: 1, SeatID: 500}
	token, err := encodeQR(signer, p)
	if err != nil {
		t.Fatalf("encodeQR: %v", err)
	}

	parts := strings.Split(token, ".")
	// Flip the FIRST char of the payload segment — always a fully-used
	// 6-bit group (see internal/waitingroom's identical lesson about
	// avoiding the last char's padding-only bits).
	b := []byte(parts[1])
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	tampered := parts[0] + "." + string(b) + "." + parts[2]

	if _, err := decodeQR(signer, tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("decodeQR(tampered payload) = %v, want ErrBadSignature", err)
	}
}

func TestQR_WrongSignerRejected(t *testing.T) {
	signer := NewHMACSigner("test-secret")
	otherSigner := NewHMACSigner("different-secret")
	p := qrPayload{TicketID: uuid.New(), OrderID: uuid.New(), EventID: 1, SeatID: 500}
	token, err := encodeQR(signer, p)
	if err != nil {
		t.Fatalf("encodeQR: %v", err)
	}
	if _, err := decodeQR(otherSigner, token); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("decodeQR with wrong signer = %v, want ErrBadSignature", err)
	}
}

func TestQR_MalformedTokenRejected(t *testing.T) {
	signer := NewHMACSigner("test-secret")
	cases := []string{"", "not-a-token", "v1.onlyonepart", "v2.x.y"}
	for _, c := range cases {
		if _, err := decodeQR(signer, c); !errors.Is(err, ErrMalformed) {
			t.Errorf("decodeQR(%q) = %v, want ErrMalformed", c, err)
		}
	}
}

func TestHMACSigner_VerifyRejectsWrongSignature(t *testing.T) {
	s := NewHMACSigner("k")
	sig := s.Sign([]byte("payload"))
	if !s.Verify([]byte("payload"), sig) {
		t.Fatal("Verify should accept its own signature")
	}
	if s.Verify([]byte("different payload"), sig) {
		t.Fatal("Verify should reject a signature for different data")
	}
}
