package graph

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidCursor = errors.New("invalid cursor")

// Microsecond precision matches what timestamptz stores, so a round trip is lossless.
func EncodeCursor(c Cursor) string {
	raw := fmt.Sprintf("%d|%s", c.UpdatedAt.UnixMicro(), c.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	micros, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return Cursor{}, ErrInvalidCursor
	}
	us, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{UpdatedAt: time.UnixMicro(us).UTC(), ID: parsed}, nil
}
