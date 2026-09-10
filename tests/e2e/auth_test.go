//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSCN_AUTH_002_BootstrapAdminAndCLI(t *testing.T) {
	dbURL := newDatabase(t)
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	login(t, p.URL, adminEmail, adminPassword)
	if err := p.Stop(syscall.SIGTERM); err != nil {
		t.Logf("stop: %v", err)
	}

	p2 := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL, "SLUICE_BOOTSTRAP_ADMIN_PASSWORD": "another-password-9"})
	login(t, p2.URL, adminEmail, adminPassword)
	r := newCookieClient(p2.URL).raw(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": adminEmail, "password": "another-password-9"}, nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("restart changed the admin password: status %d", r.Status)
	}

	out, stderr, code := runCLI(t, map[string]string{"SLUICE_DATABASE_URL": dbURL},
		"user", "create", "--email", "cli-editor@example.com", "--role", "editor", "--password", "cli-password-1")
	if code != 0 {
		t.Fatalf("user create exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "editor") {
		t.Fatalf("user create output: %s", out)
	}
	var me struct {
		Role string `json:"role"`
	}
	login(t, p2.URL, "cli-editor@example.com", "cli-password-1").do(t, http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, &me)
	if me.Role != "editor" {
		t.Fatalf("role %s", me.Role)
	}

	_, stderr, code = runCLI(t, map[string]string{"SLUICE_DATABASE_URL": dbURL},
		"user", "reset-password", "--email", "cli-editor@example.com", "--password", "cli-password-2")
	if code != 0 {
		t.Fatalf("reset-password exit %d: %s", code, stderr)
	}
	login(t, p2.URL, "cli-editor@example.com", "cli-password-2")
}

func TestSCN_AUTH_004_LastAdminGuard(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	var me struct {
		ID string `json:"id"`
	}
	admin.do(t, http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, &me)
	for _, body := range []map[string]any{{"disabled": true}, {"role": "editor"}} {
		r := admin.raw(t, http.MethodPatch, "/api/v1/users/"+me.ID, body, nil)
		if r.Status != http.StatusConflict || errCode(r.Body) != "last_admin" {
			t.Fatalf("%v: status %d %s", body, r.Status, r.Body)
		}
	}
	// With a second admin, demoting the first works.
	id2, _ := createUser(t, admin, "admin2@example.com", "admin", "admin2-password-1")
	admin.do(t, http.MethodPatch, "/api/v1/users/"+id2, map[string]any{"disabled": true}, http.StatusOK, nil)
	r := admin.raw(t, http.MethodPatch, "/api/v1/users/"+me.ID, map[string]any{"role": "viewer"}, nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("demote with the only other admin disabled: %d", r.Status)
	}
	admin.do(t, http.MethodPatch, "/api/v1/users/"+id2, map[string]any{"disabled": false}, http.StatusOK, nil)
	admin.do(t, http.MethodPatch, "/api/v1/users/"+me.ID, map[string]any{"role": "viewer"}, http.StatusOK, nil)
}

func TestSCN_AUTH_007_ChangePassword(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	_, _ = createUser(t, admin, "user7@example.com", "editor", "first-password-1")
	a := login(t, p.URL, "user7@example.com", "first-password-1")
	b := login(t, p.URL, "user7@example.com", "first-password-1")

	r := a.raw(t, http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": "wrong-password-1", "new_password": "second-password-1"}, nil)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong current password: %d %s", r.Status, r.Body)
	}
	a.do(t, http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": "first-password-1", "new_password": "second-password-1"}, http.StatusNoContent, nil)
	if r := b.raw(t, http.MethodGet, "/api/v1/auth/me", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("other session: %d", r.Status)
	}
	a.do(t, http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, nil)
	login(t, p.URL, "user7@example.com", "second-password-1")
}

func TestSCN_AUTH_008_LoginRateLimit(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := newCookieClient(p.URL)
	for i := 1; i <= 10; i++ {
		r := c.raw(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": adminEmail, "password": "wrong-" + strconv.Itoa(i)}, nil)
		if r.Status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, r.Status)
		}
	}
	r := c.raw(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": adminEmail, "password": adminPassword}, nil)
	if r.Status != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: %d %s", r.Status, r.Body)
	}
	ra, err := strconv.Atoi(r.Header.Get("Retry-After"))
	if err != nil || ra < 1 || ra > 900 {
		t.Fatalf("Retry-After %q", r.Header.Get("Retry-After"))
	}
	// Another email from the same IP is not limited by the email limit.
	r = c.raw(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "nobody@example.com", "password": "x"}, nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("other email: %d", r.Status)
	}
}

func TestSCN_AUTH_009_DisableTakesEffectOnAllInstances(t *testing.T) {
	dbURL := newDatabase(t)
	a := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	b := startServer(t, map[string]string{"SLUICE_DATABASE_URL": dbURL})
	admin := adminClient(t, a)
	id, sess := createUser(t, admin, "user9@example.com", "operator", "user9-password-1")
	secret, _ := createToken(t, sess, "operator")
	// The cookie jar is per host, so log in on B as well.
	sessB := login(t, b.URL, "user9@example.com", "user9-password-1")
	tokB := tokenClient(b.URL, secret)
	sessB.do(t, http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, nil)
	tokB.do(t, http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, nil)

	admin.do(t, http.MethodPatch, "/api/v1/users/"+id, map[string]any{"disabled": true}, http.StatusOK, nil)
	disabledAt := time.Now()
	for {
		s1 := sessB.raw(t, http.MethodGet, "/api/v1/auth/me", nil, nil).Status
		s2 := tokB.raw(t, http.MethodGet, "/api/v1/auth/me", nil, nil).Status
		if s1 == http.StatusUnauthorized && s2 == http.StatusUnauthorized {
			break
		}
		if time.Since(disabledAt) > 5*time.Second {
			t.Fatalf("after 5 s: session %d token %d", s1, s2)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSCN_AUTH_011_SameOriginForCookies(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := login(t, p.URL, adminEmail, adminPassword)
	body := map[string]any{"name": "x", "role": "viewer"}
	r := c.raw(t, http.MethodPost, "/api/v1/tokens", body, map[string]string{"Origin": "https://evil.example.com"})
	if r.Status != http.StatusForbidden {
		t.Fatalf("foreign origin: %d %s", r.Status, r.Body)
	}
	r = c.raw(t, http.MethodPost, "/api/v1/tokens", body, map[string]string{"Origin": ""})
	if r.Status != http.StatusForbidden {
		t.Fatalf("cookie POST without Origin or Sec-Fetch-Site: %d", r.Status)
	}
	r = c.raw(t, http.MethodPost, "/api/v1/tokens", body, map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"})
	if r.Status != http.StatusCreated {
		t.Fatalf("same-origin fetch: %d %s", r.Status, r.Body)
	}
	tok := adminClient(t, p)
	r = tok.raw(t, http.MethodPost, "/api/v1/tokens", body, nil)
	if r.Status != http.StatusCreated {
		t.Fatalf("token POST without Origin: %d %s", r.Status, r.Body)
	}
}

func TestSCN_AUTH_013_SecurityHeaders(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	c := newCookieClient(p.URL)
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
	}
	for _, path := range []string{"/", "/executions/abc", "/api/v1/auth/me", "/api/v1/unknown", "/healthz"} {
		r := c.raw(t, http.MethodGet, path, nil, nil)
		for k, v := range want {
			if got := r.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", path, k, got, v)
			}
		}
	}
}

func TestSCN_API_002_ErrorEnvelope(t *testing.T) {
	p := startServer(t, map[string]string{"SLUICE_DATABASE_URL": newDatabase(t)})
	admin := adminClient(t, p)
	r := admin.raw(t, http.MethodPost, "/api/v1/users", map[string]any{"email": "not-an-email", "role": "root"}, nil)
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid body: %d %s", r.Status, r.Body)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.Body, &env); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, d := range env.Error.Details {
		fields[d.Field] = true
	}
	if env.Error.Code != "validation_failed" || !fields["email"] || !fields["role"] || !fields["password"] {
		t.Fatalf("details: %s", r.Body)
	}
	r = admin.raw(t, http.MethodGet, "/api/v1/nothing/here", nil, nil)
	if r.Status != http.StatusNotFound || errCode(r.Body) != "not_found" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("unknown route: %d %s", r.Status, r.Body)
	}
}
