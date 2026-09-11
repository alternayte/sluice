// Package trigger fires schedule, webhook and flow triggers (REQ-TRG-002 to REQ-TRG-006).
package trigger

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/alternayte/sluice/internal/flow"
)

// starBit marks a cron field written as `*`. It has the value of the unexported
// starBit of robfig/cron.
const starBit = 1 << 63

// maxScanDays bounds the search for the next fire time, for example for `0 0 30 2 *`.
const maxScanDays = 5 * 366

// Schedule is a cron expression in a time zone (§6.5).
type Schedule struct {
	spec *cron.SpecSchedule
	loc  *time.Location
}

// ParseSchedule parses a cron expression and an IANA time zone. An empty zone is UTC.
func ParseSchedule(expr, tz string) (*Schedule, error) {
	s, err := flow.ParseCron(expr)
	if err != nil {
		return nil, err
	}
	spec, ok := s.(*cron.SpecSchedule)
	if !ok {
		return nil, fmt.Errorf("unsupported cron %q", expr)
	}
	loc := time.UTC
	if tz != "" {
		if loc, err = time.LoadLocation(tz); err != nil {
			return nil, err
		}
	}
	return &Schedule{spec: spec, loc: loc}, nil
}

// Next returns the first fire time after t in UTC, or the zero time when no time
// matches within five years (REQ-TRG-002, DI-26). A wall time that does not exist
// because of a DST change fires at the same wall time with the offset before the
// change, that is one gap later. A wall time that exists twice fires once, at the
// first instant.
func (s *Schedule) Next(after time.Time) time.Time {
	y, m, d := after.In(s.loc).Date()
	first := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxScanDays; i++ {
		day := first.AddDate(0, 0, i)
		if !s.dayMatches(day) {
			continue
		}
		// Collect the earliest match of the day: a wall time in a DST gap can map to an
		// instant after a later wall time of the same day.
		var best time.Time
		for h := 0; h < 24; h++ {
			if s.spec.Hour&(1<<uint(h)) == 0 {
				continue
			}
			for mi := 0; mi < 60; mi++ {
				if s.spec.Minute&(1<<uint(mi)) == 0 {
					continue
				}
				t := resolve(day.Year(), day.Month(), day.Day(), h, mi, s.loc)
				if t.After(after) && (best.IsZero() || t.Before(best)) {
					best = t
				}
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{}
}

// dayMatches applies the month, day-of-month and day-of-week fields with the cron rule:
// when both day fields are restricted, a day matches either field.
func (s *Schedule) dayMatches(day time.Time) bool {
	if s.spec.Month&(1<<uint(day.Month())) == 0 {
		return false
	}
	dom := s.spec.Dom&(1<<uint(day.Day())) != 0
	dow := s.spec.Dow&(1<<uint(day.Weekday())) != 0
	if s.spec.Dom&starBit != 0 || s.spec.Dow&starBit != 0 {
		return dom && dow
	}
	return dom || dow
}

// resolve returns the earliest instant with the wall time y-mo-d h:mi in loc. In a DST
// gap it uses the offset before the change.
func resolve(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	naive := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	before := naive.Add(-36 * time.Hour)
	var best time.Time
	for _, probe := range []time.Time{before, naive, naive.Add(36 * time.Hour)} {
		_, off := probe.In(loc).Zone()
		t := naive.Add(-time.Duration(off) * time.Second)
		lt := t.In(loc)
		if lt.Year() == y && lt.Month() == mo && lt.Day() == d && lt.Hour() == h && lt.Minute() == mi {
			if best.IsZero() || t.Before(best) {
				best = t
			}
		}
	}
	if best.IsZero() {
		_, off := before.In(loc).Zone()
		best = naive.Add(-time.Duration(off) * time.Second)
	}
	return best.UTC()
}
