package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// KPIsOut are the dashboard cards.
type KPIsOut struct {
	Executions       int      `json:"executions" doc:"Executions that ended in the range."`
	Succeeded        int      `json:"succeeded"`
	Failed           int      `json:"failed"`
	TimedOut         int      `json:"timed_out"`
	Cancelled        int      `json:"cancelled"`
	Skipped          int      `json:"skipped"`
	Running          int      `json:"running" doc:"Executions that run now."`
	SuccessRate      *float64 `json:"success_rate" nullable:"true" doc:"SUCCESS / (SUCCESS + FAILED + TIMED_OUT) in the range."`
	MedianDurationMs *float64 `json:"median_duration_ms" nullable:"true"`
}

// BucketOut is one time bucket.
type BucketOut struct {
	Start     time.Time `json:"start"`
	Success   int       `json:"success"`
	Failed    int       `json:"failed"`
	TimedOut  int       `json:"timed_out"`
	Cancelled int       `json:"cancelled"`
	Skipped   int       `json:"skipped"`
	P50Ms     *float64  `json:"p50_ms" nullable:"true"`
	P95Ms     *float64  `json:"p95_ms" nullable:"true"`
}

// ExecutionOut is one execution of the dashboard tables and flow charts.
type ExecutionOut struct {
	ID            uuid.UUID  `json:"id"`
	Namespace     string     `json:"namespace"`
	FlowID        string     `json:"flow_id"`
	State         string     `json:"state"`
	TriggerType   string     `json:"trigger_type"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty" nullable:"true"`
	EndedAt       *time.Time `json:"ended_at,omitempty" nullable:"true"`
	DurationMs    *int64     `json:"duration_ms,omitempty" nullable:"true"`
	Error         string     `json:"error"`
	TriageSummary *string    `json:"triage_summary,omitempty" nullable:"true"`
}

// DashboardOut is the dashboard data (REQ-UI-003).
type DashboardOut struct {
	Range          string         `json:"range" enum:"24h,7d,30d"`
	From           time.Time      `json:"from"`
	To             time.Time      `json:"to"`
	BucketSeconds  int            `json:"bucket_seconds"`
	KPIs           KPIsOut        `json:"kpis"`
	Buckets        []BucketOut    `json:"buckets"`
	Running        []ExecutionOut `json:"running"`
	RecentFailures []ExecutionOut `json:"recent_failures"`
}

// FlowStatsOut holds the last 50 executions of a flow, newest first.
type FlowStatsOut struct {
	Recent []ExecutionOut `json:"recent"`
}

// MetricPointOut is one aggregated value.
type MetricPointOut struct {
	ExecutionID uuid.UUID `json:"execution_id"`
	CreatedAt   time.Time `json:"created_at"`
	Value       float64   `json:"value"`
}

// MetricSeriesOut is one group of a metric chart.
type MetricSeriesOut struct {
	Group  string           `json:"group" doc:"Value of the group-by tag. Empty when the tag is missing or no group-by is set."`
	Points []MetricPointOut `json:"points"`
}

// FlowMetricsOut is the custom metric chart of a flow.
type FlowMetricsOut struct {
	Names  []string          `json:"names"`
	Series []MetricSeriesOut `json:"series"`
}

func executionsOut(list []Execution) []ExecutionOut {
	out := make([]ExecutionOut, 0, len(list))
	for _, x := range list {
		out = append(out, ExecutionOut(x))
	}
	return out
}

type flowIn struct {
	Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
	FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
}

// Routes registers the dashboard and flow chart operations. Every role reads them (Appendix B).
func Routes(api huma.API, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)

	huma.Register(api, httpx.Op("getDashboard", http.MethodGet, "/api/v1/stats/dashboard", viewer),
		func(ctx context.Context, in *struct {
			Range     string `query:"range" enum:"24h,7d,30d" default:"24h"`
			Namespace string `query:"namespace" doc:"Namespace and its children."`
		}) (*struct{ Body DashboardOut }, error) {
			d, err := s.Dashboard(ctx, in.Range, in.Namespace)
			if err != nil {
				return nil, err
			}
			out := DashboardOut{Range: d.Range, From: d.From, To: d.To, BucketSeconds: int(d.Bucket / time.Second), KPIs: KPIsOut(d.KPIs),
				Buckets: make([]BucketOut, 0, len(d.Buckets)), Running: executionsOut(d.Running), RecentFailures: executionsOut(d.RecentFailures)}
			for _, b := range d.Buckets {
				out.Buckets = append(out.Buckets, BucketOut(b))
			}
			return &struct{ Body DashboardOut }{Body: out}, nil
		})

	huma.Register(api, httpx.Op("getFlowStats", http.MethodGet, "/api/v1/flows/{namespace}/{flowId}/stats", viewer),
		func(ctx context.Context, in *flowIn) (*struct{ Body FlowStatsOut }, error) {
			list, err := s.FlowRecent(ctx, in.Namespace, in.FlowID)
			if err != nil {
				return nil, err
			}
			return &struct{ Body FlowStatsOut }{Body: FlowStatsOut{Recent: executionsOut(list)}}, nil
		})

	huma.Register(api, httpx.Op("getFlowMetrics", http.MethodGet, "/api/v1/flows/{namespace}/{flowId}/metrics", viewer),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			Name      string `query:"name" maxLength:"100" doc:"Metric name. Empty returns only the names."`
			Agg       string `query:"agg" enum:"sum,avg,max" default:"sum"`
			GroupBy   string `query:"group_by" maxLength:"100" doc:"Tag key. One series per tag value."`
		}) (*struct{ Body FlowMetricsOut }, error) {
			names, series, err := s.FlowMetric(ctx, in.Namespace, in.FlowID, in.Name, in.Agg, in.GroupBy)
			if err != nil {
				return nil, err
			}
			out := FlowMetricsOut{Names: names, Series: make([]MetricSeriesOut, 0, len(series))}
			for _, sr := range series {
				so := MetricSeriesOut{Group: sr.Group, Points: make([]MetricPointOut, 0, len(sr.Points))}
				for _, p := range sr.Points {
					so.Points = append(so.Points, MetricPointOut(p))
				}
				out.Series = append(out.Series, so)
			}
			return &struct{ Body FlowMetricsOut }{Body: out}, nil
		})
}
