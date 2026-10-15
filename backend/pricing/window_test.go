package pricing

import (
	"testing"
	"time"
)

func TestWindowSplit(t *testing.T) {
	loc := NY()
	late := Window{From: "22:00"} // 22:00 to end of day
	s, _ := SpanOnDate("2026-09-04", "18:00", "23:30", loc)
	in, out := SplitSpanByWindow(s, late)
	if sum(in) != 90 || sum(out) != 240 {
		t.Errorf("late: in=%d out=%d", sum(in), sum(out))
	}

	sunday := Window{Days: []Day{"sun"}}
	s, _ = SpanOnDate("2026-09-12", "23:00", "01:00", loc) // Sat 23:00 → Sun 01:00
	in, out = SplitSpanByWindow(s, sunday)
	if sum(in) != 60 || sum(out) != 60 {
		t.Errorf("sunday: in=%d out=%d", sum(in), sum(out))
	}

	crossing := Window{From: "22:00", To: "02:00"} // crosses midnight
	s, _ = SpanOnDate("2026-09-12", "21:00", "03:00", loc)
	in, out = SplitSpanByWindow(s, crossing)
	if sum(in) != 240 || sum(out) != 120 {
		t.Errorf("crossing: in=%d out=%d", sum(in), sum(out))
	}

	holiday := Window{Dates: "us_federal_holidays"}
	s, _ = SpanOnDate("2026-09-07", "12:00", "13:00", loc) // Labor Day
	in, _ = SplitSpanByWindow(s, holiday)
	if sum(in) != 60 {
		t.Errorf("holiday: in=%d", sum(in))
	}
	s, _ = SpanOnDate("2026-09-08", "12:00", "13:00", loc)
	in, _ = SplitSpanByWindow(s, holiday)
	if sum(in) != 0 {
		t.Errorf("non-holiday: in=%d", sum(in))
	}

	exact := Window{ExactDates: []string{"2026-12-24"}}
	s, _ = SpanOnDate("2026-12-24", "12:00", "13:00", loc)
	if in, _ = SplitSpanByWindow(s, exact); sum(in) != 60 {
		t.Errorf("exact date: in=%d", sum(in))
	}

	empty := Window{}
	s, _ = SpanOnDate("2026-09-04", "18:00", "23:30", loc)
	if in, out = SplitSpanByWindow(s, empty); sum(in) != 330 || sum(out) != 0 {
		t.Errorf("empty window should match everything: in=%d out=%d", sum(in), sum(out))
	}
}

func sum(ss []Span) int {
	n := 0
	for _, s := range ss {
		n += s.Minutes()
	}
	return n
}

func TestDayWindowCrossingMidnightKeepsItsDay(t *testing.T) {
	loc := NY()
	fri := Window{Days: []Day{"fri"}, From: "22:00", To: "02:00"}
	// Fri 2026-09-25 21:00 → Sat 02:00: 22:00–02:00 is inside, the after-midnight part belongs to Friday
	s, _ := SpanOnDate("2026-09-25", "21:00", "02:00", loc)
	in, out := SplitSpanByWindow(s, fri)
	if sum(in) != 240 || sum(out) != 60 {
		t.Errorf("friday late: in=%d out=%d", sum(in), sum(out))
	}
	sat1am := time.Date(2026, 9, 26, 1, 0, 0, 0, loc)
	if !fri.Matches(sat1am) {
		t.Errorf("Sat 01:00 should be in Friday's 22:00–02:00")
	}
	// Saturday night's after-midnight hours (Sun 01:00) are not Friday's
	if fri.Matches(time.Date(2026, 9, 27, 1, 0, 0, 0, loc)) {
		t.Errorf("Sun 01:00 is not in a Friday window")
	}
	// Thursday night into Friday: Fri 01:00 belongs to Thursday, so it is out
	s, _ = SpanOnDate("2026-09-24", "23:00", "02:00", loc)
	if in, _ = SplitSpanByWindow(s, fri); sum(in) != 0 {
		t.Errorf("thursday night: in=%d", sum(in))
	}

	// priced: Fri 21:00–02:00 with $30/hr Fridays 22:00–02:00 over $20 base = $20 + 4 × $30 = $140
	terms := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "b", Kind: KindHourly, Service: "hourly", RateCents: 2000},
		{ID: "f", Kind: KindHourly, Service: "hourly", Label: "Friday late", RateCents: 3000, When: &fri}}}
	r := PriceLine(line("2026-09-25", "hourly", "21:00", "02:00", 14000), terms, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 14000 {
		t.Errorf("friday late priced: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
}
