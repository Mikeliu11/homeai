package pricing

import (
	"fmt"
	"strings"
	"time"
)

// Day is a lowercase three-letter weekday.
type Day string

var dayOf = map[time.Weekday]Day{
	time.Monday: "mon", time.Tuesday: "tue", time.Wednesday: "wed", time.Thursday: "thu",
	time.Friday: "fri", time.Saturday: "sat", time.Sunday: "sun",
}

// Window is when a rule applies. Empty fields mean "any". Clock windows are
// evaluated per calendar day; From > To means the window crosses midnight.
type Window struct {
	Days       []Day    `json:"days,omitempty"`
	From       Clock    `json:"from,omitempty"`
	To         Clock    `json:"to,omitempty"`
	Dates      string   `json:"dates,omitempty"`      // "us_federal_holidays"
	ExactDates []string `json:"exactDates,omitempty"` // YYYY-MM-DD
}

// IsEmpty reports whether the window matches everything.
func (w Window) IsEmpty() bool {
	return len(w.Days) == 0 && w.From == "" && w.To == "" && w.Dates == "" && len(w.ExactDates) == 0
}

func (w Window) dateMatches(t time.Time) bool {
	if len(w.Days) > 0 {
		ok := false
		for _, d := range w.Days {
			if Day(strings.ToLower(string(d))) == dayOf[t.Weekday()] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	ds := t.Format("2006-01-02")
	if w.Dates == "us_federal_holidays" {
		if _, ok := USFederalHolidays(t.Year())[ds]; !ok {
			return false
		}
	}
	if len(w.ExactDates) > 0 {
		ok := false
		for _, d := range w.ExactDates {
			if d == ds {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Validate reports malformed clocks, days or dates. A window that fails
// validation must never be priced: a swallowed "10pm" would mean "all day".
func (w Window) Validate() error {
	if w.From != "" {
		if _, _, err := parseClock(w.From); err != nil {
			return err
		}
	}
	if w.To != "" {
		if _, _, err := parseClock(w.To); err != nil {
			return err
		}
	}
	for _, d := range w.Days {
		ok := false
		for _, v := range dayOf {
			if v == Day(strings.ToLower(string(d))) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("bad day %q (want mon..sun)", d)
		}
	}
	if w.Dates != "" && w.Dates != "us_federal_holidays" {
		return fmt.Errorf("bad dates %q (want us_federal_holidays)", w.Dates)
	}
	for _, d := range w.ExactDates {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return fmt.Errorf("bad date %q (want YYYY-MM-DD)", d)
		}
	}
	return nil
}

// IsDateOnly reports a window with no clock part (Sundays, holidays).
func (w Window) IsDateOnly() bool { return w.From == "" && w.To == "" && !w.IsEmpty() }

// clockRanges returns the window's [from, to) minutes-of-day; crossing midnight
// yields two ranges. Clocks are assumed validated.
func (w Window) clockRanges() [][2]int {
	from, to := 0, 24*60
	if w.From != "" {
		if h, m, err := parseClock(w.From); err == nil {
			from = h*60 + m
		}
	}
	if w.To != "" {
		if h, m, err := parseClock(w.To); err == nil {
			to = h*60 + m
		}
	}
	if to <= from && w.To != "" {
		return [][2]int{{from, 24 * 60}, {0, to}}
	}
	return [][2]int{{from, to}}
}

// Matches reports whether an instant falls inside the window.
func (w Window) Matches(t time.Time) bool {
	if !w.dateMatches(t) {
		return false
	}
	mod := t.Hour()*60 + t.Minute()
	for _, r := range w.clockRanges() {
		if mod >= r[0] && mod < r[1] {
			return true
		}
	}
	return false
}

// SplitSpanByWindow partitions a span into the minutes inside and outside a window.
func SplitSpanByWindow(s Span, w Window) (in, out []Span) {
	for _, day := range s.SplitAtMidnight() {
		if w.IsEmpty() {
			in = append(in, day)
			continue
		}
		if !w.dateMatches(day.Start) {
			out = append(out, day)
			continue
		}
		midnight := time.Date(day.Start.Year(), day.Start.Month(), day.Start.Day(), 0, 0, 0, 0, day.Start.Location())
		// collect in-window pieces, then derive the gaps
		var pieces []Span
		for _, r := range w.clockRanges() {
			ws := midnight.Add(time.Duration(r[0]) * time.Minute)
			we := midnight.Add(time.Duration(r[1]) * time.Minute)
			st, en := maxT(ws, day.Start), minT(we, day.End)
			if en.After(st) {
				pieces = append(pieces, Span{st, en})
			}
		}
		sortSpans(pieces)
		cur := day.Start
		for _, p := range pieces {
			if p.Start.After(cur) {
				out = append(out, Span{cur, p.Start})
			}
			in = append(in, p)
			cur = p.End
		}
		if day.End.After(cur) {
			out = append(out, Span{cur, day.End})
		}
	}
	return in, out
}

func maxT(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func minT(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func sortSpans(ss []Span) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j].Start.Before(ss[j-1].Start); j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}
