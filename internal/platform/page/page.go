// Package page implements cursor pagination (REQ-API-003).
package page

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Limits of list endpoints.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Limit returns a valid page size.
func Limit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	if n > MaxLimit {
		return MaxLimit
	}
	return n
}

// LimitPtr returns a valid page size from an optional parameter.
func LimitPtr(n *int) int {
	if n == nil {
		return DefaultLimit
	}
	return Limit(*n)
}

// Encode returns an opaque cursor for a (time, id) key.
func Encode(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

// ErrBadCursor is returned for cursors that cannot be decoded.
var ErrBadCursor = errors.New("invalid cursor")

// Decode parses a cursor from Encode.
func Decode(c string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, badCursor()
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, uuid.Nil, badCursor()
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, badCursor()
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.Nil, badCursor()
	}
	return t, u, nil
}

func badCursor() error {
	return httpx.Validation(httpx.FieldError{Field: "cursor", Message: ErrBadCursor.Error()})
}

// EncodeStrings returns an opaque cursor for a key of strings.
func EncodeStrings(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x00")))
}

// DecodeStrings parses a cursor from EncodeStrings with n parts.
func DecodeStrings(c string, n int) ([]string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, badCursor()
	}
	parts := strings.Split(string(b), "\x00")
	if len(parts) != n {
		return nil, badCursor()
	}
	return parts, nil
}

// DecodeOpt decodes an optional cursor. An empty cursor gives nil values.
func DecodeOpt(c string) (*time.Time, *uuid.UUID, error) {
	if c == "" {
		return nil, nil, nil
	}
	t, id, err := Decode(c)
	if err != nil {
		return nil, nil, err
	}
	return &t, &id, nil
}

// DecodePtr decodes an optional cursor. It returns nil values for no cursor.
func DecodePtr(c *string) (*time.Time, *uuid.UUID, error) {
	if c == nil || *c == "" {
		return nil, nil, nil
	}
	t, id, err := Decode(*c)
	if err != nil {
		return nil, nil, err
	}
	return &t, &id, nil
}
