package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FlowListRow is one flow in the flows list.
type FlowListRow struct {
	ID          uuid.UUID
	Namespace   string
	FlowID      string
	Path        string
	Valid       bool
	Disabled    bool
	Description string
	Labels      map[string]string
	ErrorCount  int
	LastID      *uuid.UUID
	LastState   string
	LastCreated *time.Time
}

const flowListSelect = `SELECT f.id, n.name, f.flow_key, f.path, f.valid, f.disabled,
	coalesce(r.definition->>'description', ''), coalesce(r.definition->'labels', '{}'::jsonb),
	coalesce(jsonb_array_length(r.errors), 0), le.id, coalesce(le.state, ''), le.created_at
	FROM flows f JOIN namespaces n ON n.id = f.namespace_id
	LEFT JOIN flow_revisions r ON r.id = f.current_revision_id
	LEFT JOIN LATERAL (SELECT e.id, e.state, e.created_at FROM executions e WHERE e.flow_id = f.id
		ORDER BY e.created_at DESC, e.id DESC LIMIT 1) le ON true
	WHERE f.deleted_at IS NULL AND n.deleted_at IS NULL`

func scanFlowRows(rows pgx.Rows) ([]FlowListRow, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FlowListRow, error) {
		var x FlowListRow
		var labels []byte
		err := r.Scan(&x.ID, &x.Namespace, &x.FlowID, &x.Path, &x.Valid, &x.Disabled, &x.Description, &labels, &x.ErrorCount,
			&x.LastID, &x.LastState, &x.LastCreated)
		if err == nil {
			_ = json.Unmarshal(labels, &x.Labels)
		}
		return x, err
	})
}

// ListFlows lists flows of a namespace and its children in (namespace, flow ID) order.
func (s *Service) ListFlows(ctx context.Context, nsPrefix string, after []string, limit int) ([]FlowListRow, error) {
	q := flowListSelect
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if nsPrefix != "" {
		p := arg(nsPrefix)
		q += fmt.Sprintf(" AND (n.name = %s OR starts_with(n.name, %s || '.'))", p, p)
	}
	if len(after) == 2 {
		q += fmt.Sprintf(" AND (n.name, f.flow_key) > (%s, %s)", arg(after[0]), arg(after[1]))
	}
	q += fmt.Sprintf(" ORDER BY n.name, f.flow_key LIMIT %d", limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanFlowRows(rows)
}

// ListFlowsByID returns the list row of one flow.
func (s *Service) ListFlowsByID(ctx context.Context, id uuid.UUID) ([]FlowListRow, error) {
	rows, err := s.Pool.Query(ctx, flowListSelect+" AND f.id = $1", id)
	if err != nil {
		return nil, err
	}
	return scanFlowRows(rows)
}

var _ = strings.TrimSpace
