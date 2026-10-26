package graph

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursor_RoundTripIsLossless(t *testing.T) {
	in := Cursor{UpdatedAt: time.Date(2026, 3, 1, 12, 30, 45, 123456000, time.UTC), ID: uuid.New()}

	out, err := DecodeCursor(EncodeCursor(in))

	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !out.UpdatedAt.Equal(in.UpdatedAt) || out.ID != in.ID {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestCursor_IsOpaqueBase64(t *testing.T) {
	enc := EncodeCursor(Cursor{UpdatedAt: time.UnixMicro(1), ID: uuid.New()})

	if _, err := base64.RawURLEncoding.DecodeString(enc); err != nil {
		t.Fatalf("cursor %q is not URL-safe base64: %v", enc, err)
	}
}

func TestDecodeCursor_RejectsGarbage(t *testing.T) {
	notAUUID := base64.RawURLEncoding.EncodeToString([]byte("123|not-a-uuid"))
	notANumber := base64.RawURLEncoding.EncodeToString([]byte("abc|" + uuid.NewString()))
	noSeparator := base64.RawURLEncoding.EncodeToString([]byte("123"))

	for name, in := range map[string]string{
		"not base64":     "!!!",
		"empty":          "",
		"no separator":   noSeparator,
		"non numeric ts": notANumber,
		"non uuid id":    notAUUID,
	} {
		if _, err := DecodeCursor(in); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: err = %v, want ErrInvalidCursor", name, err)
		}
	}
}
