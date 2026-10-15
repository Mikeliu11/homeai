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
	seen := map[string]int{} // duplicate key → index of the first line with it
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

		// bill-level flags first: a repeated, out-of-period or already-billed line
		// stays out of the agreed total until Jordan decides on it
		var hold []string
		heldProblems := 0
		flag := func(f Flag, holds bool) {
			res.Flags = append(res.Flags, f)
			if holds {
				hold = append(hold, f.Message)
				if f.Level == "problem" {
					heldProblems++
				}
			}
		}
		if l.Date != "" {
			if l.Start != "" || l.Qty > 0 || (lr.Status != StatusUnagreed && lr.Status != StatusUnpriceable) {
				serviceDates[l.Date] = true
			}
			if l.Start != "" {
				hourlyHelper = true
			}
			key := strings.Join([]string{l.Date, l.Service, l.Start, l.End, trimFloat(l.Qty), strings.TrimSpace(l.Note), fmt.Sprint(l.ClaimedCents)}, "|")
			if first, ok := seen[key]; ok {
				if l.Start == "" && l.Qty == 0 {
					// a date and an amount only: two walks on one day look the same, so ask rather than call it a repeat
					flag(Flag{Code: "duplicate_line", Level: "info", Message: fmt.Sprintf("Line %d has the same date, service and amount as line %d and no times; check it is a second visit, not a repeat.", i+1, first+1)}, true)
				} else {
					flag(Flag{Code: "duplicate_line", Level: "problem", Message: fmt.Sprintf("Line %d repeats line %d (%s %s).", i+1, first+1, l.Date, firstNonEmpty(l.Note, l.Service))}, true)
				}
			} else {
				seen[key] = i
			}
			if cal.Start != "" && (l.Date < cal.Start || l.Date > cal.End) {
				flag(Flag{Code: "outside_period", Level: "problem", Message: fmt.Sprintf("Line %d is dated %s, outside %s.", i+1, l.Date, b.Period)}, true)
			}
			if name, ok := cal.Holidays[l.Date]; ok {
				flag(Flag{Code: "holiday", Level: "info", Message: fmt.Sprintf("Line %d falls on %s (%s).", i+1, name, l.Date)}, false)
			}
			if len(cal.UsualDayNames) > 0 && l.Start == "" && visitService(versions, l.Service) { // visits only: sitters work odd evenings by design, and a fee is not a visit
				if d, err := ParseDate(l.Date, loc); err == nil && !containsDay(cal.UsualDayNames, dayOf[d.Weekday()]) {
					flag(Flag{Code: "non_usual_day", Level: "info", Message: fmt.Sprintf("Line %d is on a %s, not one of the usual days.", i+1, d.Weekday())}, false)
				}
			}
			if otherBillDates[l.Date] {
				flag(Flag{Code: "duplicate_date_other_bill", Level: "problem", Message: fmt.Sprintf("Line %d: %s already appears on another bill for this helper.", i+1, l.Date)}, true)
			}
		}

		switch {
		case len(hold) > 0 && (lr.Status == StatusMatch || lr.Status == StatusOver || lr.Status == StatusUnder):
			// priced, but flagged: needs your call, kept out of the agreed total
			d := Decision{LineIndex: i, Status: lr.Status, Claim: firstNonEmpty(l.Note, l.Service), Why: strings.Join(hold, " ") + " " + lr.Explanation}
			if lr.Status == StatusOver && len(lr.Readings) > 0 {
				d.Status, d.Readings = StatusAmbiguous, lr.Readings[:1] // offer the agreed amount as a choice
			}
			res.PendingDecisions = append(res.PendingDecisions, d)
			res.Problems -= heldProblems // the decision is the problem; do not count its flag twice
		case lr.Status == StatusMatch || lr.Status == StatusOver:
			res.AgreedTotalCents += lr.ExpectedCents
			if lr.Status == StatusOver {
				res.Problems++
			}
		case lr.Status == StatusUnder:
			res.AgreedTotalCents += l.ClaimedCents // never pay more than asked without a decision
		default:
			why := lr.Explanation
			if len(hold) > 0 {
				why = strings.Join(hold, " ") + " " + why
				res.Problems -= heldProblems
			}
			res.PendingDecisions = append(res.PendingDecisions, Decision{LineIndex: i, Status: lr.Status, Readings: lr.Readings,
				Claim: firstNonEmpty(l.Note, l.Service), Why: why})
		}
		if l.Date == "" && l.Qty > 0 {
			if hourlyService(versions, l.Service) {
				hourlyHelper = true // hours, not visits: they say nothing about the usual days
			} else {
				unitsBilled += l.Qty
			}
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

	// problems = over lines + pending decisions + problem-level flags (over lines
	// were counted in the loop; a flag that became a decision was taken back out)
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
	// snapshots that start inside the period; of two with the same start, the
	// later confirmation (a correction) replaces the earlier, as in TermsInForce
	latest := map[string]Terms{}
	for _, v := range versions {
		if v.EffectiveFrom <= cal.Start || v.EffectiveFrom > cal.End || (v.EffectiveTo != "" && v.EffectiveTo < v.EffectiveFrom) {
			continue
		}
		if cur, ok := latest[v.EffectiveFrom]; !ok || v.ConfirmedAt > cur.ConfirmedAt {
			latest[v.EffectiveFrom] = v
		}
	}
	var inPeriod []Terms
	for _, v := range latest {
		inPeriod = append(inPeriod, v)
	}
	sort.Slice(inPeriod, func(i, j int) bool { return inPeriod[i].EffectiveFrom < inPeriod[j].EffectiveFrom })
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
	// every snapshot prices the line the same way: no question to ask
	same := true
	for _, r := range results[1:] {
		same = same && r.Status == results[0].Status && centsOf(r.Readings) == centsOf(results[0].Readings)
	}
	if same {
		return results[0]
	}
	// several snapshots apply inside the period and the line carries no date
	out := LineResult{Line: l, Status: StatusAmbiguous, TermsLabel: "several"}
	var parts []string
	seenCents := map[Cents]bool{}
	for _, r := range results {
		if len(r.Readings) == 0 {
			continue
		}
		rd := r.Readings[0]
		if seenCents[rd.ExpectedCents] {
			continue
		}
		seenCents[rd.ExpectedCents] = true
		rd.Explanation = fmt.Sprintf("%s under %s", rd.Explanation, r.TermsLabel)
		out.Readings = append(out.Readings, rd)
		parts = append(parts, fmt.Sprintf("%s (%s)", rd.ExpectedCents, r.TermsLabel))
	}
	if len(out.Readings) == 0 {
		return results[0]
	}
	priced := 0
	for _, r := range results {
		if len(r.Readings) > 0 {
			priced++
		}
	}
	if len(out.Readings) == 1 && priced == len(results) {
		// the snapshots differ, but not on this line's price: finish on the one reading
		return finish(LineResult{Line: l, TermsLabel: results[0].TermsLabel, Info: results[0].Info}, out.Readings[:1], nil, "", "")
	}
	out.ExpectedCents = out.Readings[0].ExpectedCents
	out.Explanation = fmt.Sprintf("The rate changed during %s and this line has no dates, so it could be %s. Billed %s.", cal.Period, strings.Join(parts, " or "), l.ClaimedCents)
	return out
}

// visitService says whether a line counts as a visit: no service (the default
// per-visit rate) or a per-unit service in some snapshot. Fees and unagreed
// extras are not visits.
func visitService(versions []Terms, svc string) bool {
	if svc == "" {
		return true
	}
	for _, t := range versions {
		for _, r := range t.Rules {
			if r.Service == svc && r.Kind == KindPerUnit {
				return true
			}
		}
	}
	return false
}

// hourlyService says whether a service is priced by the hour in any snapshot.
func hourlyService(versions []Terms, svc string) bool {
	for _, v := range versions {
		for _, r := range v.Rules {
			if r.Kind == KindHourly && r.Service == svc {
				return true
			}
		}
	}
	return false
}

// centsOf is a reading set's amounts, for comparing two pricings of one line.
func centsOf(rs []Reading) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "%d,", r.ExpectedCents)
	}
	return b.String()
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
		if normDay(x) == normDay(d) {
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
