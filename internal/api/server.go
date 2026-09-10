// Package api wires the generated OpenAPI server (D-09) to the feature handlers.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// AuthorizeFunc checks the permission of an operation. It returns nil when allowed.
type AuthorizeFunc func(ctx context.Context, operationID string) error

// Mount registers all /api routes on mux. Unknown /api routes return JSON 404 (REQ-API-002).
// authorize runs before request validation, so a caller without permission never sees
// validation details.
func Mount(mux ServeMux, ssi apigen.StrictServerInterface, authorize AuthorizeFunc, mws []apigen.StrictMiddlewareFunc, httpMws ...apigen.MiddlewareFunc) error {
	validator, err := NewValidator()
	if err != nil {
		return err
	}
	validator.authorize = authorize
	strict := apigen.NewStrictHandlerWithOptions(ssi, mws, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			httpx.WriteError(w, r, httpx.Errorf(http.StatusBadRequest, "bad_request", "%s", err.Error()))
		},
		ResponseErrorHandlerFunc: httpx.WriteError,
	})
	apigen.HandlerWithOptions(strict, apigen.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: append([]apigen.MiddlewareFunc{validator.Middleware}, httpMws...),
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			httpx.WriteError(w, r, httpx.Validation(httpx.FieldError{Field: "", Message: err.Error()}))
		},
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.Errorf(http.StatusNotFound, "not_found", "unknown API route %s %s", r.Method, r.URL.Path))
	})
	return nil
}

// ServeMux is the router that Mount registers on.
type ServeMux interface {
	apigen.ServeMux
	Handle(pattern string, h http.Handler)
}

// Validator checks requests against the OpenAPI document.
type Validator struct {
	router    routers.Router
	authorize AuthorizeFunc
}

// OperationID returns the operation of a request, or "" when no operation matches.
func (v *Validator) OperationID(r *http.Request) string {
	route, _, err := v.router.FindRoute(r)
	if err != nil || route.Operation == nil {
		return ""
	}
	return route.Operation.OperationID
}

func init() {
	openapi3.DefineStringFormatValidator("email", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForEmail))
}

// NewValidator loads the embedded OpenAPI document.
func NewValidator() (*Validator, error) {
	spec, err := apigen.GetSpec()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil
	router, err := legacy.NewRouter(spec)
	if err != nil {
		return nil, err
	}
	return &Validator{router: router}, nil
}

// Middleware validates parameters and JSON bodies. Failures return 422 validation_failed
// with field details (REQ-API-002).
func (v *Validator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, params, err := v.router.FindRoute(r)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if v.authorize != nil {
			if err := v.authorize(r.Context(), route.Operation.OperationID); err != nil {
				httpx.WriteError(w, r, err)
				return
			}
		}
		ct := r.Header.Get("Content-Type")
		if r.Body != nil && r.ContentLength != 0 && !strings.HasPrefix(ct, "application/json") && ct != "" {
			// Streams (artifacts, uploads) are not validated here.
			next.ServeHTTP(w, r)
			return
		}
		in := &openapi3filter.RequestValidationInput{
			Request:    r,
			PathParams: params,
			Route:      route,
			Options: &openapi3filter.Options{
				MultiError:         true,
				AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil },
			},
		}
		if err := openapi3filter.ValidateRequest(r.Context(), in); err != nil {
			httpx.WriteError(w, r, httpx.Validation(fieldErrors(err)...))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func fieldErrors(err error) []httpx.FieldError {
	var out []httpx.FieldError
	var walk func(error)
	walk = func(err error) {
		var me openapi3.MultiError
		if errors.As(err, &me) {
			for _, e := range me {
				walk(e)
			}
			return
		}
		var re *openapi3filter.RequestError
		if errors.As(err, &re) {
			var inner openapi3.MultiError
			if errors.As(re.Err, &inner) {
				for _, e := range inner {
					walk(&openapi3filter.RequestError{Input: re.Input, Parameter: re.Parameter, RequestBody: re.RequestBody, Err: e})
				}
				return
			}
			var se *openapi3.SchemaError
			if errors.As(re.Err, &se) {
				field := strings.Join(se.JSONPointer(), ".")
				if re.Parameter != nil {
					field = re.Parameter.Name
				}
				out = append(out, httpx.FieldError{Field: field, Message: se.Reason})
				return
			}
			field := "body"
			if re.Parameter != nil {
				field = re.Parameter.Name
			}
			msg := re.Reason
			if msg == "" && re.Err != nil {
				msg = re.Err.Error()
			}
			var syn *json.SyntaxError
			if errors.As(re.Err, &syn) {
				msg = "invalid JSON"
			}
			out = append(out, httpx.FieldError{Field: field, Message: msg})
			return
		}
		var se *openapi3.SchemaError
		if errors.As(err, &se) {
			out = append(out, httpx.FieldError{Field: strings.Join(se.JSONPointer(), "."), Message: se.Reason})
			return
		}
		out = append(out, httpx.FieldError{Field: "", Message: err.Error()})
	}
	walk(err)
	return out
}
