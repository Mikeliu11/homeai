package pricing

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Household timezone. One household, one zone (spec §3).
const HouseholdTZ = "America/New_York"

// NY returns the household location.
func NY() *time.Location {
	loc, err := time.LoadLocation(HouseholdTZ)
	if err != nil {
		panic(err)
	}
	return loc
}

var clockRe = regexp.MustCompile(`^([01]?\d|2[0-4]):([0-5]\d)$`)

// Clock is "HH:MM" in 24h local time. "24:00" means end of day.
type Clock = string

func parseClock(c Clock) (h, m int, err error) {
	mm := clockRe.FindStringSubmatch(c)
	if mm == nil {
		return 0, 0, fmt.Errorf("bad clock %q (want HH:MM)", c)
	}
	h, _ = strconv.Atoi(mm[1])
	m, _ = strconv.Atoi(mm[2])
	if h == 24 && m != 0 {
		return 0, 0, fmt.Errorf("bad clock %q", c)
	}
	return h, m, nil
}

// Span is a half-open local interval [Start, End).
type Span struct {
	Start, End time.Time
}

// Minutes is the span length.
func (s Span) Minutes() int { return int(s.End.Sub(s.Start) / time.Minute) }

// Date is the local date of the span's start.
func (s Span) Date() string { return s.Start.Format("2006-01-02") }

// ParseDate parses YYYY-MM-DD in the household zone.
func ParseDate(d string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", d, loc)
}

// SpanOnDate builds a span on a date. An end at or before the start rolls to the next day.
func SpanOnDate(date string, start, end Clock, loc *time.Location) (Span, error) {
	day, err := ParseDate(date, loc)
	if err != nil {
		return Span{}, err
	}
	sh, sm, err := parseClock(start)
	if err != nil {
		return Span{}, err
	}
	eh, em, err := parseClock(end)
	if err != nil {
		return Span{}, err
	}
	st := clockOn(day, 0, sh, sm)
	en := clockOn(day, 0, eh, em)
	if !en.After(st) {
		en = clockOn(day, 1, eh, em)
	}
	return Span{Start: st, End: en}, nil
}

// clockOn is the wall-clock time h:m on day's date plus addDays, in day's zone.
// Built with time.Date, never day.Add: on a daylight-saving day the hours since
// midnight are not the clock time. 24:00 is the next midnight.
func clockOn(day time.Time, addDays, h, m int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day()+addDays, h, m, 0, 0, day.Location())
}

// SplitAtMidnight cuts a span into per-date pieces.
func (s Span) SplitAtMidnight() []Span {
	var out []Span
	cur := s.Start
	for cur.Before(s.End) {
		next := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, cur.Location())
		if next.After(s.End) {
			next = s.End
		}
		out = append(out, Span{Start: cur, End: next})
		cur = next
	}
	return out
}
