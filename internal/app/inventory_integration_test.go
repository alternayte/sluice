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

	call := func(op invOp, token string) int {
		path := paramRe.ReplaceAllString(op.Path, uuid.NewString())
		var body io.Reader
		if op.Method == http.MethodPost || op.Method == http.MethodPut || op.Method == http.MethodPatch {
			body = bytes.NewReader([]byte("{}"))
		}
		req, _ := http.NewRequest(op.Method, srv.URL+path, body)
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
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for _, op := range ops {
		acc := op.Access
		if op.ID == "logout" {
			continue // logout with a token only records an event
		}
		anon := call(op, "")
		switch {
		case acc == httpx.RunToken:
			if anon != http.StatusUnauthorized {
				t.Errorf("%s: no run token returned %d, want 401", op.ID, anon)
			}
			for _, role := range kernel.AllRoles {
				if got := call(op, tokens[role]); got != http.StatusUnauthorized {
					t.Errorf("%s with a %s user token: %d, want 401", op.ID, role, got)
				}
			}
		case acc.Public:
			if anon == http.StatusUnauthorized || anon == http.StatusForbidden {
				t.Errorf("%s: public route returned %d without auth", op.ID, anon)
			}
		default:
			if anon != http.StatusUnauthorized {
				t.Errorf("%s: no auth returned %d, want 401", op.ID, anon)
			}
			for _, role := range kernel.AllRoles {
				got := call(op, tokens[role])
				if role >= acc.Min {
					if got == http.StatusForbidden || got == http.StatusUnauthorized {
						t.Errorf("%s as %s: %d, want allowed", op.ID, role, got)
					}
				} else if got != http.StatusForbidden {
					t.Errorf("%s as %s: %d, want 403", op.ID, role, got)
				}
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
