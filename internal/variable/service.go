// Package variable manages variables per scope (REQ-SEC-007).
package variable

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/variable/variabledb"
)

// Errors of the variable feature.
var (
	ErrVariableNotFound  = httpx.Errorf(http.StatusNotFound, "variable_not_found", "variable not found")
	ErrNamespaceNotFound = httpx.Errorf(http.StatusNotFound, "namespace_not_found", "namespace not found")
)

// Service manages variables.
type Service struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Audit *audit.Writer
}

// Info is one variable with the scope that defines it.
type Info struct {
	Key       string
	Value     string
	Scope     string
	UpdatedBy string
	UpdatedAt time.Time
	Inherited bool
}

func scopeLabel(scope string) string {
	if scope == "" {
		return "global"
	}
	return scope
}

func namespaceID(ctx context.Context, q *variabledb.Queries, scope string) (*uuid.UUID, error) {
	if scope == "" {
		return nil, nil
	}
	id, err := q.NamespaceID(ctx, scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNamespaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// rank returns the position of a scope in the chain. The global scope ("") is last.
func rank(scope string, chain []string) int {
	if scope == "" {
		return len(chain)
	}
	for i, n := range chain {
		if n == scope {
			return i
		}
	}
	return len(chain) + 1
}

// List returns the global variables (namespace "") or the effective variables of a
// namespace: for each key the nearest definition with its scope (REQ-SEC-007, §6.7).
func (s *Service) List(ctx context.Context, namespace string) ([]Info, error) {
	q := variabledb.New(s.Pool)
	if _, err := namespaceID(ctx, q, namespace); err != nil {
		return nil, err
	}
	chain := flow.NamespaceChain(namespace)
	rows, err := q.ListVariables(ctx, chain)
	if err != nil {
		return nil, err
	}
	best := map[string]int{}
	var keys []string
	for i, r := range rows {
		cur, ok := best[r.Key]
		if !ok {
			keys = append(keys, r.Key)
		}
		if !ok || rank(r.Scope, chain) < rank(rows[cur].Scope, chain) {
			best[r.Key] = i
		}
	}
	out := make([]Info, 0, len(keys))
	for _, k := range keys {
		r := rows[best[k]]
		out = append(out, Info{Key: r.Key, Value: r.Value, Scope: scopeLabel(r.Scope), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt,
			Inherited: r.Scope != namespace})
	}
	return out, nil
}

// Put creates or updates a variable in a scope ("" is global).
func (s *Service) Put(ctx context.Context, scope, key, value string) (Info, error) {
	actor := "system"
	if p := kernel.FromContext(ctx); p != nil {
		actor = p.Email
	}
	now := s.Clock.Now()
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := variabledb.New(tx)
		nsID, err := namespaceID(ctx, q, scope)
		if err != nil {
			return err
		}
		id, _ := uuid.NewV7()
		created, err := q.UpsertVariable(ctx, variabledb.UpsertVariableParams{ID: id, NamespaceID: nsID, Key: key, Value: value, Actor: actor, Now: now})
		if err != nil {
			return err
		}
		action := "variable.update"
		if created {
			action = "variable.create"
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "variable", TargetID: scopeLabel(scope) + "/" + key})
	})
	return Info{Key: key, Value: value, Scope: scopeLabel(scope), UpdatedBy: actor, UpdatedAt: now}, err
}

// Delete removes a variable from a scope.
func (s *Service) Delete(ctx context.Context, scope, key string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := variabledb.New(tx)
		nsID, err := namespaceID(ctx, q, scope)
		if err != nil {
			return err
		}
		n, err := q.DeleteVariable(ctx, variabledb.DeleteVariableParams{NamespaceID: nsID, Key: key})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrVariableNotFound
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "variable.delete", TargetType: "variable", TargetID: scopeLabel(scope) + "/" + key})
	})
}
