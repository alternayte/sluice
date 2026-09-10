// Package promx exposes the Prometheus metrics of REQ-CORE-004.
package promx

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Metrics owns the registry and the HTTP duration histogram.
type Metrics struct {
	Registry     *prometheus.Registry
	httpDuration *prometheus.HistogramVec
}

// New creates the registry with the database-backed collectors.
func New(pool *pgxpool.Pool, log *slog.Logger) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		Registry: reg,
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sluice_http_request_duration_seconds",
			Help:    "HTTP request duration by method, route and status.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route", "status"}),
	}
	reg.MustRegister(m.httpDuration, &dbCollector{pool: pool, log: log})
	return m
}

// Handler serves /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// Middleware records request durations. route returns a low-cardinality route label.
func (m *Metrics) Middleware(route func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &httpx.StatusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.Status
		if status == 0 {
			status = http.StatusOK
		}
		m.httpDuration.WithLabelValues(r.Method, route(r), strconv.Itoa(status)).Observe(time.Since(start).Seconds())
	})
}

var (
	execDesc  = prometheus.NewDesc("sluice_executions", "Executions by state.", []string{"state"}, nil)
	taskDesc  = prometheus.NewDesc("sluice_task_runs", "Task runs by state.", []string{"state"}, nil)
	queueDesc = prometheus.NewDesc("sluice_queue_depth", "Queued task runs by pool.", []string{"pool"}, nil)
)

var (
	executionStates = []string{"QUEUED", "RUNNING", "CANCELLING", "SUCCESS", "FAILED", "TIMED_OUT", "CANCELLED", "SKIPPED"}
	taskStates      = []string{"PENDING", "QUEUED", "RUNNING", "SUCCESS", "FAILED", "TIMED_OUT", "CANCELLED", "SKIPPED"}
)

type dbCollector struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- execDesc
	ch <- taskDesc
	ch <- queueDesc
}

func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.countBy(ctx, ch, execDesc, "SELECT state, count(*) FROM executions GROUP BY state", executionStates)
	c.countBy(ctx, ch, taskDesc, "SELECT state, count(*) FROM task_runs GROUP BY state", taskStates)
	c.countBy(ctx, ch, queueDesc, "SELECT pool, count(*) FROM task_runs WHERE state = 'QUEUED' GROUP BY pool", nil)
}

func (c *dbCollector) countBy(ctx context.Context, ch chan<- prometheus.Metric, desc *prometheus.Desc, q string, all []string) {
	counts := map[string]float64{}
	for _, s := range all {
		counts[s] = 0
	}
	rows, err := c.pool.Query(ctx, q)
	if err != nil {
		c.log.Warn("metrics query failed", "err", err)
		return
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err == nil {
			counts[k] = float64(n)
		}
	}
	rows.Close()
	if len(counts) == 0 {
		counts["default"] = 0
	}
	for k, v := range counts {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, k)
	}
}
