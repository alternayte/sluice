package auth

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

type authErrKey struct{}

// OwnAuthPrefixes are path prefixes that use their own authentication (run tokens,
// webhook keys, MCP bearer tokens are resolved per route).
var OwnAuthPrefixes = []string{"/api/runner/", "/hooks/"}

// Middleware authenticates the request from a bearer API token or the session cookie,
// and enforces same-origin for cookie-authenticated unsafe methods (SI-06).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range OwnAuthPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		ctx := withMeta(r.Context(), RequestMeta{IP: httpx.ClientIP(r), UserAgent: r.UserAgent()})
		p, refresh, err := s.authenticate(ctx, r)
		if err != nil && err != errAuth {
			httpx.WriteError(w, r, err)
			return
		}
		if err == errAuth {
			ctx = context.WithValue(ctx, authErrKey{}, true)
			if _, cerr := r.Cookie(CookieName); cerr == nil && r.Header.Get("Authorization") == "" {
				http.SetCookie(w, s.ClearCookie())
			}
		}
		if p != nil {
			if p.Kind == "session" && !safeMethod(r.Method) && !s.sameOrigin(r) {
				httpx.WriteError(w, r, httpx.Errorf(http.StatusForbidden, "csrf_failed", "cross-origin request rejected"))
				return
			}
			if refresh != nil {
				http.SetCookie(w, refresh)
			}
			ctx = kernel.WithPrincipal(ctx, p)
			actor := audit.Actor{Type: audit.ActorUser, ID: p.UserID.String(), IP: httpx.ClientIP(r)}
			if p.TokenID != nil {
				actor.Type = audit.ActorToken
				actor.TokenID = p.TokenID.String()
			}
			ctx = audit.WithActor(ctx, actor)
		} else {
			ctx = audit.WithActor(ctx, audit.Actor{Type: audit.ActorSystem, IP: httpx.ClientIP(r)})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin checks Origin against the public origin or the request host, and falls
// back to Sec-Fetch-Site. A request with neither header is rejected.
func (s *Service) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		if o == "null" {
			return false
		}
		u, err := url.Parse(o)
		if err != nil {
			return false
		}
		origin := u.Scheme + "://" + u.Host
		if strings.EqualFold(origin, s.PublicOrigin) {
			return true
		}
		return strings.EqualFold(u.Host, r.Host)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		return false
	default:
		return false
	}
}

// Authorize checks the permission of an operation for the request context. It returns
// nil when the call is allowed.
func Authorize(ctx context.Context, operationID string) error {
	acc, ok := Operations[OperationKey(operationID)]
	if !ok {
		return httpx.ErrForbidden
	}
	return check(ctx, acc)
}

// AuthorizeRoute checks a non-OpenAPI route permission by its pattern.
func AuthorizeRoute(ctx context.Context, pattern string) error {
	acc, ok := Routes[pattern]
	if !ok {
		return httpx.ErrForbidden
	}
	return check(ctx, acc)
}

func check(ctx context.Context, acc Access) error {
	if acc.Public || acc.Other != "" {
		return nil
	}
	p := kernel.FromContext(ctx)
	if p == nil {
		return httpx.ErrUnauthorized
	}
	if p.MustChangePassword && !acc.Self {
		return ErrPasswordChange
	}
	if !p.Can(acc.Min) {
		return httpx.ErrForbidden
	}
	return nil
}

// RequireRole wraps a non-OpenAPI handler with a route permission.
func RequireRole(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := AuthorizeRoute(r.Context(), pattern); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
