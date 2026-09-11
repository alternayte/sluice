//go:build integration

package app

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/namespace"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/lease"
	"github.com/alternayte/sluice/internal/platform/logging"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
)

// fakeClockServer builds a server on dbURL with a fake clock. It does not run.
func fakeClockServer(t *testing.T, dbURL string, clk clock.Clock) *Server {
	t.Helper()
	cfg, err := LoadConfig(LoadOptions{Server: true, Env: map[string]string{
		"SLUICE_DATABASE_URL": dbURL,
		"SLUICE_PUBLIC_URL":   "http://127.0.0.1:8080",
	}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := newServer(context.Background(), cfg, logging.New(io.Discard, "error", "text"), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Pool.Close)
	return s
}

// saveFlows creates a managed namespace and saves the files as one version.
func saveFlows(t *testing.T, s *Server, ns string, files map[string]string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Namespaces.Create(ctx, ns, ""); err != nil {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
	var changes []namespace.Change
	for p, src := range files {
		changes = append(changes, namespace.Change{Op: "put", Path: p, Content: []byte(src)})
	}
	if _, err := s.Namespaces.Save(ctx, ns, changes, "flows", nil); err != nil {
		t.Fatalf("save %s: %v", ns, err)
	}
}

// lead makes s the only holder of the scheduler lease at the current fake time.
func lead(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, "DELETE FROM leases WHERE name = $1", lease.Scheduler); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Leases.TryAcquire(ctx, lease.Scheduler); err != nil || !ok {
		t.Fatalf("acquire scheduler lease: %v %v", ok, err)
	}
}

// fired returns the scheduled_for times of the schedule executions of a flow.
func fired(t *testing.T, s *Server, ns, flowKey string) []time.Time {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `SELECT e.scheduled_for FROM executions e
		JOIN flows f ON f.id = e.flow_id JOIN namespaces n ON n.id = f.namespace_id
		WHERE n.name = $1 AND f.flow_key = $2 AND e.trigger_type = 'schedule' ORDER BY e.scheduled_for`, ns, flowKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			t.Fatal(err)
		}
		out = append(out, ts.UTC())
	}
	return out
}

func tick(t *testing.T, s *Server) {
	t.Helper()
	if err := s.Triggers.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

func wantTimes(t *testing.T, got []time.Time, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fired %v, want %v", got, want)
	}
	for i, w := range want {
		wt, _ := time.Parse(time.RFC3339, w)
		if !got[i].Equal(wt) {
			t.Fatalf("fire %d at %s, want %s (all: %v)", i, got[i].Format(time.RFC3339), w, got)
		}
	}
}

// TestSCN_TRG_002_DSTSchedule runs `30 2 * * *` in Europe/Zurich over the spring and
// autumn DST dates of 2026. It fires once per day at the correct instant (REQ-TRG-002).
func TestSCN_TRG_002_DSTSchedule(t *testing.T) {
	flowSrc := "id: dst\ntriggers:\n  - {id: night, type: schedule, cron: \"30 2 * * *\", timezone: Europe/Zurich}\n" +
		"tasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	cases := []struct {
		name, from, to string
		want           []string
	}{
		// Spring forward on 29 March: 02:30 does not exist and fires at 03:30 CEST.
		{"spring", "2026-03-27T00:00:00Z", "2026-03-31T00:00:00Z",
			[]string{"2026-03-27T01:30:00Z", "2026-03-28T01:30:00Z", "2026-03-29T01:30:00Z", "2026-03-30T00:30:00Z"}},
		// Autumn back on 25 October: 02:30 exists twice and fires once, at 02:30 CEST.
		{"autumn", "2026-10-23T00:00:00Z", "2026-10-27T00:00:00Z",
			[]string{"2026-10-23T00:30:00Z", "2026-10-24T00:30:00Z", "2026-10-25T00:30:00Z", "2026-10-26T01:30:00Z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, _ := time.Parse(time.RFC3339, c.from)
			to, _ := time.Parse(time.RFC3339, c.to)
			clk := clock.NewFake(from)
			s := fakeClockServer(t, pgtest.Shared(t).NewDatabase(t), clk)
			saveFlows(t, s, "dst", map[string]string{"dst.flow.yaml": flowSrc})
			lead(t, s)
			for !clk.Now().After(to) {
				if _, err := s.Leases.TryAcquire(context.Background(), lease.Scheduler); err != nil {
					t.Fatal(err)
				}
				tick(t, s)
				clk.Advance(15 * time.Minute)
			}
			wantTimes(t, fired(t, s, "dst", "dst"), c.want...)
		})
	}
}

// TestSCN_TRG_003_CatchUp stops the scheduler over three fire times. catch_up last creates
// one execution for the latest time, none creates none (REQ-TRG-002).
func TestSCN_TRG_003_CatchUp(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	s := fakeClockServer(t, pgtest.Shared(t).NewDatabase(t), clk)
	task := "tasks:\n  - {id: t, type: command, command: [\"true\"]}\n"
	saveFlows(t, s, "catch", map[string]string{
		"last.flow.yaml": "id: last\ntriggers:\n  - {id: h, type: schedule, cron: \"0 * * * *\", catch_up: last}\n" + task,
		"none.flow.yaml": "id: none\ntriggers:\n  - {id: h, type: schedule, cron: \"0 * * * *\", catch_up: none}\n" + task,
	})
	lead(t, s)
	tick(t, s) // sets the next fire time to 01:00

	clk.Set(time.Date(2026, 5, 1, 3, 10, 0, 0, time.UTC)) // down over 01:00, 02:00 and 03:00
	lead(t, s)
	tick(t, s)
	wantTimes(t, fired(t, s, "catch", "last"), "2026-05-01T03:00:00Z")
	wantTimes(t, fired(t, s, "catch", "none"))

	clk.Set(time.Date(2026, 5, 1, 4, 0, 0, 0, time.UTC))
	lead(t, s)
	tick(t, s)
	wantTimes(t, fired(t, s, "catch", "last"), "2026-05-01T03:00:00Z", "2026-05-01T04:00:00Z")
	wantTimes(t, fired(t, s, "catch", "none"), "2026-05-01T04:00:00Z")
}

// TestSCN_TRG_004_ExactlyOnce runs three instances with a forced leader change every 2
// fake seconds over 60 fake minutes of `* * * * *`. Every instance ticks at each step.
// Exactly 60 executions exist (REQ-TRG-003, D-14).
func TestSCN_TRG_004_ExactlyOnce(t *testing.T) {
	dbURL := pgtest.Shared(t).NewDatabase(t)
	clk := clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	servers := []*Server{fakeClockServer(t, dbURL, clk), fakeClockServer(t, dbURL, clk), fakeClockServer(t, dbURL, clk)}
	saveFlows(t, servers[0], "once", map[string]string{
		"every.flow.yaml": "id: every\ntriggers:\n  - {id: m, type: schedule, cron: \"* * * * *\"}\ntasks:\n  - {id: t, type: command, command: [\"true\"]}\n",
	})
	end := clk.Now().Add(time.Hour)
	for step := 0; !clk.Now().After(end); step++ {
		lead(t, servers[step%len(servers)])
		for _, s := range servers {
			tick(t, s)
		}
		clk.Advance(2 * time.Second)
	}
	got := fired(t, servers[0], "once", "every")
	if len(got) != 60 {
		t.Fatalf("%d executions, want 60", len(got))
	}
	for i, ts := range got {
		if want := time.Date(2026, 6, 1, 0, i+1, 0, 0, time.UTC); !ts.Equal(want) {
			t.Fatalf("execution %d scheduled for %s, want %s", i, ts, want)
		}
	}
}
