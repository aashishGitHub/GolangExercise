package waitingroom

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// tokenTTL is how long an admission token remains valid once issued —
// long enough to cover a full checkout session (hold -> pay -> confirm)
// without the user getting bounced back to the queue mid-purchase.
const tokenTTL = 15 * time.Minute

var (
	ErrInvalidToken = errors.New("waitingroom: invalid admission token")
	ErrTokenExpired = errors.New("waitingroom: admission token expired")
	ErrSubMismatch  = errors.New("waitingroom: admission token does not belong to this user")
)

// tokenPayload is docs/plan.md's exact field set: {sub,eid,pos,iat,exp,nonce}.
// nonce is carried for future single-use enforcement (a Redis SETNX per
// verification) but is NOT checked against a use-once store here — that
// would add a Redis round trip to every hold request for a marginal
// benefit on top of the sub+event binding and expiry already in force.
// Honest scope cut, not an oversight.
type tokenPayload struct {
	Sub      string `json:"sub"`
	EventID  int64  `json:"eid"`
	Pos      int64  `json:"pos"`
	IssuedAt int64  `json:"iat"`
	ExpAt    int64  `json:"exp"`
	Nonce    string `json:"nonce"`
}

// issueToken builds `v1.<b64 payload>.<b64 HMAC-SHA256>` — HMAC, not JWT
// (docs/plan.md): there is exactly one verifier (this service), so a
// standard library JWT's header/alg-confusion surface buys nothing here.
func (q *Queue) issueToken(sub string, eventID, pos int64) (string, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	now := time.Now()
	payload := tokenPayload{
		Sub: sub, EventID: eventID, Pos: pos,
		IssuedAt: now.Unix(), ExpAt: now.Add(tokenTTL).Unix(),
		Nonce: hex.EncodeToString(nonce),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	b64body := base64.RawURLEncoding.EncodeToString(body)
	sig := q.sign(b64body)
	return "v1." + b64body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (q *Queue) sign(b64body string) []byte {
	mac := hmac.New(sha256.New, q.secret)
	mac.Write([]byte(b64body))
	return mac.Sum(nil)
}

// VerifyToken checks signature (constant-time), expiry, and that the token
// belongs to expectedSub for expectedEventID — the "bound to the Cognito
// sub so it cannot be traded" requirement: a stolen/shared token verifies
// against the WRONG sub and is rejected exactly like a forged one.
func (q *Queue) VerifyToken(token, expectedSub string, expectedEventID int64) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return ErrInvalidToken
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidToken
	}
	if !hmac.Equal(gotSig, q.sign(parts[1])) {
		return ErrInvalidToken
	}
	bodyBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}
	var payload tokenPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return ErrInvalidToken
	}
	if payload.EventID != expectedEventID {
		return ErrInvalidToken
	}
	if payload.Sub != expectedSub {
		return ErrSubMismatch
	}
	if time.Now().Unix() > payload.ExpAt {
		return ErrTokenExpired
	}
	return nil
}
