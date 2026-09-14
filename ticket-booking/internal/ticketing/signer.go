// Package ticketing issues QR-coded tickets after an order reaches
// TICKETED, renders them to MinIO/S3, and redeems them at the gate.
package ticketing

import (
	"crypto/hmac"
	"crypto/sha256"
)

// Signer is the boundary docs/plan.md draws for QR authenticity: an env
// HMAC key locally, KMS (Sign/Verify via the asymmetric or MAC API) in
// prod. Nothing else in this package knows or cares which.
type Signer interface {
	Sign(payload []byte) []byte
	Verify(payload, sig []byte) bool
}

// HMACSigner is the local implementation — cfg.WaitingRoomSecret's sibling
// for QR authenticity, a distinct key so rotating one never invalidates
// the other.
type HMACSigner struct {
	key []byte
}

func NewHMACSigner(key string) *HMACSigner {
	return &HMACSigner{key: []byte(key)}
}

func (s *HMACSigner) Sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

func (s *HMACSigner) Verify(payload, sig []byte) bool {
	return hmac.Equal(sig, s.Sign(payload))
}
