package webhook

import (
	"errors"
	"testing"
	"time"
)

const (
	vectorSecret    = "s3cret"
	vectorTimestamp = "1700000000"
	vectorDelivery  = "dlv-1"
	vectorBody      = `{"a":1}`
	vectorSignature = "sha256=4115fa7ca537e9209469446d5e3b025790a8740dceff8a6a4fbe7059313b4b8a"
)

func fixedClock(secs int64) func() time.Time {
	return func() time.Time { return time.Unix(secs, 0) }
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name       string
		secret     string
		timestamp  string
		deliveryID string
		signature  string
		body       string
		now        int64
		want       error
	}{
		{"valid, matches openssl vector", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature, vectorBody, 1700000000, nil},
		{"valid at the edge of the window", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature, vectorBody, 1700000300, nil},
		{"wrong secret", "other", vectorTimestamp, vectorDelivery, vectorSignature, vectorBody, 1700000000, ErrBadSignature},
		{"tampered body", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature, `{"a":2}`, 1700000000, ErrBadSignature},
		{"reformatted body is a different MAC", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature, `{"a": 1}`, 1700000000, ErrBadSignature},
		{"timestamp swapped after signing", vectorSecret, "1700000001", vectorDelivery, vectorSignature, vectorBody, 1700000001, ErrBadSignature},
		{"delivery id swapped after signing", vectorSecret, vectorTimestamp, "dlv-2", vectorSignature, vectorBody, 1700000000, ErrBadSignature},
		{"delivery id containing the separator", vectorSecret, vectorTimestamp, "dlv.1", vectorSignature, vectorBody, 1700000000, ErrMalformedHeaders},
		{"too old", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature, vectorBody, 1700000301, ErrStaleTimestamp},
		{"too far in the future", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature, vectorBody, 1699999699, ErrStaleTimestamp},
		{"missing signature", vectorSecret, vectorTimestamp, vectorDelivery, "", vectorBody, 1700000000, ErrMalformedHeaders},
		{"missing timestamp", vectorSecret, "", vectorDelivery, vectorSignature, vectorBody, 1700000000, ErrMalformedHeaders},
		{"non-numeric timestamp", vectorSecret, "yesterday", vectorDelivery, vectorSignature, vectorBody, 1700000000, ErrMalformedHeaders},
		{"signature without prefix", vectorSecret, vectorTimestamp, vectorDelivery, vectorSignature[len(signaturePrefix):], vectorBody, 1700000000, ErrMalformedHeaders},
		{"signature not hex", vectorSecret, vectorTimestamp, vectorDelivery, "sha256=zz", vectorBody, 1700000000, ErrMalformedHeaders},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVerifier(tt.secret, 5*time.Minute, fixedClock(tt.now))

			err := v.Verify(tt.timestamp, tt.deliveryID, tt.signature, []byte(tt.body))

			if !errors.Is(err, tt.want) {
				t.Errorf("Verify() = %v, want %v", err, tt.want)
			}
		})
	}
}
