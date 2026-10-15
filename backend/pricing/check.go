package pricing

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Bill is one helper's bill for one period, as extracted.
type Bill struct {
	HelperID          string `json:"helperId"`
	Period            string `json:"period"` // YYYY-MM
	Lines             []Line `json:"lines"`
	ClaimedTotalCents Cents  `json:"claimedTotalCents"`
}

// Decision is a line Jordan has to rule on before paying.
type Decision struct {
	LineIndex int       `json:"lineIndex"`
	Status    Status    `json:"status"`
	Readings  []Reading `json:"readings,omitempty"`
	Claim     string    `json:"claim"` // what the helper wrote
	Why       string    `json:"why"`
}

// Flag is a bill-level observation.
type Flag struct {
	Code    string `json:"code"` // duplicate_line outside_period holiday non_usual_day count_vs_usual duplicate_date_other_bill arithmetic
	Message string `json:"message"`
	Level   string `json:"level"` // "problem" or "info"
}

// CheckResult is the engine's verdict on a bill. Three numbers, always:
// what was claimed, what the lines add to, and what the agreement supports.
type CheckResult struct {
	Lines             []LineResult `json:"lines"`
	ClaimedTotalCents Cents        `json:"claimedTotalCents"`
	LineSumCents      Cents        `json:"lineSumCents"`
	AgreedTotalCents  Cents        `json:"agreedTotalCents"`
	ArithmeticOK      bool         `json:"arithmeticOk"`
	PendingDecisions  []Decision   `json:"pendingDecisions"`
	Flags             []Flag       `json:"flags"`
	Headline          string       `json:"headline"`
	Problems          int          `json:"problems"`
	EngineVersion     string       `json:"engineVersion"`
	TermsUsed         []string     `json:"termsUsed"` // labels of the snapshots that priced lines
}

// TermsInForce picks the confirmed snapshot for a date: the greatest
// EffectiveFrom ≤ date. An undated snapshot ("") is lowest precedence. Two
// snapshots with the same date: the later confirmation wins (a correction).
// Order of the input does not matter.
func TermsInForce(versions []Terms, date string) (Terms, bool) {
	var best Terms
	found := false
	for _, v := range versions {
		if v.EffectiveFrom > date {
			continue
		}
		if v.EffectiveTo != "" && v.EffectiveTo < date {
			continue
		}
		if !found || v.EffectiveFrom > best.EffectiveFrom || (v.EffectiveFrom == best.EffectiveFrom && v.ConfirmedAt > best.ConfirmedAt) {
			best, found = v, true
		}
	}
	return best, found
}

// CheckBill prices every line against the terms in force on its date and
// reports totals, decisions Jordan must make, and bill-level flags.
func CheckBill(b Bill, versions []Terms, cal CalendarFacts, otherBillDates map[string]bool, loc *time.Location) CheckResult {
	res := CheckResult{ClaimedTotalCents: b.ClaimedTotalCents, EngineVersion: EngineVersion,
		Lines: []LineResult{}, PendingDecisions: []Decision{}, Flags: []Flag{}, TermsUsed: []string{}}
	used := map[string]bool{}
	seen := map[string]int{}
	var unitsBilled float64
	serviceDates := map[string]bool{} // distinct dates with a priced service line (fees don't count as days)
	hourlyHelper := false

	for i, l := range b.Lines {
		res.LineSumCents += l.ClaimedCents
		var lr LineResult
		if l.Date == "" {
			lr = priceUndated(l, versions, cal, loc)
			if lr.TermsLabel != "" && lr.TermsLabel != "several" {
				used[lr.TermsLabel] = true
			}
		} else {
			t, ok := TermsInForce(versions, l.Date)
			if !ok {
				lr = LineResult{Line: l, Status: StatusUnpriceable, Explanation: fmt.Sprintf("No confirmed terms were in force on %s.", l.Date)}
			} else {
				lr = PriceLine(l, t, loc)
				used[t.Label] = true
			}
		}
		res.Lines = append(res.Lines, lr)

		switch lr.Status {
		case StatusMatch, StatusOver:
			res.AgreedTotalCents += lr.ExpectedCents
		case StatusUnder:
			res.AgreedTotalCents += l.ClaimedCents // never pay more than asked without a decision
		default:
			res.PendingDecisions = append(res.PendingDecisions, Decision{LineIndex: i, Status: lr.Status, Readings: lr.Readings,
				Claim: firstNonEmpty(l.Note, l.Service), Why: lr.Explanation})
		}
		if l.Date != "" {
			if l.Start != "" || l.Qty > 0 || (lr.Status != StatusUnagreed && lr.Status != StatusUnpriceable) {
				serviceDates[l.Date] = true
			}
			if l.Start != "" {
				hourlyHelper = true
			}
			key := l.Date + "|" + l.Service + "|" + l.Start + "|" + l.End + "|" + fmt.Sprint(l.ClaimedCents)
			seen[key]++
			if seen[key] == 2 {
				res.Flags = append(res.Flags, Flag{Code: "duplicate_line", Level: "problem", Message: fmt.Sprintf("Line %d repeats an earlier line (%s %s).", i+1, l.Date, firstNonEmpty(l.Note, l.Service))})
			}
			if cal.Start != "" && (l.Date < cal.Start || l.Date > cal.End) {
				res.Flags = append(res.Flags, Flag{Code: "outside_period", Level: "problem", Message: fmt.Sprintf("Line %d is dated %s, outside %s.", i+1, l.Date, b.Period)})
			}
			if name, ok := cal.Holidays[l.Date]; ok {
				res.Flags = append(res.Flags, Flag{Code: "holiday", Level: "info", Message: fmt.Sprintf("Line %d falls on %s (%s).", i+1, name, l.Date)})
			}
			if len(cal.UsualDayNames) > 0 && l.Start == "" { // per-visit helpers only; sitters work odd evenings by design
				if d, err := ParseDate(l.Date, loc); err == nil && !containsDay(cal.UsualDayNames, dayOf[d.Weekday()]) {
					res.Flags = append(res.Flags, Flag{Code: "non_usual_day", Level: "info", Message: fmt.Sprintf("Line %d is on a %s, not one of the usual days.", i+1, d.Weekday())})
				}
			}
			if otherBillDates[l.Date] {
				res.Flags = append(res.Flags, Flag{Code: "duplicate_date_other_bill", Level: "problem", Message: fmt.Sprintf("%s already appears on another bill for this helper.", l.Date)})
			}
		} else if l.Qty > 0 {
			unitsBilled += l.Qty
		}
	}

	res.ArithmeticOK = res.LineSumCents == res.ClaimedTotalCents
	if !res.ArithmeticOK {
		res.Flags = append(res.Flags, Flag{Code: "arithmetic", Level: "problem", Message: fmt.Sprintf("The lines add up to %s, but the bill says %s.", res.LineSumCents, res.ClaimedTotalCents)})
	}
	if cal.UsualDays > 0 {
		n := float64(len(serviceDates))
		if unitsBilled > 0 {
			n = unitsBilled
		}
		if n > 0 && int(n) != cal.UsualDays {
			level := "info"
			if int(n) > cal.UsualDays && !hourlyHelper {
				level = "problem" // a per-visit helper billing more visits than there were usual days
			}
			res.Flags = append(res.Flags, Flag{Code: "count_vs_usual", Level: level, Message: fmt.Sprintf("%s billed; the usual schedule has %d days in %s%s.", trimFloat(n), cal.UsualDays, monthName(b.Period), holidayNote(cal))})
		}
	}

	for k := range used {
		res.TermsUsed = append(res.TermsUsed, k)
	}
	sort.Strings(res.TermsUsed)

	// problems = lines not matching + pending decisions + problem-level flags
	for _, lr := range res.Lines {
		if lr.Status == StatusOver {
			res.Problems++
		}
	}
	res.Problems += len(res.PendingDecisions)
	for _, f := range res.Flags {
		if f.Level == "problem" {
			res.Problems++
		}
	}
	res.Headline = headline(res)
	return res
}

func headline(r CheckResult) string {
	if r.Problems == 0 {
		return fmt.Sprintf("Pay %s — matches what you agreed", r.AgreedTotalCents)
	}
	if n := len(r.PendingDecisions); n > 0 && r.AgreedTotalCents == 0 {
		if n == 1 {
			return fmt.Sprintf("1 line needs your call before paying (billed %s)", r.ClaimedTotalCents)
		}
		return fmt.Sprintf("%d lines need your call before paying (billed %s)", n, r.ClaimedTotalCents)
	}
	noun := "problems"
	if r.Problems == 1 {
		noun = "problem"
	}
	if r.AgreedTotalCents == r.ClaimedTotalCents {
		return fmt.Sprintf("Pay %s — %d %s to look at", r.AgreedTotalCents, r.Problems, noun)
	}
	return fmt.Sprintf("Pay %s, not %s — %d %s", r.AgreedTotalCents, r.ClaimedTotalCents, r.Problems, noun)
}

// priceUndated prices an aggregate line (no date) against the period. If the
// terms change inside the period, both readings are returned as ambiguous.
func priceUndated(l Line, versions []Terms, cal CalendarFacts, loc *time.Location) LineResult {
	if cal.Start == "" {
		return LineResult{Line: l, Status: StatusUnpriceable, Explanation: "Undated line and no billing period."}
	}
	var inPeriod []Terms
	for _, v := range versions {
		if v.EffectiveFrom > cal.Start && v.EffectiveFrom <= cal.End {
			inPeriod = append(inPeriod, v)
		}
	}
	first, ok := TermsInForce(versions, cal.Start)
	if !ok && len(inPeriod) == 0 {
		return LineResult{Line: l, Status: StatusUnpriceable, Explanation: fmt.Sprintf("No confirmed terms were in force in %s.", cal.Period)}
	}
	var results []LineResult
	if ok {
		results = append(results, PriceLine(l, first, loc))
	}
	for _, v := range inPeriod {
		results = append(results, PriceLine(l, v, loc))
	}
	if len(results) == 1 {
		return results[0]
	}
	// several snapshots apply inside the period and the line carries no date
	out := LineResult{Line: l, Status: StatusAmbiguous, TermsLabel: "several"}
	var parts []string
	for _, r := range results {
		if len(r.Readings) == 0 {
			continue
		}
		rd := r.Readings[0]
		rd.Explanation = fmt.Sprintf("%s under %s", rd.Explanation, r.TermsLabel)
		out.Readings = append(out.Readings, rd)
		parts = append(parts, fmt.Sprintf("%s (%s)", rd.ExpectedCents, r.TermsLabel))
	}
	if len(out.Readings) == 0 {
		return results[0]
	}
	out.ExpectedCents = out.Readings[0].ExpectedCents
	out.Explanation = fmt.Sprintf("The rate changed during %s and this line has no dates, so it could be %s. Billed %s.", cal.Period, strings.Join(parts, " or "), l.ClaimedCents)
	return out
}

func holidayNote(cal CalendarFacts) string {
	var names []string
	for d, n := range cal.Holidays {
		if t, err := time.Parse("2006-01-02", d); err == nil && containsDay(cal.UsualDayNames, dayOf[t.Weekday()]) {
			names = append(names, fmt.Sprintf("%s (%s)", n, t.Format("Mon, Jan 2")))
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return " (excluding " + strings.Join(names, ", ") + ")"
}

func containsDay(ds []Day, d Day) bool {
	for _, x := range ds {
		if x == d {
			return true
		}
	}
	return false
}

// monthName turns "2026-09" into "September 2026" for messages.
func monthName(period string) string {
	if t, err := time.Parse("2006-01", period); err == nil {
		return t.Format("January 2006")
	}
	return period
}
