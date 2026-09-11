package trigger_test

import (
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/trigger"
)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestScheduleNext(t *testing.T) {
	cases := []struct {
		name, cron, tz, after string
		want                  []string
	}{
		{"zurich spring forward fires one gap later", "30 2 * * *", "Europe/Zurich", "2026-03-27T12:00:00Z",
			[]string{"2026-03-28T01:30:00Z", "2026-03-29T01:30:00Z", "2026-03-30T00:30:00Z"}},
		{"zurich autumn back fires once at the first instant", "30 2 * * *", "Europe/Zurich", "2026-10-24T12:00:00Z",
			[]string{"2026-10-25T00:30:00Z", "2026-10-26T01:30:00Z"}},
		{"gap time does not hide an earlier time of the day", "0,30 2,3 * * *", "Europe/Zurich", "2026-03-28T23:00:00Z",
			[]string{"2026-03-29T01:00:00Z", "2026-03-29T01:30:00Z", "2026-03-30T00:00:00Z"}},
		{"hourly", "@hourly", "", "2026-01-01T10:15:00Z",
			[]string{"2026-01-01T11:00:00Z", "2026-01-01T12:00:00Z"}},
		{"monthly", "@monthly", "UTC", "2026-01-15T00:00:00Z",
			[]string{"2026-02-01T00:00:00Z", "2026-03-01T00:00:00Z"}},
		{"restricted day fields match either field", "0 0 13 * 5", "", "2026-02-01T00:00:00Z",
			[]string{"2026-02-06T00:00:00Z", "2026-02-13T00:00:00Z", "2026-02-20T00:00:00Z"}},
		{"every minute", "* * * * *", "", "2026-01-01T00:00:30Z",
			[]string{"2026-01-01T00:01:00Z", "2026-01-01T00:02:00Z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := trigger.ParseSchedule(c.cron, c.tz)
			if err != nil {
				t.Fatal(err)
			}
			at := utc(c.after)
			for _, w := range c.want {
				at = s.Next(at)
				if !at.Equal(utc(w)) {
					t.Fatalf("next %s, want %s", at.Format(time.RFC3339), w)
				}
			}
		})
	}
}

func TestScheduleNeverMatches(t *testing.T) {
	s, err := trigger.ParseSchedule("0 0 30 2 *", "")
	if err != nil {
		t.Fatal(err)
	}
	if n := s.Next(utc("2026-01-01T00:00:00Z")); !n.IsZero() {
		t.Fatalf("next %s, want none", n)
	}
}
