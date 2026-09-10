package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/dbq"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/token"
)

// CookieName is the session cookie (REQ-AUTH-001).
const CookieName = "sluice_session"

// Login rate limits of REQ-AUTH-008.
const (
	RateWindow        = 15 * time.Minute
	MaxEmailFailures  = 10
	MaxIPFailures     = 50
	sessionTouchEvery = time.Minute
	tokenTouchEvery   = 10 * time.Second
	maxTokenDays      = 365
)

// Errors of the auth service.
var (
	ErrInvalidCredentials  = httpx.Errorf(http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
	ErrLastAdmin           = httpx.Errorf(http.StatusConflict, "last_admin", "at least one enabled admin must remain")
	ErrPasswordChange      = httpx.ErrPasswordChange
	ErrEmailTaken          = httpx.Errorf(http.StatusConflict, "email_taken", "a user with this email exists")
	ErrTokenRoleTooHigh    = httpx.Errorf(http.StatusForbidden, "forbidden", "the token role must not exceed your role")
	ErrWrongCurrentPasword = httpx.Validation(httpx.FieldError{Field: "current_password", Message: "the current password is wrong"})
)

// Service implements users, sessions and tokens.
type Service struct {
	Pool         *pgxpool.Pool
	Clock        clock.Clock
	Audit        *audit.Writer
	Log          *slog.Logger
	SessionTTL   time.Duration
	SecureCookie bool
	// PublicOrigin is scheme://host[:port] of SLUICE_PUBLIC_URL.
	PublicOrigin string
}

func (s *Service) q(db dbq.DBTX) *dbq.Queries {
	if db == nil {
		return dbq.New(s.Pool)
	}
	return dbq.New(db)
}

func newID() uuid.UUID {
	id, _ := uuid.NewV7()
	return id
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Bootstrap creates the first admin when the users table is empty (REQ-AUTH-002).
func (s *Service) Bootstrap(ctx context.Context, email, password string) (bool, error) {
	if email == "" || password == "" {
		return false, nil
	}
	n, err := s.q(nil).CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	id := newID()
	tag, err := s.Pool.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, role, created_at)
		SELECT $1, $2, 'Admin', $3, 'admin', $4 WHERE NOT EXISTS (SELECT 1 FROM users)
		ON CONFLICT (email) DO NOTHING`, id, normEmail(email), hash, s.Clock.Now())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	_ = s.Audit.RecordAs(ctx, nil, audit.Actor{Type: audit.ActorSystem}, audit.Event{
		Action: "user.create", TargetType: "user", TargetID: id.String(),
		Details: map[string]any{"email": normEmail(email), "role": "admin", "source": "bootstrap"},
	})
	return true, nil
}

// LoginResult is a successful login.
type LoginResult struct {
	SessionID string
	Principal *kernel.Principal
}

// Login checks the rate limit and the password and creates a session.
func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (*LoginResult, error) {
	email = normEmail(email)
	now := s.Clock.Now()
	q := s.q(nil)
	f, err := q.LoginFailures(ctx, dbq.LoginFailuresParams{Email: email, Ip: ip, Since: now.Add(-RateWindow)})
	if err != nil {
		return nil, err
	}
	if f.EmailFailures >= MaxEmailFailures || f.IpFailures >= MaxIPFailures {
		oldest := f.EmailOldest
		if f.IpFailures >= MaxIPFailures && (f.EmailFailures < MaxEmailFailures || f.IpOldest.Before(oldest)) {
			oldest = f.IpOldest
		}
		retry := int(math.Ceil(oldest.Add(RateWindow).Sub(now).Seconds()))
		if retry < 1 {
			retry = 1
		}
		e := httpx.Errorf(http.StatusTooManyRequests, "rate_limited", "too many failed logins, try again later")
		e.RetryAfter = retry
		return nil, e
	}
	u, err := q.GetUserByEmail(ctx, email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	hash := dummyHash
	if found {
		hash = u.PasswordHash
	}
	ok, verr := VerifyPassword(hash, password)
	if verr != nil && found {
		s.Log.Error("password hash unreadable", "user", u.ID, "err", verr)
	}
	if !found || !ok || u.DisabledAt != nil {
		_ = q.InsertLoginAttempt(ctx, dbq.InsertLoginAttemptParams{Email: email, Ip: ip, AttemptedAt: now, Success: false})
		actor := audit.Actor{Type: audit.ActorSystem, IP: ip}
		if found {
			actor = audit.Actor{Type: audit.ActorUser, ID: u.ID.String(), IP: ip}
		}
		_ = s.Audit.RecordAs(ctx, nil, actor, audit.Event{Action: "auth.login_failed", TargetType: "user", TargetID: idOrEmpty(found, u.ID), Details: map[string]any{"email": email}})
		return nil, ErrInvalidCredentials
	}
	sid, sidHash := token.NewSecret()
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		qt := dbq.New(tx)
		if err := qt.InsertLoginAttempt(ctx, dbq.InsertLoginAttemptParams{Email: email, Ip: ip, AttemptedAt: now, Success: true}); err != nil {
			return err
		}
		if err := qt.InsertSession(ctx, dbq.InsertSessionParams{IDHash: sidHash, UserID: u.ID, CreatedAt: now,
			ExpiresAt: now.Add(s.SessionTTL), Ip: ip, UserAgent: truncate(userAgent, 512)}); err != nil {
			return err
		}
		if err := qt.TouchLogin(ctx, dbq.TouchLoginParams{ID: u.ID, LastLoginAt: &now}); err != nil {
			return err
		}
		return s.Audit.RecordAs(ctx, tx, audit.Actor{Type: audit.ActorUser, ID: u.ID.String(), IP: ip},
			audit.Event{Action: "auth.login", TargetType: "user", TargetID: u.ID.String()})
	})
	if err != nil {
		return nil, err
	}
	role, _ := kernel.ParseRole(u.Role)
	return &LoginResult{SessionID: sid, Principal: &kernel.Principal{UserID: u.ID, Email: u.Email, Name: u.Name, Role: role,
		Kind: "session", SessionHash: sidHash, MustChangePassword: u.MustChangePassword}}, nil
}

func idOrEmpty(ok bool, id uuid.UUID) string {
	if !ok {
		return ""
	}
	return id.String()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Logout deletes the current session.
func (s *Service) Logout(ctx context.Context, p *kernel.Principal) error {
	if p.Kind == "session" {
		if err := s.q(nil).DeleteSession(ctx, p.SessionHash); err != nil {
			return err
		}
	}
	return s.Audit.Record(ctx, nil, audit.Event{Action: "auth.logout", TargetType: "user", TargetID: p.UserID.String()})
}

// errAuth marks an authentication failure from presented credentials.
var errAuth = httpx.ErrUnauthorized

// authenticate resolves the principal from a bearer token or the session cookie.
// It returns a refreshed cookie when the sliding session was extended.
func (s *Service) authenticate(ctx context.Context, r *http.Request) (*kernel.Principal, *http.Cookie, error) {
	now := s.Clock.Now()
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, cred, _ := strings.Cut(h, " ")
		if !strings.EqualFold(scheme, "Bearer") || !LooksLikeAPIToken(strings.TrimSpace(cred)) {
			return nil, nil, errAuth
		}
		row, err := s.q(nil).GetTokenByHash(ctx, token.HashSecret(strings.TrimSpace(cred)))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errAuth
		}
		if err != nil {
			return nil, nil, err
		}
		if row.RevokedAt != nil || row.DisabledAt != nil || (row.ExpiresAt != nil && !now.Before(*row.ExpiresAt)) {
			return nil, nil, errAuth
		}
		if row.LastUsedAt == nil || now.Sub(*row.LastUsedAt) > tokenTouchEvery {
			_ = s.q(nil).TouchToken(ctx, dbq.TouchTokenParams{ID: row.ID, LastUsedAt: &now})
		}
		tr, _ := kernel.ParseRole(row.TokenRole)
		ur, _ := kernel.ParseRole(row.UserRole)
		id := row.ID
		return &kernel.Principal{UserID: row.UserID, Email: row.Email, Name: row.Name, Role: kernel.MinRole(tr, ur), Kind: "token",
			TokenID: &id, MustChangePassword: row.MustChangePassword}, nil, nil
	}
	value := sessionCookieValue(r)
	if value == "" {
		return nil, nil, nil
	}
	hash := token.HashSecret(value)
	row, err := s.q(nil).GetSession(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, errAuth
	}
	if err != nil {
		return nil, nil, err
	}
	if row.DisabledAt != nil || !now.Before(row.ExpiresAt) {
		return nil, nil, errAuth
	}
	var refresh *http.Cookie
	if now.Sub(row.LastSeenAt) > sessionTouchEvery {
		if err := s.q(nil).TouchSession(ctx, dbq.TouchSessionParams{IDHash: hash, LastSeenAt: now, ExpiresAt: now.Add(s.SessionTTL)}); err == nil {
			refresh = s.SessionCookie(value)
		}
	}
	role, _ := kernel.ParseRole(row.Role)
	return &kernel.Principal{UserID: row.UserID, Email: row.Email, Name: row.Name, Role: role, Kind: "session",
		SessionHash: hash, MustChangePassword: row.MustChangePassword}, refresh, nil
}

// SessionCookie returns the session cookie with the sliding TTL.
func (s *Service) SessionCookie(value string) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: s.SecureCookie,
		SameSite: http.SameSiteLaxMode, MaxAge: int(s.SessionTTL.Seconds())}
}

// sessionCookieValue returns the session cookie value, or "" when the request has none.
func sessionCookieValue(r *http.Request) string {
	for _, c := range r.Cookies() {
		if c.Name == CookieName {
			return c.Value
		}
	}
	return ""
}

// ClearCookie returns a cookie that deletes the session cookie.
func (s *Service) ClearCookie() *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.SecureCookie,
		SameSite: http.SameSiteLaxMode, MaxAge: -1}
}

// UpdateMe changes the own name.
func (s *Service) UpdateMe(ctx context.Context, p *kernel.Principal, name string) (dbq.User, error) {
	var out dbq.User
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		u, err := dbq.New(tx).GetUserForUpdate(ctx, p.UserID)
		if err != nil {
			return err
		}
		out, err = dbq.New(tx).UpdateUser(ctx, dbq.UpdateUserParams{ID: u.ID, Name: strings.TrimSpace(name), Role: u.Role, DisabledAt: u.DisabledAt})
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "user.update", TargetType: "user", TargetID: u.ID.String(), Details: map[string]any{"name": out.Name}})
	})
	return out, err
}

// ChangePassword changes the own password and signs out other sessions (REQ-AUTH-004).
func (s *Service) ChangePassword(ctx context.Context, p *kernel.Principal, current, next string) error {
	u, err := s.q(nil).GetUserByID(ctx, p.UserID)
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(u.PasswordHash, current)
	if err != nil || !ok {
		return ErrWrongCurrentPasword
	}
	if err := ValidatePassword(next); err != nil {
		return httpx.Validation(httpx.FieldError{Field: "new_password", Message: err.Error()})
	}
	if next == current {
		return httpx.Validation(httpx.FieldError{Field: "new_password", Message: "the new password must differ from the current password"})
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		if err := q.SetUserPassword(ctx, dbq.SetUserPasswordParams{ID: u.ID, PasswordHash: hash, MustChangePassword: false}); err != nil {
			return err
		}
		if _, err := q.DeleteOtherSessions(ctx, dbq.DeleteOtherSessionsParams{UserID: u.ID, IDHash: sessionHashOrNone(p)}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "user.change_password", TargetType: "user", TargetID: u.ID.String()})
	})
}

func sessionHashOrNone(p *kernel.Principal) []byte {
	if p.Kind == "session" {
		return p.SessionHash
	}
	return []byte{}
}

// RevokeOtherSessions deletes all sessions of the user except the current one.
func (s *Service) RevokeOtherSessions(ctx context.Context, p *kernel.Principal) (int64, error) {
	n, err := s.q(nil).DeleteOtherSessions(ctx, dbq.DeleteOtherSessionsParams{UserID: p.UserID, IDHash: sessionHashOrNone(p)})
	if err != nil {
		return 0, err
	}
	return n, s.Audit.Record(ctx, nil, audit.Event{Action: "auth.revoke_other_sessions", TargetType: "user", TargetID: p.UserID.String(), Details: map[string]any{"count": n}})
}

// CreateUser creates a user with a temporary password (REQ-AUTH-003). A temporary
// password forces a change at next login.
func (s *Service) CreateUser(ctx context.Context, email, name string, role kernel.Role, password string, temporary bool) (dbq.User, error) {
	if role == kernel.RoleNone {
		return dbq.User{}, httpx.Validation(httpx.FieldError{Field: "role", Message: "unknown role"})
	}
	if err := ValidatePassword(password); err != nil {
		return dbq.User{}, httpx.Validation(httpx.FieldError{Field: "password", Message: err.Error()})
	}
	email = normEmail(email)
	if !strings.Contains(email, "@") {
		return dbq.User{}, httpx.Validation(httpx.FieldError{Field: "email", Message: "must be an email address"})
	}
	hash, err := HashPassword(password)
	if err != nil {
		return dbq.User{}, err
	}
	id := newID()
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := dbq.New(tx).InsertUser(ctx, dbq.InsertUserParams{ID: id, Email: email, Name: strings.TrimSpace(name),
			PasswordHash: hash, Role: role.String(), MustChangePassword: temporary, CreatedAt: s.Clock.Now()}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "user.create", TargetType: "user", TargetID: id.String(),
			Details: map[string]any{"email": email, "role": role.String()}})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return dbq.User{}, ErrEmailTaken
	}
	if err != nil {
		return dbq.User{}, err
	}
	return s.q(nil).GetUserByID(ctx, id)
}

// UserChange holds optional user changes.
type UserChange struct {
	Name     *string
	Role     *kernel.Role
	Disabled *bool
}

// UpdateUser changes a user. The last enabled admin cannot be disabled or demoted.
func (s *Service) UpdateUser(ctx context.Context, id uuid.UUID, ch UserChange) (dbq.User, error) {
	var out dbq.User
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		admins, err := q.LockEnabledAdmins(ctx)
		if err != nil {
			return err
		}
		u, err := q.GetUserForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		name, role, disabledAt := u.Name, u.Role, u.DisabledAt
		details := map[string]any{}
		if ch.Name != nil {
			name = strings.TrimSpace(*ch.Name)
			details["name"] = name
		}
		if ch.Role != nil {
			if *ch.Role == kernel.RoleNone {
				return httpx.Validation(httpx.FieldError{Field: "role", Message: "unknown role"})
			}
			if role != ch.Role.String() {
				details["role"] = map[string]string{"from": role, "to": ch.Role.String()}
			}
			role = ch.Role.String()
		}
		if ch.Disabled != nil {
			if *ch.Disabled && disabledAt == nil {
				now := s.Clock.Now()
				disabledAt = &now
				details["disabled"] = true
			} else if !*ch.Disabled && disabledAt != nil {
				disabledAt = nil
				details["disabled"] = false
			}
		}
		wasEnabledAdmin := u.Role == "admin" && u.DisabledAt == nil
		staysEnabledAdmin := role == "admin" && disabledAt == nil
		if wasEnabledAdmin && !staysEnabledAdmin && len(admins) <= 1 {
			return ErrLastAdmin
		}
		out, err = q.UpdateUser(ctx, dbq.UpdateUserParams{ID: id, Name: name, Role: role, DisabledAt: disabledAt})
		if err != nil {
			return err
		}
		if disabledAt != nil {
			if _, err := q.DeleteUserSessions(ctx, id); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "user.update", TargetType: "user", TargetID: id.String(), Details: details})
	})
	return out, err
}

// ResetPassword sets a password. temporary forces a change at next login.
func (s *Service) ResetPassword(ctx context.Context, id uuid.UUID, password string, temporary bool) error {
	if err := ValidatePassword(password); err != nil {
		return httpx.Validation(httpx.FieldError{Field: "password", Message: err.Error()})
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		if _, err := q.GetUserForUpdate(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		} else if err != nil {
			return err
		}
		if err := q.SetUserPassword(ctx, dbq.SetUserPasswordParams{ID: id, PasswordHash: hash, MustChangePassword: temporary}); err != nil {
			return err
		}
		if _, err := q.DeleteUserSessions(ctx, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "user.reset_password", TargetType: "user", TargetID: id.String()})
	})
}

// UserByEmail returns a user.
func (s *Service) UserByEmail(ctx context.Context, email string) (dbq.User, error) {
	u, err := s.q(nil).GetUserByEmail(ctx, normEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return u, httpx.ErrNotFound
	}
	return u, err
}

// CreateToken creates an API token for the principal (REQ-AUTH-005).
func (s *Service) CreateToken(ctx context.Context, p *kernel.Principal, name string, role kernel.Role, days *int) (string, dbq.ListTokensRow, error) {
	if role == kernel.RoleNone {
		return "", dbq.ListTokensRow{}, httpx.Validation(httpx.FieldError{Field: "role", Message: "unknown role"})
	}
	if role > p.Role {
		return "", dbq.ListTokensRow{}, ErrTokenRoleTooHigh
	}
	now := s.Clock.Now()
	var exp *time.Time
	if days != nil {
		if *days < 1 || *days > maxTokenDays {
			return "", dbq.ListTokensRow{}, httpx.Validation(httpx.FieldError{Field: "expires_in_days", Message: fmt.Sprintf("must be between 1 and %d", maxTokenDays)})
		}
		t := now.Add(time.Duration(*days) * 24 * time.Hour)
		exp = &t
	}
	secret, prefix, hash := NewAPIToken()
	id := newID()
	name = strings.TrimSpace(name)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := dbq.New(tx).InsertToken(ctx, dbq.InsertTokenParams{ID: id, UserID: p.UserID, Name: name, TokenHash: hash,
			Prefix: prefix, Role: role.String(), CreatedAt: now, ExpiresAt: exp}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "token.create", TargetType: "token", TargetID: id.String(),
			Details: map[string]any{"name": name, "role": role.String(), "prefix": prefix}})
	})
	if err != nil {
		return "", dbq.ListTokensRow{}, err
	}
	return secret, dbq.ListTokensRow{ID: id, UserID: p.UserID, Name: name, Prefix: prefix, Role: role.String(),
		CreatedAt: now, ExpiresAt: exp, UserEmail: p.Email}, nil
}

// ListTokens lists own tokens, or all tokens for admins with all=true.
func (s *Service) ListTokens(ctx context.Context, p *kernel.Principal, all bool, afterCreated *time.Time, afterID *uuid.UUID, limit int) ([]dbq.ListTokensRow, error) {
	var owner *uuid.UUID
	if all {
		if !p.Can(kernel.Admin) {
			return nil, httpx.ErrForbidden
		}
	} else {
		owner = &p.UserID
	}
	return s.q(nil).ListTokens(ctx, dbq.ListTokensParams{UserID: owner, AfterCreated: afterCreated, AfterID: afterID, Lim: int32(limit)})
}

// RevokeToken revokes a token. Owners and admins can revoke.
func (s *Service) RevokeToken(ctx context.Context, p *kernel.Principal, id uuid.UUID) error {
	t, err := s.q(nil).GetToken(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.UserID != p.UserID && !p.Can(kernel.Admin)) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		now := s.Clock.Now()
		if err := dbq.New(tx).RevokeToken(ctx, dbq.RevokeTokenParams{ID: id, RevokedAt: &now}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "token.revoke", TargetType: "token", TargetID: id.String(),
			Details: map[string]any{"name": t.Name, "owner": t.UserEmail}})
	})
}

// Cleanup deletes expired sessions and old login attempts. The maintenance leader calls it.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.Clock.Now()
	if _, err := s.q(nil).DeleteExpiredSessions(ctx, now); err != nil {
		return err
	}
	_, err := s.q(nil).DeleteOldLoginAttempts(ctx, now.Add(-24*time.Hour))
	return err
}
