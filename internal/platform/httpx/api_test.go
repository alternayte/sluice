package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

type createIn struct {
	Body struct {
		Email    string `json:"email" format:"email"`
		Role     string `json:"role" enum:"viewer,operator,editor,admin"`
		Password string `json:"password" minLength:"10"`
	}
}

type okOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	huma.Register(api, httpx.Op("create", http.MethodPost, "/api/v1/things", httpx.MinRole(kernel.Admin)),
		func(context.Context, *createIn) (*okOut, error) {
			out := &okOut{}
			out.Body.OK = true
			return out, nil
		})
	huma.Register(api, httpx.Op("limited", http.MethodGet, "/api/v1/limited", httpx.Public),
		func(context.Context, *struct{}) (*okOut, error) {
			return nil, &httpx.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "slow down", RetryAfter: 7}
		})
	huma.Register(api, httpx.Op("boom", http.MethodGet, "/api/v1/boom", httpx.Public),
		func(context.Context, *struct{}) (*okOut, error) { return nil, errors.New("secret database detail") })
	httpx.Raw(api, r, httpx.Op("stream", http.MethodGet, "/api/v1/stream/{id}", httpx.MinRole(kernel.Viewer)),
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = io.WriteString(w, "raw:"+chi.URLParam(req, "id"))
		})
	r.Handle("/api/*", httpx.NotFoundJSON())
	if err := httpx.CheckAccess(api); err != nil {
		t.Fatal(err)
	}
	return r
}

type result struct {
	status int
	header http.Header
	body   string
	env    struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Field string `json:"field"`
			} `json:"details"`
		} `json:"error"`
	}
}

func do(t *testing.T, h http.Handler, method, path, body string, role kernel.Role) result {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if role != kernel.RoleNone {
		req = req.WithContext(kernel.WithPrincipal(req.Context(), &kernel.Principal{Role: role}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := result{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
	_ = json.Unmarshal(rec.Body.Bytes(), &res.env)
	return res
}

func TestAPIContract(t *testing.T) {
	h := testHandler(t)
	bad := `{"email":"x","role":"king","password":"short"}`

	t.Run("validation lists each field", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", bad, kernel.Admin)
		fields := map[string]bool{}
		for _, d := range r.env.Error.Details {
			fields[d.Field] = true
		}
		if r.status != http.StatusUnprocessableEntity || r.env.Error.Code != "validation_failed" ||
			!fields["email"] || !fields["role"] || !fields["password"] {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("no caller gets 401 before validation", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", bad, kernel.RoleNone)
		if r.status != http.StatusUnauthorized || r.env.Error.Code != "unauthorized" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("low role gets 403", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", bad, kernel.Viewer)
		if r.status != http.StatusForbidden || r.env.Error.Code != "forbidden" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("success body has no extra fields", func(t *testing.T) {
		r := do(t, h, http.MethodPost, "/api/v1/things", `{"email":"a@b.co","role":"admin","password":"long-enough-1"}`, kernel.Admin)
		if r.status != http.StatusOK || strings.TrimSpace(r.body) != `{"ok":true}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("error keeps code and Retry-After", func(t *testing.T) {
		r := do(t, h, http.MethodGet, "/api/v1/limited", "", kernel.RoleNone)
		if r.status != http.StatusTooManyRequests || r.env.Error.Code != "rate_limited" || r.header.Get("Retry-After") != "7" ||
			r.header.Get("Content-Type") != "application/json" {
			t.Fatalf("%d %v %s", r.status, r.header, r.body)
		}
	})
	t.Run("unknown error hides detail", func(t *testing.T) {
		r := do(t, h, http.MethodGet, "/api/v1/boom", "", kernel.RoleNone)
		if r.status != http.StatusInternalServerError || r.env.Error.Code != "internal" || strings.Contains(r.body, "secret") {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
	t.Run("raw route checks access", func(t *testing.T) {
		if r := do(t, h, http.MethodGet, "/api/v1/stream/42", "", kernel.RoleNone); r.status != http.StatusUnauthorized {
			t.Fatalf("anonymous: %d", r.status)
		}
		if r := do(t, h, http.MethodGet, "/api/v1/stream/42", "", kernel.Viewer); r.status != http.StatusOK || r.body != "raw:42" {
			t.Fatalf("viewer: %d %s", r.status, r.body)
		}
	})
	t.Run("unknown API route is JSON 404", func(t *testing.T) {
		r := do(t, h, http.MethodGet, "/api/v1/nothing", "", kernel.Admin)
		if r.status != http.StatusNotFound || r.env.Error.Code != "not_found" {
			t.Fatalf("%d %s", r.status, r.body)
		}
	})
}

func TestCheckAccessFindsOperationWithoutAccess(t *testing.T) {
	api := httpx.NewAPI(chi.NewMux())
	huma.Register(api, huma.Operation{OperationID: "open", Method: http.MethodGet, Path: "/api/v1/open"},
		func(context.Context, *struct{}) (*okOut, error) { return &okOut{}, nil })
	if err := httpx.CheckAccess(api); err == nil || !strings.Contains(err.Error(), "/api/v1/open") {
		t.Fatalf("want error that names /api/v1/open, got %v", err)
	}
}
