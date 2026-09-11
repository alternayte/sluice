// Package metrics serves the dashboard aggregates and the flow charts (REQ-UI-003, REQ-UI-006).
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// ErrFlowNotFound is returned for an unknown flow.
var ErrFlowNotFound = httpx.Errorf(http.StatusNotFound, "flow_not_found", "flow not found")

// RecentLimit is the number of executions of the flow charts (REQ-UI-006).
const RecentLimit = 50

// Service computes aggregates from the executions and metrics tables.
type Service struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
}

// Range is a dashboard time range with its bucket size (REQ-UI-003).
type Range struct {
	Name   string
	Span   time.Duration
	Bucket time.Duration
}

// Ranges are the dashboard ranges: hourly buckets for 24 h, daily buckets for 7 d and 30 d.
var Ranges = map[string]Range{
	"24h": {Name: "24h", Span: 24 * time.Hour, Bucket: time.Hour},
	"7d":  {Name: "7d", Span: 7 * 24 * time.Hour, Bucket: 24 * time.Hour},
	"30d": {Name: "30d", Span: 30 * 24 * time.Hour, Bucket: 24 * time.Hour},
}

// KPIs are the dashboard cards. Executions counts the executions that ended in the range.
// The success rate is SUCCESS / (SUCCESS + FAILED + TIMED_OUT) (DI-36).
type KPIs struct {
	Executions       int
	Succeeded        int
	Failed           int
	TimedOut         int
	Cancelled        int
	Skipped          int
	Running          int
	SuccessRate      *float64
	MedianDurationMs *float64
}

// Bucket is one time bucket of ended executions by state, with duration percentiles.
type Bucket struct {
	Start     time.Time
	Success   int
	Failed    int
	TimedOut  int
	Cancelled int
	Skipped   int
	P50Ms     *float64
	P95Ms     *float64
}

// Execution is one execution of the dashboard tables and the flow charts.
type Execution struct {
	ID            uuid.UUID
	Namespace     string
	FlowID        string
	State         string
	TriggerType   string
	CreatedAt     time.Time
	StartedAt     *time.Time
	EndedAt       *time.Time
	DurationMs    *int64
	Error         string
	TriageSummary *string
}

// Dashboard is the dashboard data of one range.
type Dashboard struct {
	Range          string
	From, To       time.Time
	Bucket         time.Duration
	KPIs           KPIs
	Buckets        []Bucket
	Running        []Execution
	RecentFailures []Execution
}

// nsFilter returns a condition on n.name for a namespace and its children, or "".
func nsFilter(namespace string, args *[]any) string {
	if namespace == "" {
		return ""
	}
	*args = append(*args, namespace)
	p := fmt.Sprintf("$%d", len(*args))
	return fmt.Sprintf(" AND (n.name = %s OR starts_with(n.name, %s || '.'))", p, p)
}

// Dashboard returns the aggregates of a range, optionally for a namespace and its children.
func (s *Service) Dashboard(ctx context.Context, rangeName, namespace string) (Dashboard, error) {
	r, ok := Ranges[rangeName]
	if !ok {
		return Dashboard{}, httpx.Validation(httpx.FieldError{Field: "range", Message: "use 24h, 7d or 30d"})
	}
	now := s.Clock.Now().UTC()
	to := now.Truncate(r.Bucket).Add(r.Bucket)
	from := to.Add(-r.Span)
	d := Dashboard{Range: r.Name, From: from, To: to, Bucket: r.Bucket}
	byStart := map[time.Time]*Bucket{}
	for t := from; t.Before(to); t = t.Add(r.Bucket) {
		b := &Bucket{Start: t}
		d.Buckets = append(d.Buckets, *b)
		byStart[t] = b
	}

	args := []any{from, to, r.Bucket}
	ns := nsFilter(namespace, &args)
	base := ` FROM executions e JOIN namespaces n ON n.id = e.namespace_id
		WHERE e.ended_at >= $1 AND e.ended_at < $2 AND e.state IN ('SUCCESS', 'FAILED', 'TIMED_OUT', 'CANCELLED', 'SKIPPED')` + ns
	rows, err := s.Pool.Query(ctx, `SELECT date_bin($3::interval, e.ended_at, $1) AS b, e.state, count(*)`+base+` GROUP BY 1, 2`, args...)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var start time.Time
		var state string
		var n int
		if err := rows.Scan(&start, &state, &n); err != nil {
			rows.Close()
			return d, err
		}
		b := byStart[start.UTC()]
		if b == nil {
			continue
		}
		d.KPIs.Executions += n
		switch state {
		case "SUCCESS":
			b.Success += n
			d.KPIs.Succeeded += n
		case "FAILED":
			b.Failed += n
			d.KPIs.Failed += n
		case "TIMED_OUT":
			b.TimedOut += n
			d.KPIs.TimedOut += n
		case "CANCELLED":
			b.Cancelled += n
			d.KPIs.Cancelled += n
		case "SKIPPED":
			b.Skipped += n
			d.KPIs.Skipped += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}

	durBase := ` FROM executions e JOIN namespaces n ON n.id = e.namespace_id
		WHERE e.ended_at >= $1 AND e.ended_at < $2 AND e.state IN ('SUCCESS', 'FAILED', 'TIMED_OUT') AND e.duration_ms IS NOT NULL` + ns
	rows, err = s.Pool.Query(ctx, `SELECT date_bin($3::interval, e.ended_at, $1) AS b,
		percentile_cont(0.5) WITHIN GROUP (ORDER BY e.duration_ms), percentile_cont(0.95) WITHIN GROUP (ORDER BY e.duration_ms)`+durBase+` GROUP BY 1`, args...)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var start time.Time
		var p50, p95 float64
		if err := rows.Scan(&start, &p50, &p95); err != nil {
			rows.Close()
			return d, err
		}
		if b := byStart[start.UTC()]; b != nil {
			b.P50Ms, b.P95Ms = &p50, &p95
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	for i := range d.Buckets {
		d.Buckets[i] = *byStart[d.Buckets[i].Start]
	}
	var median *float64
	if err := s.Pool.QueryRow(ctx, `SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY e.duration_ms)`+durBase+` AND $3::interval IS NOT NULL`, args...).Scan(&median); err != nil {
		return d, err
	}
	d.KPIs.MedianDurationMs = median
	if denom := d.KPIs.Succeeded + d.KPIs.Failed + d.KPIs.TimedOut; denom > 0 {
		rate := float64(d.KPIs.Succeeded) / float64(denom)
		d.KPIs.SuccessRate = &rate
	}

	nsArgs := []any{}
	nsOnly := nsFilter(namespace, &nsArgs)
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM executions e JOIN namespaces n ON n.id = e.namespace_id
		WHERE e.state IN ('RUNNING', 'CANCELLING')`+nsOnly, nsArgs...).Scan(&d.KPIs.Running); err != nil {
		return d, err
	}
	if d.Running, err = s.executions(ctx, `WHERE e.state IN ('RUNNING', 'CANCELLING')`+nsOnly+` ORDER BY e.started_at NULLS LAST, e.id LIMIT 20`, nsArgs...); err != nil {
		return d, err
	}
	if d.RecentFailures, err = s.executions(ctx, `WHERE e.state IN ('FAILED', 'TIMED_OUT')`+nsOnly+` ORDER BY e.ended_at DESC NULLS LAST, e.id DESC LIMIT 10`, nsArgs...); err != nil {
		return d, err
	}
	return d, nil
}

// executions lists executions with the latest triage summary.
func (s *Service) executions(ctx context.Context, where string, args ...any) ([]Execution, error) {
	rows, err := s.Pool.Query(ctx, `SELECT e.id, n.name, coalesce(f.flow_key, ''), e.state, e.trigger_type, e.created_at, e.started_at, e.ended_at,
		e.duration_ms, e.error, i.summary
		FROM executions e JOIN namespaces n ON n.id = e.namespace_id LEFT JOIN flows f ON f.id = e.flow_id
		LEFT JOIN LATERAL (SELECT x.summary FROM ai_insights x WHERE x.execution_id = e.id AND coalesce(x.summary, '') <> ''
			ORDER BY x.created_at DESC LIMIT 1) i ON true `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Execution{}
	for rows.Next() {
		var x Execution
		if err := rows.Scan(&x.ID, &x.Namespace, &x.FlowID, &x.State, &x.TriggerType, &x.CreatedAt, &x.StartedAt, &x.EndedAt,
			&x.DurationMs, &x.Error, &x.TriageSummary); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) flowID(ctx context.Context, namespace, flowKey string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT f.id FROM flows f JOIN namespaces n ON n.id = f.namespace_id
		WHERE n.name = $1 AND f.flow_key = $2 AND f.deleted_at IS NULL AND n.deleted_at IS NULL`, namespace, flowKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return id, ErrFlowNotFound
	}
	return id, err
}

// FlowRecent returns the last 50 executions of a flow, newest first (REQ-UI-006).
func (s *Service) FlowRecent(ctx context.Context, namespace, flowKey string) ([]Execution, error) {
	id, err := s.flowID(ctx, namespace, flowKey)
	if err != nil {
		return nil, err
	}
	return s.executions(ctx, `WHERE e.flow_id = $1 ORDER BY e.created_at DESC, e.id DESC LIMIT $2`, id, RecentLimit)
}

// MetricPoint is the aggregated value of one metric in one execution.
type MetricPoint struct {
	ExecutionID uuid.UUID
	CreatedAt   time.Time
	Value       float64
}

// MetricSeries is the points of one group (one value of the group-by tag).
type MetricSeries struct {
	Group  string
	Points []MetricPoint
}

// aggregations maps the API aggregation to SQL.
var aggregations = map[string]string{"sum": "sum", "avg": "avg", "max": "max"}

// FlowMetric returns the metric names of a flow and, for a name, one series per value of
// the group-by tag over the last 50 executions (REQ-UI-006).
func (s *Service) FlowMetric(ctx context.Context, namespace, flowKey, name, agg, groupBy string) ([]string, []MetricSeries, error) {
	id, err := s.flowID(ctx, namespace, flowKey)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT name FROM metrics WHERE flow_id = $1 ORDER BY name LIMIT 200`, id)
	if err != nil {
		return nil, nil, err
	}
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	series := []MetricSeries{}
	if name == "" {
		return names, series, nil
	}
	fn, ok := aggregations[agg]
	if !ok {
		return nil, nil, httpx.Validation(httpx.FieldError{Field: "agg", Message: "use sum, avg or max"})
	}
	rows, err = s.Pool.Query(ctx, `WITH recent AS (SELECT id, created_at FROM executions WHERE flow_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2)
		SELECT r.id, r.created_at, coalesce(m.tags ->> $4, '') AS g, `+fn+`(m.value)
		FROM recent r JOIN metrics m ON m.execution_id = r.id AND m.name = $3
		GROUP BY r.id, r.created_at, g ORDER BY r.created_at, g`, id, RecentLimit, name, groupBy)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byGroup := map[string]*MetricSeries{}
	for rows.Next() {
		var p MetricPoint
		var g string
		if err := rows.Scan(&p.ExecutionID, &p.CreatedAt, &g, &p.Value); err != nil {
			return nil, nil, err
		}
		sr := byGroup[g]
		if sr == nil {
			sr = &MetricSeries{Group: g}
			byGroup[g] = sr
		}
		sr.Points = append(sr.Points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, sr := range byGroup {
		series = append(series, *sr)
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Group < series[j].Group })
	return names, series, nil
}
