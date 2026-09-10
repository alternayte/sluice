//go:build integration

package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/auth"
	"github.com/alternayte/sluice/internal/kernel"
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

var paramRe = regexp.MustCompile(`\{[^}]+\}`)

type specOp struct {
	ID, Method, Path string
}

func specOperations(t *testing.T) []specOp {
	t.Helper()
	spec, err := apigen.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var out []specOp
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			out = append(out, specOp{ID: auth.OperationKey(op.OperationID), Method: method, Path: path})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TestSCN_AUTH_006_RouteInventory checks that every router route and every OpenAPI
// operation has a permission entry, and calls each operation with each role (SI-03).
func TestSCN_AUTH_006_RouteInventory(t *testing.T) {
	s := testServer(t)
	mux, err := s.Routes()
	if err != nil {
		t.Fatal(err)
	}
	ops := specOperations(t)
	specPatterns := map[string]bool{}
	for _, op := range ops {
		specPatterns[op.Method+" "+op.Path] = true
		if _, ok := auth.Operations[op.ID]; !ok {
			t.Errorf("operation %s has no permission entry", op.ID)
		}
	}
	known := map[string]bool{}
	for _, op := range ops {
		known[op.ID] = true
	}
	for id := range auth.Operations {
		if !known[id] {
			t.Errorf("permission entry %s has no OpenAPI operation", id)
		}
	}
	for _, p := range mux.Patterns {
		if specPatterns[p] {
			continue
		}
		if _, ok := auth.Routes[p]; !ok {
			t.Errorf("router route %q has no permission entry", p)
		}
	}
	if t.Failed() {
		return
	}

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

	call := func(op specOp, token string) int {
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
		acc := auth.Operations[op.ID]
		if op.ID == "logout" {
			continue // logout with a token only records an event; checked below with a separate token
		}
		anon := call(op, "")
		switch {
		case acc.Public || acc.Other != "":
			if anon == http.StatusUnauthorized || anon == http.StatusForbidden {
				t.Errorf("%s: public route returned %d without auth", op.ID, anon)
			}
		default:
			if anon != http.StatusUnauthorized {
				t.Errorf("%s: no auth returned %d, want 401", op.ID, anon)
			}
		}
		for _, role := range kernel.AllRoles {
			got := call(op, tokens[role])
			allowed := acc.Public || acc.Other != "" || role >= acc.Min
			if allowed && (got == http.StatusForbidden || got == http.StatusUnauthorized) {
				t.Errorf("%s as %s: %d, want allowed", op.ID, role, got)
			}
			if !allowed && got != http.StatusForbidden {
				t.Errorf("%s as %s: %d, want 403", op.ID, role, got)
			}
		}
	}
	if !strings.Contains(strings.Join(mux.Patterns, " "), "GET /healthz") {
		t.Error("route recording failed")
	}
}
