// Package httpx holds HTTP helpers: the error envelope, JSON writing and request IDs.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/platform/logging"
)

// Error is an API error with the envelope {"error":{"code","message","details"}} (REQ-API-002).
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
	// RetryAfter sets the Retry-After header in seconds when > 0.
	RetryAfter int `json:"-"`
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// MarshalJSON writes the envelope {"error":{"code","message","details"}} (REQ-API-002).
func (e *Error) MarshalJSON() ([]byte, error) {
	type body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	}
	return json.Marshal(struct {
		Error body `json:"error"`
	}{body{Code: e.Code, Message: e.Message, Details: e.Details}})
}

// GetStatus returns the HTTP status. huma uses it.
func (e *Error) GetStatus() int { return e.Status }

// GetHeaders returns Retry-After when it is set. huma uses it.
func (e *Error) GetHeaders() http.Header {
	h := http.Header{}
	if e.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	return h
}

// ContentType keeps application/json for errors. huma uses it.
func (e *Error) ContentType(string) string { return "application/json" }

// Errorf creates an API error.
func Errorf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithDetails returns a copy of e with details.
func (e *Error) WithDetails(d any) *Error {
	c := *e
	c.Details = d
	return &c
}

// Common errors.
var (
	ErrUnauthorized = &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "authentication required"}
	ErrForbidden    = &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "permission denied"}
	ErrNotFound     = &Error{Status: http.StatusNotFound, Code: "not_found", Message: "not found"}
)

// FieldError is one entry of validation_failed details.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Validation returns a 422 validation_failed error with field details.
func Validation(fields ...FieldError) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "validation failed", Details: fields}
}

// WriteJSON writes v as JSON with the status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes err as the error envelope. Unknown errors become 500 internal.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		logging.From(r.Context()).Error("request failed", "err", err)
		e = errInternal
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	WriteJSON(w, e.Status, e)
}

var errInternal = &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error"}

type requestIDKey struct{}

// RequestID returns the request ID in the context.
func RequestID(ctx context.Context) string {
	s, _ := ctx.Value(requestIDKey{}).(string)
	return s
}

// WithRequestID adds a request ID to each request, returns it in X-Request-Id and
// adds it to the context logger (REQ-CORE-009).
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		ctx = logging.With(ctx, logging.From(ctx).With("request_id", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// StatusRecorder records the response status.
type StatusRecorder struct {
	http.ResponseWriter
	Status int
}

// WriteHeader records the status.
func (s *StatusRecorder) WriteHeader(code int) {
	if s.Status == 0 {
		s.Status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// Write sets the status to 200 when no status was written.
func (s *StatusRecorder) Write(b []byte) (int, error) {
	if s.Status == 0 {
		s.Status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer for SSE.
func (s *StatusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying writer for http.ResponseController.
func (s *StatusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// ClientIP returns the remote IP without port.
func ClientIP(r *http.Request) string {
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			host = host[:i]
			break
		}
	}
	if len(host) > 1 && host[0] == '[' {
		host = host[1 : len(host)-1]
	}
	return host
}
