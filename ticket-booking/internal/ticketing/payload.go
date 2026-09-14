package ticketing

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// qrPayload is what's actually encoded into the QR image — the SAME
// v1.<b64 payload>.<b64 sig> shape internal/waitingroom's admission
// tokens use, a deliberate house convention: one signed-token format
// across the codebase instead of two ad hoc ones.
type qrPayload struct {
	TicketID uuid.UUID `json:"tid"`
	OrderID  uuid.UUID `json:"oid"`
	EventID  int64     `json:"eid"`
	SeatID   int64     `json:"sid"`
}

var (
	ErrBadSignature = errors.New("ticketing: bad or tampered QR signature")
	ErrMalformed    = errors.New("ticketing: malformed QR payload")
)

func encodeQR(signer Signer, p qrPayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	b64body := base64.RawURLEncoding.EncodeToString(body)
	sig := signer.Sign([]byte(b64body))
	return "v1." + b64body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func decodeQR(signer Signer, token string) (qrPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return qrPayload{}, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return qrPayload{}, ErrMalformed
	}
	if !signer.Verify([]byte(parts[1]), sig) {
		return qrPayload{}, ErrBadSignature
	}
	bodyBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return qrPayload{}, ErrMalformed
	}
	var p qrPayload
	if err := json.Unmarshal(bodyBytes, &p); err != nil {
		return qrPayload{}, ErrMalformed
	}
	return p, nil
}
