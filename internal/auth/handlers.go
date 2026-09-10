package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/alternayte/sluice/internal/api/apigen"
	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// Handlers serves the auth, users, tokens and audit operations.
type Handlers struct {
	Svc *Service
}

type metaKey struct{}

// RequestMeta holds request data for handlers.
type RequestMeta struct {
	IP        string
	UserAgent string
}

func withMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFrom returns the request metadata.
func MetaFrom(ctx context.Context) RequestMeta {
	m, _ := ctx.Value(metaKey{}).(RequestMeta)
	return m
}

func mustPrincipal(ctx context.Context) (*Principal, error) {
	p := FromContext(ctx)
	if p == nil {
		return nil, httpx.ErrUnauthorized
	}
	return p, nil
}

func toMe(p *Principal) apigen.Me {
	kind := apigen.MeAuthTypeSession
	if p.Kind == "token" {
		kind = apigen.MeAuthTypeToken
	}
	return apigen.Me{Id: p.UserID, Email: p.Email, Name: p.Name, Role: apigen.Role(p.Role.String()),
		MustChangePassword: p.MustChangePassword, AuthType: kind}
}

func toUser(u dbq.User) apigen.User {
	return apigen.User{Id: u.ID, Email: u.Email, Name: u.Name, Role: apigen.Role(u.Role), MustChangePassword: u.MustChangePassword,
		Disabled: u.DisabledAt != nil, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

func toToken(t dbq.ListTokensRow) apigen.Token {
	return apigen.Token{Id: t.ID, UserId: t.UserID, UserEmail: t.UserEmail, Name: t.Name, Prefix: t.Prefix, Role: apigen.Role(t.Role),
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt}
}

func parseRole(r apigen.Role) Role {
	role, _ := ParseRole(string(r))
	return role
}

type loginResponse struct {
	cookie *http.Cookie
	me     apigen.Me
}

func (r loginResponse) VisitLoginResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.me)
}

// Login signs in and sets the session cookie (REQ-AUTH-001).
func (h Handlers) Login(ctx context.Context, req apigen.LoginRequestObject) (apigen.LoginResponseObject, error) {
	m := MetaFrom(ctx)
	res, err := h.Svc.Login(ctx, req.Body.Email, req.Body.Password, m.IP, m.UserAgent)
	if err != nil {
		return nil, err
	}
	return loginResponse{cookie: h.Svc.SessionCookie(res.SessionID), me: toMe(res.Principal)}, nil
}

type logoutResponse struct{ cookie *http.Cookie }

func (r logoutResponse) VisitLogoutResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Logout deletes the session.
func (h Handlers) Logout(ctx context.Context, _ apigen.LogoutRequestObject) (apigen.LogoutResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.Svc.Logout(ctx, p); err != nil {
		return nil, err
	}
	return logoutResponse{cookie: h.Svc.ClearCookie()}, nil
}

// GetMe returns the current user.
func (h Handlers) GetMe(ctx context.Context, _ apigen.GetMeRequestObject) (apigen.GetMeResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return apigen.GetMe200JSONResponse(toMe(p)), nil
}

// UpdateMe changes the own name.
func (h Handlers) UpdateMe(ctx context.Context, req apigen.UpdateMeRequestObject) (apigen.UpdateMeResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.Svc.UpdateMe(ctx, p, req.Body.Name)
	if err != nil {
		return nil, err
	}
	p2 := *p
	p2.Name = u.Name
	return apigen.UpdateMe200JSONResponse(toMe(&p2)), nil
}

// ChangePassword changes the own password (REQ-AUTH-004).
func (h Handlers) ChangePassword(ctx context.Context, req apigen.ChangePasswordRequestObject) (apigen.ChangePasswordResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.Svc.ChangePassword(ctx, p, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return apigen.ChangePassword204Response{}, nil
}

// RevokeOtherSessions signs out the other sessions.
func (h Handlers) RevokeOtherSessions(ctx context.Context, _ apigen.RevokeOtherSessionsRequestObject) (apigen.RevokeOtherSessionsResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.Svc.RevokeOtherSessions(ctx, p)
	if err != nil {
		return nil, err
	}
	return apigen.RevokeOtherSessions200JSONResponse{Count: int(n)}, nil
}

// ListUsers lists users.
func (h Handlers) ListUsers(ctx context.Context, req apigen.ListUsersRequestObject) (apigen.ListUsersResponseObject, error) {
	after, afterID, err := page.DecodePtr((*string)(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	limit := page.LimitPtr((*int)(req.Params.Limit))
	rows, err := h.Svc.q(nil).ListUsers(ctx, dbq.ListUsersParams{AfterCreated: after, AfterID: afterID, Lim: int32(limit + 1)})
	if err != nil {
		return nil, err
	}
	out := apigen.ListUsers200JSONResponse{Items: []apigen.User{}}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		c := page.Encode(last.CreatedAt, last.ID)
		out.NextCursor = &c
	}
	for _, u := range rows {
		out.Items = append(out.Items, toUser(u))
	}
	return out, nil
}

// CreateUser creates a user with a temporary password.
func (h Handlers) CreateUser(ctx context.Context, req apigen.CreateUserRequestObject) (apigen.CreateUserResponseObject, error) {
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	u, err := h.Svc.CreateUser(ctx, string(req.Body.Email), name, parseRole(req.Body.Role), req.Body.Password, true)
	if err != nil {
		return nil, err
	}
	return apigen.CreateUser201JSONResponse(toUser(u)), nil
}

// UpdateUser changes role, name or disabled state.
func (h Handlers) UpdateUser(ctx context.Context, req apigen.UpdateUserRequestObject) (apigen.UpdateUserResponseObject, error) {
	ch := UserChange{Name: req.Body.Name, Disabled: req.Body.Disabled}
	if req.Body.Role != nil {
		r := parseRole(*req.Body.Role)
		ch.Role = &r
	}
	u, err := h.Svc.UpdateUser(ctx, req.UserId, ch)
	if err != nil {
		return nil, err
	}
	return apigen.UpdateUser200JSONResponse(toUser(u)), nil
}

// ResetUserPassword sets a temporary password.
func (h Handlers) ResetUserPassword(ctx context.Context, req apigen.ResetUserPasswordRequestObject) (apigen.ResetUserPasswordResponseObject, error) {
	if err := h.Svc.ResetPassword(ctx, req.UserId, req.Body.Password, true); err != nil {
		return nil, err
	}
	return apigen.ResetUserPassword204Response{}, nil
}

// ListTokens lists tokens.
func (h Handlers) ListTokens(ctx context.Context, req apigen.ListTokensRequestObject) (apigen.ListTokensResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	after, afterID, err := page.DecodePtr(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := page.LimitPtr(req.Params.Limit)
	all := req.Params.All != nil && *req.Params.All
	rows, err := h.Svc.ListTokens(ctx, p, all, after, afterID, limit+1)
	if err != nil {
		return nil, err
	}
	out := apigen.ListTokens200JSONResponse{Items: []apigen.Token{}}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		c := page.Encode(last.CreatedAt, last.ID)
		out.NextCursor = &c
	}
	for _, t := range rows {
		out.Items = append(out.Items, toToken(t))
	}
	return out, nil
}

// CreateToken creates a token and returns the secret once.
func (h Handlers) CreateToken(ctx context.Context, req apigen.CreateTokenRequestObject) (apigen.CreateTokenResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	secret, row, err := h.Svc.CreateToken(ctx, p, req.Body.Name, parseRole(req.Body.Role), req.Body.ExpiresInDays)
	if err != nil {
		return nil, err
	}
	return apigen.CreateToken201JSONResponse{Token: toToken(row), Secret: secret}, nil
}

// RevokeToken revokes a token.
func (h Handlers) RevokeToken(ctx context.Context, req apigen.RevokeTokenRequestObject) (apigen.RevokeTokenResponseObject, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.Svc.RevokeToken(ctx, p, req.TokenId); err != nil {
		return nil, err
	}
	return apigen.RevokeToken204Response{}, nil
}

// ListAuditEvents lists audit events with filters.
func (h Handlers) ListAuditEvents(ctx context.Context, req apigen.ListAuditEventsRequestObject) (apigen.ListAuditEventsResponseObject, error) {
	f := audit.Filter{From: req.Params.From, To: req.Params.To, Limit: page.LimitPtr((*int)(req.Params.Limit))}
	if req.Params.Actor != nil {
		f.Actor = *req.Params.Actor
	}
	if req.Params.Action != nil {
		f.Action = *req.Params.Action
	}
	if req.Params.Target != nil {
		f.Target = *req.Params.Target
	}
	if req.Params.Cursor != nil {
		f.Cursor = string(*req.Params.Cursor)
	}
	rows, next, err := h.Svc.Audit.List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := apigen.ListAuditEvents200JSONResponse{Items: []apigen.AuditEvent{}}
	if next != "" {
		out.NextCursor = &next
	}
	for _, r := range rows {
		d := r.Details
		if d == nil {
			d = map[string]any{}
		}
		out.Items = append(out.Items, apigen.AuditEvent{Id: r.ID, Ts: r.Ts, ActorType: apigen.AuditEventActorType(r.ActorType),
			ActorId: r.ActorID, ActorLabel: r.ActorLabel, Action: r.Action, TargetType: r.TargetType, TargetId: r.TargetID, Details: d, Ip: r.IP})
	}
	return out, nil
}
