package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// Me is the current user.
type Me struct {
	ID                 uuid.UUID `json:"id"`
	Email              string    `json:"email"`
	Name               string    `json:"name"`
	Role               string    `json:"role" enum:"viewer,operator,editor,admin"`
	MustChangePassword bool      `json:"must_change_password"`
	AuthType           string    `json:"auth_type" enum:"session,token"`
}

// User is a user account.
type User struct {
	ID                 uuid.UUID  `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name"`
	Role               string     `json:"role" enum:"viewer,operator,editor,admin"`
	MustChangePassword bool       `json:"must_change_password"`
	Disabled           bool       `json:"disabled"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
}

// Token is an API token without its secret.
type Token struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	UserEmail  string     `json:"user_email"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role" enum:"viewer,operator,editor,admin"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type meOut struct{ Body Me }

type meCookieOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      Me
}

type cookieOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type userOut struct{ Body User }

type listIn struct {
	Cursor string `query:"cursor"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// CountResult is the number of changed items.
type CountResult struct {
	Count int `json:"count"`
}

type countOut struct{ Body CountResult }

func toMeOp(p *kernel.Principal) Me {
	kind := "session"
	if p.Kind == "token" {
		kind = "token"
	}
	return Me{ID: p.UserID, Email: p.Email, Name: p.Name, Role: p.Role.String(), MustChangePassword: p.MustChangePassword, AuthType: kind}
}

func toUserOp(u dbq.User) User {
	return User{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, MustChangePassword: u.MustChangePassword,
		Disabled: u.DisabledAt != nil, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

func toTokenOp(t dbq.ListTokensRow) Token {
	return Token{ID: t.ID, UserID: t.UserID, UserEmail: t.UserEmail, Name: t.Name, Prefix: t.Prefix, Role: t.Role,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt}
}

func mustPrincipalOp(ctx context.Context) (*kernel.Principal, error) {
	p := kernel.FromContext(ctx)
	if p == nil {
		return nil, httpx.ErrUnauthorized
	}
	return p, nil
}

func role(s string) kernel.Role {
	r, _ := kernel.ParseRole(s)
	return r
}

func withStatus(op huma.Operation, status int) huma.Operation {
	op.DefaultStatus = status
	return op
}

// Routes registers the session, profile, user and token operations (REQ-AUTH-001 to REQ-AUTH-005).
func Routes(api huma.API, s *Service) {
	registerSession(api, s)
	registerUsers(api, s)
	registerTokens(api, s)
}

func registerSession(api huma.API, s *Service) {
	huma.Register(api, httpx.Op("login", http.MethodPost, "/api/v1/auth/login", httpx.Public),
		func(ctx context.Context, in *struct {
			Body struct {
				Email    string `json:"email" minLength:"3" maxLength:"320"`
				Password string `json:"password" minLength:"1" maxLength:"1024"`
			}
		}) (*meCookieOut, error) {
			m := MetaFrom(ctx)
			res, err := s.Login(ctx, in.Body.Email, in.Body.Password, m.IP, m.UserAgent)
			if err != nil {
				return nil, err
			}
			return &meCookieOut{SetCookie: *s.SessionCookie(res.SessionID), Body: toMeOp(res.Principal)}, nil
		})

	huma.Register(api, withStatus(httpx.Op("logout", http.MethodPost, "/api/v1/auth/logout", httpx.Authenticated), http.StatusNoContent),
		func(ctx context.Context, _ *struct{}) (*cookieOut, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			if err := s.Logout(ctx, p); err != nil {
				return nil, err
			}
			return &cookieOut{SetCookie: *s.ClearCookie()}, nil
		})

	huma.Register(api, httpx.Op("getMe", http.MethodGet, "/api/v1/auth/me", httpx.Authenticated),
		func(ctx context.Context, _ *struct{}) (*meOut, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			return &meOut{Body: toMeOp(p)}, nil
		})

	huma.Register(api, httpx.Op("updateMe", http.MethodPatch, "/api/v1/auth/me", httpx.Authenticated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name string `json:"name" maxLength:"200"`
			}
		}) (*meOut, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			u, err := s.UpdateMe(ctx, p, in.Body.Name)
			if err != nil {
				return nil, err
			}
			p2 := *p
			p2.Name = u.Name
			return &meOut{Body: toMeOp(&p2)}, nil
		})

	huma.Register(api, withStatus(httpx.Op("changePassword", http.MethodPost, "/api/v1/auth/password", httpx.Authenticated), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			Body struct {
				CurrentPassword string `json:"current_password" maxLength:"1024"`
				NewPassword     string `json:"new_password" minLength:"10" maxLength:"1024"`
			}
		}) (*struct{}, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.ChangePassword(ctx, p, in.Body.CurrentPassword, in.Body.NewPassword)
		})

	huma.Register(api, httpx.Op("revokeOtherSessions", http.MethodPost, "/api/v1/auth/sessions/revoke-others", httpx.Authenticated),
		func(ctx context.Context, _ *struct{}) (*countOut, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			n, err := s.RevokeOtherSessions(ctx, p)
			if err != nil {
				return nil, err
			}
			out := &countOut{}
			out.Body.Count = int(n)
			return out, nil
		})
}

// UserList is one page of users.
type UserList struct {
	Items      []User  `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

func registerUsers(api huma.API, s *Service) {
	admin := httpx.MinRole(kernel.Admin)

	huma.Register(api, httpx.Op("listUsers", http.MethodGet, "/api/v1/users", admin),
		func(ctx context.Context, in *listIn) (*struct{ Body UserList }, error) {
			after, afterID, err := page.DecodeOpt(in.Cursor)
			if err != nil {
				return nil, err
			}
			limit := page.Limit(in.Limit)
			rows, err := s.q(nil).ListUsers(ctx, dbq.ListUsersParams{AfterCreated: after, AfterID: afterID, Lim: int32(limit + 1)})
			if err != nil {
				return nil, err
			}
			out := &struct{ Body UserList }{Body: UserList{Items: []User{}}}
			if len(rows) > limit {
				rows = rows[:limit]
				last := rows[len(rows)-1]
				c := page.Encode(last.CreatedAt, last.ID)
				out.Body.NextCursor = &c
			}
			for _, u := range rows {
				out.Body.Items = append(out.Body.Items, toUserOp(u))
			}
			return out, nil
		})

	huma.Register(api, withStatus(httpx.Op("createUser", http.MethodPost, "/api/v1/users", admin), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Email    string `json:"email" format:"email" maxLength:"320"`
				Name     string `json:"name,omitempty" maxLength:"200"`
				Role     string `json:"role" enum:"viewer,operator,editor,admin"`
				Password string `json:"password" minLength:"10" maxLength:"1024" doc:"Temporary password. The user must change it at first login."`
			}
		}) (*userOut, error) {
			u, err := s.CreateUser(ctx, in.Body.Email, in.Body.Name, role(in.Body.Role), in.Body.Password, true)
			if err != nil {
				return nil, err
			}
			return &userOut{Body: toUserOp(u)}, nil
		})

	huma.Register(api, httpx.Op("updateUser", http.MethodPatch, "/api/v1/users/{userId}", admin),
		func(ctx context.Context, in *struct {
			UserID uuid.UUID `path:"userId"`
			Body   struct {
				Name     *string `json:"name,omitempty" maxLength:"200"`
				Role     *string `json:"role,omitempty" enum:"viewer,operator,editor,admin"`
				Disabled *bool   `json:"disabled,omitempty"`
			}
		}) (*userOut, error) {
			ch := UserChange{Name: in.Body.Name, Disabled: in.Body.Disabled}
			if in.Body.Role != nil {
				r := role(*in.Body.Role)
				ch.Role = &r
			}
			u, err := s.UpdateUser(ctx, in.UserID, ch)
			if err != nil {
				return nil, err
			}
			return &userOut{Body: toUserOp(u)}, nil
		})

	huma.Register(api, withStatus(httpx.Op("resetUserPassword", http.MethodPost, "/api/v1/users/{userId}/reset-password", admin), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			UserID uuid.UUID `path:"userId"`
			Body   ResetPasswordRequest
		}) (*struct{}, error) {
			return nil, s.ResetPassword(ctx, in.UserID, in.Body.Password, true)
		})
}

// ResetPasswordRequest is the body of resetUserPassword.
type ResetPasswordRequest struct {
	Password string `json:"password" minLength:"10" maxLength:"1024"`
}

// TokenList is one page of tokens.
type TokenList struct {
	Items      []Token `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// CreatedToken is a new token with its secret. The secret is shown once (REQ-AUTH-005).
type CreatedToken struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

func registerTokens(api huma.API, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)

	huma.Register(api, httpx.Op("listTokens", http.MethodGet, "/api/v1/tokens", viewer),
		func(ctx context.Context, in *struct {
			Cursor string `query:"cursor"`
			Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
			All    bool   `query:"all" doc:"All users. Needs admin."`
		}) (*struct{ Body TokenList }, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			after, afterID, err := page.DecodeOpt(in.Cursor)
			if err != nil {
				return nil, err
			}
			limit := page.Limit(in.Limit)
			rows, err := s.ListTokens(ctx, p, in.All, after, afterID, limit+1)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body TokenList }{Body: TokenList{Items: []Token{}}}
			if len(rows) > limit {
				rows = rows[:limit]
				last := rows[len(rows)-1]
				c := page.Encode(last.CreatedAt, last.ID)
				out.Body.NextCursor = &c
			}
			for _, t := range rows {
				out.Body.Items = append(out.Body.Items, toTokenOp(t))
			}
			return out, nil
		})

	huma.Register(api, withStatus(httpx.Op("createToken", http.MethodPost, "/api/v1/tokens", viewer), http.StatusCreated),
		func(ctx context.Context, in *struct {
			Body struct {
				Name          string `json:"name" minLength:"1" maxLength:"100"`
				Role          string `json:"role" enum:"viewer,operator,editor,admin"`
				ExpiresInDays *int   `json:"expires_in_days,omitempty" minimum:"1" maximum:"365"`
			}
		}) (*struct{ Body CreatedToken }, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			secret, row, err := s.CreateToken(ctx, p, in.Body.Name, role(in.Body.Role), in.Body.ExpiresInDays)
			if err != nil {
				return nil, err
			}
			return &struct{ Body CreatedToken }{Body: CreatedToken{Token: toTokenOp(row), Secret: secret}}, nil
		})

	huma.Register(api, withStatus(httpx.Op("revokeToken", http.MethodDelete, "/api/v1/tokens/{tokenId}", viewer), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			TokenID uuid.UUID `path:"tokenId"`
		}) (*struct{}, error) {
			p, err := mustPrincipalOp(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.RevokeToken(ctx, p, in.TokenID)
		})
}
