package auth

import (
	"context"

	"github.com/google/uuid"
)

// Role is one of the four fixed roles, ordered viewer < operator < editor < admin.
type Role int

// Roles in order (REQ-AUTH-006).
const (
	RoleNone Role = iota
	Viewer
	Operator
	Editor
	Admin
)

var roleNames = map[Role]string{Viewer: "viewer", Operator: "operator", Editor: "editor", Admin: "admin"}

// AllRoles lists the roles in order.
var AllRoles = []Role{Viewer, Operator, Editor, Admin}

func (r Role) String() string { return roleNames[r] }

// ParseRole parses a role name.
func ParseRole(s string) (Role, bool) {
	for r, n := range roleNames {
		if n == s {
			return r, true
		}
	}
	return RoleNone, false
}

// MinRole returns the lower role.
func MinRole(a, b Role) Role {
	if a < b {
		return a
	}
	return b
}

// Principal is the authenticated caller.
type Principal struct {
	UserID             uuid.UUID
	Email              string
	Name               string
	Role               Role
	Kind               string // session or token
	TokenID            *uuid.UUID
	SessionHash        []byte
	MustChangePassword bool
}

// Can reports whether the principal has at least role min.
func (p *Principal) Can(min Role) bool { return p != nil && p.Role >= min }

type principalKey struct{}

// WithPrincipal stores the principal in the context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the principal or nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
