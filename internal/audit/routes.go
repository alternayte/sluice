package audit

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/page"
)

// AuditEvent is one audit log entry.
type AuditEvent struct {
	ID         uuid.UUID      `json:"id"`
	Ts         time.Time      `json:"ts"`
	ActorType  string         `json:"actor_type" enum:"user,token,system,ai"`
	ActorID    string         `json:"actor_id"`
	ActorLabel string         `json:"actor_label"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Details    map[string]any `json:"details"`
	IP         string         `json:"ip"`
}

// AuditList is one page of audit events.
type AuditList struct {
	Items      []AuditEvent `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type listIn struct {
	Actor  string    `query:"actor"`
	Action string    `query:"action"`
	Target string    `query:"target"`
	From   time.Time `query:"from"`
	To     time.Time `query:"to"`
	Cursor string    `query:"cursor"`
	Limit  int       `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// Routes registers the audit log operation (REQ-AUTH-007).
func Routes(api huma.API, w *Writer) {
	huma.Register(api, httpx.Op("listAuditEvents", http.MethodGet, "/api/v1/audit", httpx.MinRole(kernel.Admin)),
		func(ctx context.Context, in *listIn) (*struct{ Body AuditList }, error) {
			f := Filter{Actor: in.Actor, Action: in.Action, Target: in.Target, Cursor: in.Cursor, Limit: page.Limit(in.Limit)}
			if !in.From.IsZero() {
				f.From = &in.From
			}
			if !in.To.IsZero() {
				f.To = &in.To
			}
			rows, next, err := w.List(ctx, f)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body AuditList }{Body: AuditList{Items: []AuditEvent{}}}
			if next != "" {
				out.Body.NextCursor = &next
			}
			for _, r := range rows {
				d := r.Details
				if d == nil {
					d = map[string]any{}
				}
				out.Body.Items = append(out.Body.Items, AuditEvent{ID: r.ID, Ts: r.Ts, ActorType: r.ActorType, ActorID: r.ActorID,
					ActorLabel: r.ActorLabel, Action: r.Action, TargetType: r.TargetType, TargetID: r.TargetID, Details: d, IP: r.IP})
			}
			return out, nil
		})
}
