// Package audit writes and lists audit events (REQ-AUTH-007).
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit/auditdb"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/page"
)

// Retention of audit events.
const Retention = 365 * 24 * time.Hour

// Actor types.
const (
	ActorUser   = "user"
	ActorToken  = "token"
	ActorSystem = "system"
	ActorAI     = "ai"
)

// Actor is who performed an action. ID is the user ID for user, token and AI actors.
type Actor struct {
	Type    string
	ID      string
	TokenID string
	IP      string
}

type actorKey struct{}

// WithActor stores the actor in the context.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor, or the system actor.
func ActorFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok {
		return a
	}
	return Actor{Type: ActorSystem}
}

// Event is one audit event.
type Event struct {
	Action     string
	TargetType string
	TargetID   string
	Details    map[string]any
}

// Writer records audit events.
type Writer struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
}

// Record writes an event with the actor of ctx. db is a pool or transaction.
func (w *Writer) Record(ctx context.Context, db auditdb.DBTX, e Event) error {
	return w.RecordAs(ctx, db, ActorFrom(ctx), e)
}

// RecordAs writes an event with an explicit actor.
func (w *Writer) RecordAs(ctx context.Context, db auditdb.DBTX, a Actor, e Event) error {
	if db == nil {
		db = w.Pool
	}
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	if a.TokenID != "" {
		details["token_id"] = a.TokenID
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	id, _ := uuid.NewV7()
	return auditdb.New(db).InsertAuditEvent(ctx, auditdb.InsertAuditEventParams{
		ID: id, Ts: w.Clock.Now(), ActorType: a.Type, ActorID: a.ID, Action: e.Action,
		TargetType: e.TargetType, TargetID: e.TargetID, Details: b, Ip: a.IP,
	})
}

// Filter selects audit events.
type Filter struct {
	Actor  string // user ID or email
	Action string
	Target string // type or type:id
	From   *time.Time
	To     *time.Time
	Cursor string
	Limit  int
}

// Row is one listed event with the actor label.
type Row struct {
	ID         uuid.UUID
	Ts         time.Time
	ActorType  string
	ActorID    string
	ActorLabel string
	Action     string
	TargetType string
	TargetID   string
	Details    map[string]any
	IP         string
}

// List returns events newest first with keyset pagination.
func (w *Writer) List(ctx context.Context, f Filter) ([]Row, string, error) {
	limit := page.Limit(f.Limit)
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Actor != "" {
		p := arg(f.Actor)
		where = append(where, fmt.Sprintf("(e.actor_id = %s OR u.email = %s::citext)", p, p))
	}
	if f.Action != "" {
		where = append(where, "e.action = "+arg(f.Action))
	}
	if f.Target != "" {
		typ, id, hasID := strings.Cut(f.Target, ":")
		where = append(where, "e.target_type = "+arg(typ))
		if hasID {
			where = append(where, "e.target_id = "+arg(id))
		}
	}
	if f.From != nil {
		where = append(where, "e.ts >= "+arg(*f.From))
	}
	if f.To != nil {
		where = append(where, "e.ts <= "+arg(*f.To))
	}
	if f.Cursor != "" {
		ts, id, err := page.Decode(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, fmt.Sprintf("(e.ts, e.id) < (%s, %s)", arg(ts), arg(id)))
	}
	q := `SELECT e.id, e.ts, e.actor_type, e.actor_id, COALESCE(u.email, e.actor_id), e.action,
		e.target_type, e.target_id, e.details, e.ip
		FROM audit_events e LEFT JOIN users u ON e.actor_type <> 'system' AND u.id::text = e.actor_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY e.ts DESC, e.id DESC LIMIT %d", limit+1)
	rows, err := w.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
		var x Row
		var details []byte
		err := r.Scan(&x.ID, &x.Ts, &x.ActorType, &x.ActorID, &x.ActorLabel, &x.Action, &x.TargetType, &x.TargetID, &details, &x.IP)
		if err == nil {
			_ = json.Unmarshal(details, &x.Details)
		}
		return x, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = page.Encode(last.Ts, last.ID)
	}
	return out, next, nil
}

// DeleteExpired deletes events older than the retention. The maintenance leader calls it.
func (w *Writer) DeleteExpired(ctx context.Context) (int64, error) {
	return auditdb.New(w.Pool).DeleteOldAuditEvents(ctx, w.Clock.Now().Add(-Retention))
}
