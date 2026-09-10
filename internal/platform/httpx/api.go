package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
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
func Check(ctx context.Context, acc Access) error {
	if acc.Public || acc.Other != "" {
		return nil
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

func init() { huma.NewError = newError }

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
	api := humachi.New(r, cfg)
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

// newError replaces huma.NewError. It keeps the Sluice envelope and codes (REQ-API-002).
func newError(status int, msg string, errs ...error) huma.StatusError {
	switch {
	case status >= http.StatusInternalServerError:
		slog.Error("request failed", "status", status, "err", msg)
		return errInternal
	case status == http.StatusUnprocessableEntity || (status == http.StatusBadRequest && len(errs) > 0):
		fields := make([]FieldError, 0, len(errs))
		for _, err := range errs {
			var d *huma.ErrorDetail
			if errors.As(err, &d) {
				fields = append(fields, FieldError{Field: fieldName(d.Location), Message: d.Message})
				continue
			}
			fields = append(fields, FieldError{Message: err.Error()})
		}
		return Validation(fields...)
	}
	return &Error{Status: status, Code: codeFor(status), Message: msg}
}

// fieldName turns a huma location such as body.email, query.limit or path.userId into the field name.
func fieldName(loc string) string {
	if _, rest, ok := strings.Cut(loc, "."); ok {
		return rest
	}
	return loc
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

// CheckAccess returns an error that names each operation without access (default deny, D-23).
func CheckAccess(api huma.API) error {
	var missing []string
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch, item.Head, item.Options} {
			if op == nil {
				continue
			}
			if _, ok := AccessOf(op); !ok {
				missing = append(missing, op.Method+" "+path)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("operations without access: %s", strings.Join(missing, ", "))
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
