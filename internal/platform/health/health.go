// Package health serves /healthz and /readyz (REQ-CORE-004).
package health

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Check is one named readiness check.
type Check struct {
	Name string
	Func func(ctx context.Context) error
}

// Checker runs readiness checks.
type Checker struct {
	mu     sync.RWMutex
	checks []Check
}

// Add registers a readiness check.
func (c *Checker) Add(name string, fn func(ctx context.Context) error) {
	c.mu.Lock()
	c.checks = append(c.checks, Check{Name: name, Func: fn})
	c.mu.Unlock()
}

// Result is the readiness response body.
type Result struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
	Failed []string          `json:"failed,omitempty"`
}

// Run executes all checks in parallel with a timeout.
func (c *Checker) Run(ctx context.Context) Result {
	c.mu.RLock()
	checks := append([]Check(nil), c.checks...)
	c.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res := Result{Status: "ok", Checks: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ch := range checks {
		wg.Add(1)
		go func(ch Check) {
			defer wg.Done()
			msg := "ok"
			if err := ch.Func(ctx); err != nil {
				msg = err.Error()
			}
			mu.Lock()
			res.Checks[ch.Name] = msg
			if msg != "ok" {
				res.Failed = append(res.Failed, ch.Name)
			}
			mu.Unlock()
		}(ch)
	}
	wg.Wait()
	if len(res.Failed) > 0 {
		sort.Strings(res.Failed)
		res.Status = "fail"
	}
	return res
}

// Healthz reports that the process is alive.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readyz runs all checks and returns 200 or 503.
func (c *Checker) Readyz(w http.ResponseWriter, r *http.Request) {
	res := c.Run(r.Context())
	status := http.StatusOK
	if res.Status != "ok" {
		status = http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, status, res)
}
