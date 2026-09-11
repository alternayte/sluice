package trigger

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/platform/token"
	"github.com/alternayte/sluice/internal/trigger/triggerdb"
)

// Limits and timings of triggers.
const (
	// DefaultGrace is the age after which a passed fire time counts as missed (DI-27).
	DefaultGrace = 30 * time.Second
	// MaxChainDepth is the deepest flow trigger chain that fires (REQ-TRG-005).
	MaxChainDepth = 10
	// MaxWebhookBody is the webhook body limit (REQ-TRG-004).
	MaxWebhookBody = 1 << 20
	// maxCatchUpScan bounds the search for the latest missed fire time.
	maxCatchUpScan = 1_000_000
)

// Errors of the trigger feature.
var (
	ErrWebhookNotFound = httpx.Errorf(http.StatusNotFound, "not_found", "webhook not found")
	ErrFlowDisabled    = httpx.Errorf(http.StatusConflict, "flow_disabled", "the flow is disabled")
	ErrFlowInvalid     = httpx.Errorf(http.StatusUnprocessableEntity, "flow_invalid", "the flow is invalid")
	ErrBodyTooLarge    = httpx.Errorf(http.StatusRequestEntityTooLarge, "body_too_large", "the webhook body is larger than 1 MiB")
	ErrTriggerNotFound = httpx.Errorf(http.StatusNotFound, "trigger_not_found", "trigger not found")
	ErrNotWebhook      = httpx.Errorf(http.StatusConflict, "not_a_webhook", "the trigger is not a webhook trigger")
)

// Start describes an execution that a trigger creates.
type Start struct {
	Namespace      string
	FlowKey        string
	TriggerType    string
	TriggerID      uuid.UUID
	ScheduledFor   *time.Time
	Payload        map[string]any
	InputTemplates map[string]string
	ChainDepth     int
}

// Starter creates executions. internal/app adapts the execution engine.
type Starter interface {
	// Start creates an execution in tx. The scheduler and flow triggers use it.
	Start(ctx context.Context, tx pgx.Tx, s Start) (uuid.UUID, error)
	// StartOwn creates an execution in its own transaction. It loads the flow first, so
	// that concurrent calls cannot hold all pool connections (webhooks).
	StartOwn(ctx context.Context, s Start) (uuid.UUID, error)
}

// Service fires schedule, webhook and flow triggers.
type Service struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Audit   *audit.Writer
	Log     *slog.Logger
	Starter Starter
	// Holder is the lease holder name of this instance.
	Holder string
	// PublicURL is the external base URL for webhook URLs.
	PublicURL string
	// Grace overrides DefaultGrace.
	Grace time.Duration
}

func (s *Service) grace() time.Duration {
	if s.Grace > 0 {
		return s.Grace
	}
	return DefaultGrace
}

// Tick fires the due schedules. Every state change checks the scheduler lease in the same
// statement, so only the lease holder fires (REQ-TRG-002, REQ-TRG-003, D-14).
func (s *Service) Tick(ctx context.Context) error {
	now := s.Clock.Now()
	rows, err := triggerdb.New(s.Pool).ListDueSchedules(ctx, &now)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.fireSchedule(ctx, r, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.Log.Warn("schedule trigger", "trigger", r.ID, "err", err)
		}
	}
	return nil
}

// schedulePlan is the result of one schedule check: the next fire time and the time to
// fire now, if any.
type schedulePlan struct {
	next time.Time
	fire *time.Time
}

// planSchedule applies catch_up (REQ-TRG-002). A due time is missed when a later fire
// time also passed or when it passed more than grace ago. `last` fires the latest passed
// time. `none` fires no missed time.
func planSchedule(sch *Schedule, catchUp string, due *time.Time, now time.Time, grace time.Duration) schedulePlan {
	next := sch.Next(now)
	if next.IsZero() {
		next = now.AddDate(5, 0, 0) // no fire time within five years: check again then
	}
	p := schedulePlan{next: next}
	if due == nil {
		return p
	}
	latest, passed := *due, 1
	for n := sch.Next(latest); !n.IsZero() && !n.After(now) && passed < maxCatchUpScan; n = sch.Next(n) {
		latest = n
		passed++
	}
	missed := passed > 1 || now.Sub(*due) > grace
	if missed && catchUp == "none" {
		return p
	}
	p.fire = &latest
	return p
}

func (s *Service) fireSchedule(ctx context.Context, r triggerdb.ListDueSchedulesRow, now time.Time) error {
	var cfg flow.Trigger
	if err := json.Unmarshal(r.Config, &cfg); err != nil {
		return err
	}
	sch, err := ParseSchedule(cfg.Cron, cfg.Timezone)
	if err != nil {
		return err
	}
	plan := planSchedule(sch, cfg.CatchUp, r.NextFireAt, now, s.grace())
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := triggerdb.New(tx)
		n, err := q.AdvanceSchedule(ctx, triggerdb.AdvanceScheduleParams{NextFireAt: &plan.next, FiredAt: plan.fire, ID: r.ID,
			Expected: r.NextFireAt, Holder: s.Holder, Now: now})
		if err != nil || n == 0 {
			return err // not the lease holder, or another tick moved the trigger
		}
		if plan.fire == nil {
			return nil
		}
		// The unique key (trigger_id, scheduled_for) is the last guard (D-14).
		exists, err := q.ScheduledExecutionExists(ctx, triggerdb.ScheduledExecutionExistsParams{TriggerID: &r.ID, ScheduledFor: plan.fire})
		if err != nil || exists {
			return err
		}
		return s.startIsolated(ctx, tx, Start{Namespace: r.Namespace, FlowKey: r.FlowKey, TriggerType: "schedule", TriggerID: r.ID,
			ScheduledFor: plan.fire, Payload: map[string]any{"scheduled_for": plan.fire.UTC().Format(time.RFC3339)}, InputTemplates: cfg.Inputs})
	})
}

// startIsolated starts an execution in a savepoint. A failure, for example an input that
// does not match its type, is logged and audited and does not undo the caller's work.
func (s *Service) startIsolated(ctx context.Context, tx pgx.Tx, st Start) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if _, err := s.Starter.Start(ctx, sp, st); err != nil {
		_ = sp.Rollback(ctx)
		s.Log.Warn("trigger did not start an execution", "trigger", st.TriggerID, "flow", st.Namespace+"/"+st.FlowKey, "err", err)
		return s.Audit.Record(ctx, tx, audit.Event{Action: "trigger.failed", TargetType: "trigger", TargetID: st.TriggerID.String(),
			Details: map[string]any{"flow": st.Namespace + "/" + st.FlowKey, "trigger_type": st.TriggerType, "error": err.Error()}})
	}
	return sp.Commit(ctx)
}

// Ended is an execution that reached a terminal state.
type Ended struct {
	ID         uuid.UUID
	FlowID     *uuid.UUID
	State      string
	Outputs    json.RawMessage
	ChainDepth int
}

// OnEnd fires the flow triggers that name the flow of an ended execution. It runs in the
// transaction that ends the execution (§4.3 step 6, REQ-TRG-005). A trigger without
// states fires on every end state (DI-29).
func (s *Service) OnEnd(ctx context.Context, tx pgx.Tx, e Ended) error {
	if e.FlowID == nil {
		return nil
	}
	q := triggerdb.New(tx)
	name, err := q.GetFlowName(ctx, *e.FlowID)
	if err != nil {
		return err
	}
	upstream := name.Namespace + "/" + name.FlowKey
	downs, err := q.ListDownstreamTriggers(ctx, upstream)
	if err != nil {
		return err
	}
	outputs := map[string]any{}
	if len(e.Outputs) > 0 {
		_ = json.Unmarshal(e.Outputs, &outputs)
	}
	for _, d := range downs {
		var cfg flow.Trigger
		if err := json.Unmarshal(d.Config, &cfg); err != nil {
			return err
		}
		if len(cfg.States) > 0 && !contains(cfg.States, e.State) {
			continue
		}
		depth := e.ChainDepth + 1
		target := d.Namespace + "/" + d.FlowKey
		if depth > MaxChainDepth {
			if err := s.Audit.Record(ctx, tx, audit.Event{Action: "trigger.chain_depth_exceeded", TargetType: "trigger", TargetID: d.ID.String(),
				Details: map[string]any{"flow": target, "upstream_execution": e.ID.String(), "chain_depth": depth}}); err != nil {
				return err
			}
			continue
		}
		payload := map[string]any{"execution_id": e.ID.String(), "state": e.State, "outputs": outputs, "flow": upstream}
		if err := s.startIsolated(ctx, tx, Start{Namespace: d.Namespace, FlowKey: d.FlowKey, TriggerType: "flow", TriggerID: d.ID,
			Payload: payload, InputTemplates: cfg.Inputs, ChainDepth: depth}); err != nil {
			return err
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// droppedHeaders are request headers that are not stored in the trigger payload,
// because they carry credentials of the caller (DI-28).
var droppedHeaders = map[string]bool{"authorization": true, "cookie": true, "proxy-authorization": true}

// FireWebhook starts the flow of a webhook key (REQ-TRG-004, SI-05). A wrong key, a key
// of a removed trigger or a key of a deleted flow is 404.
func (s *Service) FireWebhook(ctx context.Context, key string, body []byte, headers http.Header) (uuid.UUID, error) {
	hash := token.HashSecret(key)
	row, err := triggerdb.New(s.Pool).GetWebhookTrigger(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrWebhookNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if subtle.ConstantTimeCompare(row.WebhookKeyHash, hash) != 1 {
		return uuid.Nil, ErrWebhookNotFound
	}
	switch {
	case row.Disabled:
		return uuid.Nil, ErrFlowDisabled
	case !row.Valid:
		return uuid.Nil, ErrFlowInvalid
	case !row.Active:
		return uuid.Nil, ErrWebhookNotFound
	}
	var cfg flow.Trigger
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return uuid.Nil, err
	}
	var parsed any = string(body)
	if len(body) > 0 && json.Valid(body) {
		_ = json.Unmarshal(body, &parsed)
	}
	hdrs := map[string]any{}
	for k, v := range headers {
		lk := strings.ToLower(k)
		if len(v) > 0 && !droppedHeaders[lk] {
			hdrs[lk] = v[0]
		}
	}
	id, err := s.Starter.StartOwn(ctx, Start{Namespace: row.Namespace, FlowKey: row.FlowKey, TriggerType: "webhook", TriggerID: row.ID,
		Payload: map[string]any{"body": parsed, "headers": hdrs}, InputTemplates: cfg.Inputs})
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE triggers SET last_fired_at = $2 WHERE id = $1", row.ID, s.Clock.Now()); err != nil && s.Log != nil {
		s.Log.Warn("record webhook fire time", "trigger", row.ID, "err", err)
	}
	return id, nil
}

// RotateWebhookKey creates a new key for a webhook trigger and returns it once. The old
// key stops working at once (REQ-TRG-004).
func (s *Service) RotateWebhookKey(ctx context.Context, namespace, flowKey, triggerKey string) (string, error) {
	var key string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := triggerdb.New(tx)
		t, err := q.GetFlowTrigger(ctx, triggerdb.GetFlowTriggerParams{Namespace: namespace, FlowKey: flowKey, TriggerKey: triggerKey})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTriggerNotFound
		}
		if err != nil {
			return err
		}
		if t.Type != "webhook" {
			return ErrNotWebhook
		}
		var hash []byte
		key, hash = token.NewSecret()
		if err := q.SetWebhookKey(ctx, triggerdb.SetWebhookKeyParams{Hash: hash, ID: t.ID}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "trigger.webhook_key_rotate", TargetType: "trigger", TargetID: t.ID.String(),
			Details: map[string]any{"flow": namespace + "/" + flowKey, "trigger": triggerKey}})
	})
	return key, err
}

// WebhookURL returns the public URL of a webhook key.
func (s *Service) WebhookURL(key string) string {
	return strings.TrimRight(s.PublicURL, "/") + "/hooks/" + key
}

// Upcoming is the next fire time of one active schedule.
type Upcoming struct {
	Namespace  string
	FlowKey    string
	TriggerKey string
	Cron       string
	Timezone   string
	NextFireAt time.Time
}

// Upcoming lists the next fire times of active schedules, sorted (REQ-TRG-006). A
// namespace filter includes its child namespaces.
func (s *Service) Upcoming(ctx context.Context, namespace string, limit int) ([]Upcoming, error) {
	rows, err := triggerdb.New(s.Pool).ListUpcomingSchedules(ctx, namespace)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	out := make([]Upcoming, 0, len(rows))
	for _, r := range rows {
		var cfg flow.Trigger
		if json.Unmarshal(r.Config, &cfg) != nil {
			continue
		}
		next := time.Time{}
		if r.NextFireAt != nil {
			next = *r.NextFireAt
		} else if sch, err := ParseSchedule(cfg.Cron, cfg.Timezone); err == nil {
			next = sch.Next(now)
		}
		if next.IsZero() {
			continue
		}
		tz := cfg.Timezone
		if tz == "" {
			tz = "UTC"
		}
		out = append(out, Upcoming{Namespace: r.Namespace, FlowKey: r.FlowKey, TriggerKey: r.TriggerKey, Cron: cfg.Cron, Timezone: tz, NextFireAt: next.UTC()})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.NextFireAt.Equal(b.NextFireAt) {
			return a.NextFireAt.Before(b.NextFireAt)
		}
		return a.Namespace+"/"+a.FlowKey+"/"+a.TriggerKey < b.Namespace+"/"+b.FlowKey+"/"+b.TriggerKey
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
