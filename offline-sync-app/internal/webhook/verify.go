// Package webhook receives signed inbound webhooks. The handler only
// authenticates and persists (inbox pattern); a Worker applies the stored
// events later, so a slow or failing downstream never makes the sender retry.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderDeliveryID = "X-Webhook-Delivery"
	HeaderTimestamp  = "X-Webhook-Timestamp"
	HeaderSignature  = "X-Webhook-Signature"

	signaturePrefix = "sha256="
)

var (
	ErrMalformedHeaders = errors.New("missing or malformed signature headers")
	ErrBadSignature     = errors.New("signature mismatch")
	ErrStaleTimestamp   = errors.New("timestamp outside tolerance")
)

type Verifier struct {
	secret    []byte
	tolerance time.Duration
	now       func() time.Time
}

func NewVerifier(secret string, tolerance time.Duration, now func() time.Time) *Verifier {
	return &Verifier{secret: []byte(secret), tolerance: tolerance, now: now}
}

// Verify checks HMAC-SHA256(secret, timestamp + "." + deliveryID + "." + body)
// against the signature header. body must be the raw bytes as received:
// re-encoding parsed JSON changes the bytes and breaks the MAC.
func (v *Verifier) Verify(timestamp, deliveryID, signature string, body []byte) error {
	hexSig, ok := strings.CutPrefix(signature, signaturePrefix)
	// A "." in the id would let a captured request shift body bytes into the id and keep a valid MAC.
	if !ok || timestamp == "" || strings.Contains(deliveryID, ".") {
		return ErrMalformedHeaders
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return ErrMalformedHeaders
	}
	secs, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrMalformedHeaders
	}

	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(timestamp + "." + deliveryID + "."))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrBadSignature
	}

	age := v.now().Sub(time.Unix(secs, 0))
	if age > v.tolerance || age < -v.tolerance {
		return ErrStaleTimestamp
	}
	return nil
}
