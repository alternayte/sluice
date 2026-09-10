//go:build integration

package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dbURL := pgtest.Shared(t).NewDatabase(t)
	cfg, err := LoadConfig(LoadOptions{Server: true, Env: map[string]string{
		"SLUICE_DATABASE_URL": dbURL,
		"SLUICE_PUBLIC_URL":   "http://127.0.0.1:8080",
	}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(context.Background(), cfg, logging.New(io.Discard, "error", "text"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Pool.Close)
	return s
}

type invOp struct {
	ID, Method, Path string
	Access           httpx.Access
}

func inventory(t *testing.T) []invOp {
	t.Helper()
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	registerRoutes(api, r, services{})
	var out []invOp
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			acc, ok := httpx.AccessOf(op)
			if !ok {
				t.Errorf("%s %s has no access", op.Method, path)
			}
			out = append(out, invOp{ID: op.OperationID, Method: op.Method, Path: path, Access: acc})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var paramRe = regexp.MustCompile(`\{[^}]+\}`)

// TestSCN_AUTH_006_RouteInventory calls every operation without authentication and with
// each role. The result must match the access of the operation (REQ-AUTH-006, SI-03):
//   - a run-token operation returns 401 without authentication and 401 with a user token (SI-04);
//   - a public operation returns neither 401 nor 403 without authentication;
//   - every other operation returns 401 without authentication, 403 for a role below its Min,
//     and neither 401 nor 403 for Min and higher roles.
func TestSCN_AUTH_006_RouteInventory(t *testing.T) {
	ops := inventory(t)
	if len(ops) < 55 {
		t.Fatalf("inventory has %d operations, want at least 55", len(ops))
	}
	s := testServer(t)
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := audit.WithActor(context.Background(), audit.Actor{Type: audit.ActorSystem})
	tokens := map[kernel.Role]string{}
	for _, role := range kernel.AllRoles {
		u, err := s.Auth.CreateUser(ctx, "inv-"+role.String()+"@example.com", "", role, "inventory-pass-1", false)
		if err != nil {
			t.Fatal(err)
		}
		secret, _, err := s.Auth.CreateToken(ctx, &kernel.Principal{UserID: u.ID, Email: u.Email, Role: role}, "inv", role, nil)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = secret
	}

	// A user with a temporary password can use only Self operations (SI-03).
	tmpUser, err := s.Auth.CreateUser(ctx, "inv-temp@example.com", "", kernel.Admin, "inventory-pass-1", true)
	if err != nil {
		t.Fatal(err)
	}
	tmpToken, _, err := s.Auth.CreateToken(ctx, &kernel.Principal{UserID: tmpUser.ID, Email: tmpUser.Email, Role: kernel.Admin}, "inv", kernel.Admin, nil)
	if err != nil {
		t.Fatal(err)
	}

	callRaw := func(method, path, token string) (int, string) {
		path = paramRe.ReplaceAllString(path, uuid.NewString())
		var body io.Reader
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
			body = bytes.NewReader([]byte("{}"))
		}
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b)
	}

	// Every GET operation is also called with HEAD. HEAD must get the same allow or deny
	// result as GET, as with the net/http ServeMux of the old server.
	for _, op := range ops {
		methods := []string{op.Method}
		if op.Method == http.MethodGet {
			methods = append(methods, http.MethodHead)
		}
		for _, method := range methods {
			checkOp(t, op, method, tokens, func(token string) int {
				code, _ := callRaw(method, op.Path, token)
				return code
			})
		}
	}

	// SI-03: a user who must change the password gets 403 password_change_required on an
	// operation that is not Self, and can use a Self operation.
	code, body := callRaw(http.MethodGet, "/api/v1/namespaces", tmpToken)
	if code != http.StatusForbidden || !strings.Contains(body, `"password_change_required"`) {
		t.Errorf("listNamespaces with a temporary password: %d %s, want 403 password_change_required", code, body)
	}
	if code, body := callRaw(http.MethodGet, "/api/v1/auth/me", tmpToken); code < 200 || code > 299 {
		t.Errorf("getMe with a temporary password: %d %s, want 2xx", code, body)
	}
}

// checkOp checks the result of one operation with one method against its access.
func checkOp(t *testing.T, op invOp, method string, tokens map[kernel.Role]string, call func(token string) int) {
	t.Helper()
	acc := op.Access
	if op.ID == "logout" {
		return // logout with a token only records an event
	}
	anon := call("")
	switch {
	case acc == httpx.RunToken:
		if anon != http.StatusUnauthorized {
			t.Errorf("%s %s: no run token returned %d, want 401", method, op.ID, anon)
		}
		for _, role := range kernel.AllRoles {
			if got := call(tokens[role]); got != http.StatusUnauthorized {
				t.Errorf("%s %s with a %s user token: %d, want 401", method, op.ID, role, got)
			}
		}
	case acc.Public:
		if anon == http.StatusUnauthorized || anon == http.StatusForbidden {
			t.Errorf("%s %s: public route returned %d without auth", method, op.ID, anon)
		}
	default:
		if anon != http.StatusUnauthorized {
			t.Errorf("%s %s: no auth returned %d, want 401", method, op.ID, anon)
		}
		for _, role := range kernel.AllRoles {
			got := call(tokens[role])
			if role >= acc.Min {
				if got == http.StatusForbidden || got == http.StatusUnauthorized {
					t.Errorf("%s %s as %s: %d, want allowed", method, op.ID, role, got)
				}
			} else if got != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d, want 403", method, op.ID, role, got)
			}
		}
	}
}

// TestErrorSchemaIsEnvelope checks that the spec describes errors in the wire form
// {"error":{"code","message","details"}} (REQ-API-002).
func TestErrorSchemaIsEnvelope(t *testing.T) {
	r := chi.NewMux()
	api := httpx.NewAPI(r)
	registerRoutes(api, r, services{})
	reg := api.OpenAPI().Components.Schemas
	checked := 0
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			for status, resp := range op.Responses {
				mt := resp.Content["application/json"]
				if (status != "default" && status < "400") || mt == nil || mt.Schema == nil {
					continue
				}
				s := mt.Schema
				if s.Ref != "" {
					s = reg.SchemaFromRef(s.Ref)
				}
				if s == nil || s.Properties["error"] == nil || !slices.Contains(s.Required, "error") {
					t.Errorf("%s %s: error response %s has no required error property", op.Method, path, status)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("spec has no error responses")
	}
}
