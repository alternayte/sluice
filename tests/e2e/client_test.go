//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// Bootstrap admin of every e2e server.
const (
	adminEmail    = "admin@example.com"
	adminPassword = "admin-password-1"
)

// client calls the API with a bearer token or a session cookie.
type client struct {
	base   string
	token  string
	http   *http.Client
	origin string // Origin header for cookie-authenticated unsafe requests
}

// response is a fully read HTTP response.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

func newCookieClient(base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{base: base, http: &http.Client{Jar: jar}, origin: base}
}

func tokenClient(base, token string) *client {
	return &client{base: base, token: token, http: &http.Client{}}
}

// raw sends a request and returns the response without status checks.
func (c *client) raw(t testing.TB, method, path string, body any, headers map[string]string) response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rd = bytes.NewReader(b)
		case string:
			rd = strings.NewReader(b)
		default:
			j, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(j)
		}
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else if c.origin != "" && method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", c.origin)
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return response{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

// do sends a request, checks the status and decodes JSON into out.
func (c *client) do(t testing.TB, method, path string, body any, want int, out any) response {
	t.Helper()
	r := c.raw(t, method, path, body, nil)
	if r.Status != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, r.Status, want, r.Body)
	}
	if out != nil && len(r.Body) > 0 {
		if err := json.Unmarshal(r.Body, out); err != nil {
			t.Fatalf("decode %s %s: %v: %s", method, path, err, r.Body)
		}
	}
	return r
}

// errCode returns the error code of an error envelope.
func errCode(b []byte) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &env)
	return env.Error.Code
}

// login signs in with a cookie client.
func login(t testing.TB, base, email, password string) *client {
	t.Helper()
	c := newCookieClient(base)
	c.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password}, http.StatusOK, nil)
	return c
}

type createdToken struct {
	Secret string `json:"secret"`
	Token  struct {
		ID string `json:"id"`
	} `json:"token"`
}

// createToken creates an API token with the client and returns the secret and ID.
func createToken(t testing.TB, c *client, role string) (string, string) {
	t.Helper()
	var out createdToken
	c.do(t, http.MethodPost, "/api/v1/tokens", map[string]any{"name": "e2e-" + role, "role": role}, http.StatusCreated, &out)
	return out.Secret, out.Token.ID
}

// adminClient returns a token client for the bootstrap admin.
func adminClient(t testing.TB, p *Proc) *client {
	t.Helper()
	secret, _ := createToken(t, login(t, p.URL, adminEmail, adminPassword), "admin")
	return tokenClient(p.URL, secret)
}

type userOut struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// createUser creates a user with a temporary password and sets a final password.
func createUser(t testing.TB, admin *client, email, role, password string) (string, *client) {
	t.Helper()
	var u userOut
	admin.do(t, http.MethodPost, "/api/v1/users", map[string]string{"email": email, "role": role, "password": "temporary-pass-1"}, http.StatusCreated, &u)
	c := login(t, admin.base, email, "temporary-pass-1")
	c.do(t, http.MethodPost, "/api/v1/auth/password", map[string]string{"current_password": "temporary-pass-1", "new_password": password}, http.StatusNoContent, nil)
	return u.ID, c
}
