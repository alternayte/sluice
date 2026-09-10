package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/logging"
)

// Access is the permission of one operation (Appendix B, SI-03).
type Access struct {
	// Public operations need no authentication.
	Public bool
	// Min is the lowest role that can call the operation.
	Min kernel.Role
	// Self operations are allowed while the user must change the password.
	Self bool
	// Other names the credential that the operation checks itself, for example a run token.
	Other string
}

// Access values that many operations use.
var (
	Public        = Access{Public: true}
	Authenticated = Access{Min: kernel.Viewer, Self: true}
	RunToken      = Access{Other: "run token of the task run (SI-04)"}
)

// MinRole returns the access for role r and higher roles.
func MinRole(r kernel.Role) Access { return Access{Min: r} }

const accessKey = "sluice:access"

// Op returns an operation with its access. Every operation uses Op (D-23).
func Op(id, method, path string, acc Access) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Metadata: map[string]any{accessKey: acc}}
}

// AccessOf returns the access of an operation.
func AccessOf(op *huma.Operation) (Access, bool) {
	if op == nil || op.Metadata == nil {
		return Access{}, false
	}
	a, ok := op.Metadata[accessKey].(Access)
	return a, ok
}

// ErrPasswordChange blocks all operations except self operations until the user sets a new password.
var ErrPasswordChange = Errorf(http.StatusForbidden, "password_change_required", "change your password first")

// Check returns nil when the caller in ctx can use an operation with access acc.
// An access with Min at kernel.RoleNone and no other grant is a configuration error, not a
// grant to every caller: Check denies it (default deny, D-23).
func Check(ctx context.Context, acc Access) error {
	if acc.Public || acc.Other != "" {
		return nil
	}
	if acc.Min == kernel.RoleNone {
		return ErrForbidden
	}
	p := kernel.FromContext(ctx)
	if p == nil {
		return ErrUnauthorized
	}
	if p.MustChangePassword && !acc.Self {
		return ErrPasswordChange
	}
	if !p.Can(acc.Min) {
		return ErrForbidden
	}
	return nil
}

func init() {
	huma.NewError = newError
	huma.NewErrorWithContext = newErrorWithContext
	// api/openapi.yaml never allowed null for an array, and the handlers send [] for an empty list.
	huma.DefaultArrayNullable = false
}

// NewAPI creates the huma API on r. It serves no spec and no docs: `sluice openapi` prints the spec.
// The access check runs before huma reads the request, so a caller without permission
// never sees validation details (SI-03).
func NewAPI(r *chi.Mux) huma.API {
	cfg := huma.DefaultConfig("Sluice API", "1")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	// No $schema field in bodies and no Link header: the JSON contract stays as it is.
	cfg.CreateHooks = nil
	// api/openapi.yaml never sets additionalProperties: false, and the old validator accepted
	// unknown body properties. Keep that contract: a newer client or runner can send a new field.
	cfg.AllowAdditionalPropertiesByDefault = true
	api := humachi.New(r, cfg)
	// huma describes an error response with the fields of Error. The wire form is the envelope.
	api.OpenAPI().Components.Schemas.RegisterTypeAlias(reflect.TypeFor[Error](), reflect.TypeFor[ErrorEnvelope]())
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		acc, ok := AccessOf(ctx.Operation())
		if !ok {
			writeHuma(ctx, ErrForbidden)
			return
		}
		if err := Check(ctx.Context(), acc); err != nil {
			writeHuma(ctx, err)
			return
		}
		next(ctx)
	})
	return api
}

func writeHuma(ctx huma.Context, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.ErrorContext(ctx.Context(), "request failed", "err", err)
		e = errInternal
	}
	if e.RetryAfter > 0 {
		ctx.SetHeader("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(e.Status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(e)
}

// newError replaces huma.NewError for callers without a request context. It keeps the Sluice
// envelope and codes (REQ-API-002).
func newError(status int, msg string, errs ...error) huma.StatusError {
	if status >= http.StatusInternalServerError {
		slog.Error("request failed", "status", status, "err", msg, "cause", causesOf(errs))
		return errInternal
	}
	return errorFor(status, msg, errs)
}

// newErrorWithContext replaces huma.NewErrorWithContext. A handler error reaches huma as
// NewErrorWithContext(ctx, 500, "unexpected error occurred", err): logging it through
// logging.From(ctx.Context()) keeps the real cause and the request ID (REQ-CORE-009), while the
// response body still hides the cause behind errInternal.
func newErrorWithContext(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
	if status >= http.StatusInternalServerError {
		logging.From(ctx.Context()).Error("request failed", "status", status, "err", msg, "cause", causesOf(errs))
		return errInternal
	}
	return errorFor(status, msg, errs)
}

// causesOf renders errs for a log line without leaking them into the response.
func causesOf(errs []error) string {
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// errorFor builds the Sluice error for a non-5xx status (REQ-API-002).
func errorFor(status int, msg string, errs []error) huma.StatusError {
	if status == http.StatusUnprocessableEntity || (status == http.StatusBadRequest && len(errs) > 0) {
		fields := make([]FieldError, 0, len(errs))
		for _, err := range errs {
			var d *huma.ErrorDetail
			if errors.As(err, &d) {
				fields = append(fields, bodyFieldError(d.Location, d.Message))
				continue
			}
			fields = append(fields, FieldError{Message: err.Error()})
		}
		return Validation(fields...)
	}
	return &Error{Status: status, Code: codeFor(status), Message: msg}
}

// bodyFieldError turns a huma location such as body.email, query.limit or path.userId, together
// with its message, into a FieldError.
//
// A missing required top-level property reaches this function with the bare location "body" and
// the property name only in the message ("expected required property x to be present"): this
// function reads the name from the message so the field still names the property, as the old
// kin-openapi validator did.
//
// A malformed JSON body also reaches this function with the bare location "body", since huma
// parses the raw body before it runs schema validation and reports the parse failure the same
// way. Its message is the raw encoding/json parser text, for example "invalid character 'b'
// looking for beginning of object key string", and must not reach the caller (D-23, no internal
// detail in the response): this function replaces it with "invalid JSON" for every body-location
// message that is not one of the known schema-validation messages (REQ-API-002).
func bodyFieldError(loc, msg string) FieldError {
	if _, rest, ok := strings.Cut(loc, "."); ok {
		return FieldError{Field: rest, Message: msg}
	}
	if loc == "body" {
		if name, ok := requiredPropertyName(msg); ok {
			return FieldError{Field: name, Message: msg}
		}
		if !isSchemaBodyMessage(msg) {
			return FieldError{Field: "body", Message: "invalid JSON"}
		}
	}
	return FieldError{Field: loc, Message: msg}
}

// requiredPropertyName extracts x from "expected required property x to be present".
func requiredPropertyName(msg string) (string, bool) {
	const prefix = "expected required property "
	const suffix = " to be present"
	if !strings.HasPrefix(msg, prefix) || !strings.HasSuffix(msg, suffix) {
		return "", false
	}
	return msg[len(prefix) : len(msg)-len(suffix)], true
}

// schemaBodyMessagePrefixes lists every huma schema-validation message that can carry the bare
// "body" location, other than the required-property message (handled separately). Any other
// message at that location comes from the JSON parser, not the schema validator.
var schemaBodyMessagePrefixes = []string{
	"unexpected property",
	"expected object",
	"expected property ", // dependent required property
	"expected schema $ref to resolve",
}

// isSchemaBodyMessage reports whether msg is one of huma's schema-validation messages for a
// top-level object, rather than a JSON parser message.
func isSchemaBodyMessage(msg string) bool {
	for _, p := range schemaBodyMessagePrefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	}
	return strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
}

// CheckAccess returns an error that names each operation without access, and each operation
// whose access is the zero value, since a zero Access grants every logged-in caller
// instead of denying by default (D-23).
func CheckAccess(api huma.API) error {
	var missing []string
	var empty []string
	for path, item := range api.OpenAPI().Paths {
		ops := []*huma.Operation{
			item.Get, item.Put, item.Post, item.Delete, item.Patch, item.Head, item.Options, item.Trace,
		}
		for _, op := range ops {
			if op == nil {
				continue
			}
			acc, ok := AccessOf(op)
			if !ok {
				missing = append(missing, op.Method+" "+path)
				continue
			}
			if acc == (Access{}) {
				empty = append(empty, op.Method+" "+path)
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(empty)
	var msgs []string
	if len(missing) > 0 {
		msgs = append(msgs, fmt.Sprintf("operations without access: %s", strings.Join(missing, ", ")))
	}
	if len(empty) > 0 {
		msgs = append(msgs, fmt.Sprintf("operations with an empty access: %s", strings.Join(empty, ", ")))
	}
	if len(msgs) > 0 {
		return errors.New(strings.Join(msgs, "; "))
	}
	return nil
}

// Raw adds an operation to the spec and serves it with a plain handler behind the access check.
// Use it only for bodies that must not be buffered: file and artifact transfer, bundles and SSE (DI-23).
func Raw(api huma.API, r chi.Router, op huma.Operation, h http.HandlerFunc) {
	acc, ok := AccessOf(&op)
	if !ok {
		panic("httpx.Raw: operation " + op.OperationID + " has no access")
	}
	if op.Responses == nil {
		op.Responses = RawResponse(http.StatusOK, "application/octet-stream", "Content.")
	}
	api.OpenAPI().AddOperation(&op)
	r.Method(op.Method, op.Path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := Check(req.Context(), acc); err != nil {
			WriteError(w, req, err)
			return
		}
		h(w, req)
	}))
}

// PathParams describes string path parameters for a Raw operation.
func PathParams(names ...string) []*huma.Param {
	out := make([]*huma.Param, 0, len(names))
	for _, n := range names {
		out = append(out, &huma.Param{Name: n, In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString}})
	}
	return out
}

// RawResponse describes the success response of a Raw operation.
func RawResponse(status int, contentType, description string) map[string]*huma.Response {
	resp := &huma.Response{Description: description}
	if contentType != "" {
		resp.Content = map[string]*huma.MediaType{contentType: {}}
	}
	return map[string]*huma.Response{strconv.Itoa(status): resp}
}

// NotFoundJSON answers unknown API routes with a JSON 404 (REQ-API-002).
func NotFoundJSON() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, Errorf(http.StatusNotFound, "not_found", "unknown API route %s %s", r.Method, r.URL.Path))
	})
}
