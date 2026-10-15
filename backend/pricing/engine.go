package pricing

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// EngineVersion is recorded on every check so an approval can say which
// arithmetic it was based on.
const EngineVersion = "pricing/2"

// PriceLine prices one line under one terms snapshot. It is pure: same inputs,
// same verdict. It never guesses: anything it cannot express is unpriceable,
// and anything the terms leave open is ambiguous with every reading shown.
func PriceLine(l Line, t Terms, loc *time.Location) LineResult {
	res := LineResult{Line: l, TermsLabel: t.Label}
	if errs := ValidateTerms(t); len(errs) > 0 {
		return finish(res, nil, nil, "These terms have a problem and cannot be used for pricing: "+strings.Join(errs, "; "), StatusUnpriceable)
	}
	if l.ClaimedCents < 0 {
		return finish(res, nil, nil, fmt.Sprintf("Credit line (%s, %s): no agreed rate prices a credit; confirm with the helper.", l.ClaimedCents, firstNonEmpty(l.Note, l.Service, "no description")), StatusUnpriceable)
	}
	ix := index(t)

	svc := strings.TrimSpace(l.Service)
	hasSpan := l.Start != "" || l.End != ""
	if svc == "" || svc == "unknown" {
		switch {
		case hasSpan && len(ix.byService["hourly"]) > 0:
			svc = "hourly"
		case t.DefaultPerVisit != "":
			svc = t.DefaultPerVisit
		default:
			return finish(res, nil, nil, fmt.Sprintf("No agreed rate covers %q.", firstNonEmpty(l.Note, "this line")), StatusUnagreed)
		}
	}
	primary := ix.byService[svc]
	if len(primary) == 0 {
		return finish(res, nil, nil, fmt.Sprintf("No agreed rate for %q.", firstNonEmpty(l.Note, svc)), StatusUnagreed)
	}

	var span Span
	var spanErr error
	if hasSpan {
		if l.Date == "" {
			return finish(res, nil, nil, "A line with times needs a date.", StatusUnpriceable)
		}
		span, spanErr = SpanOnDate(l.Date, l.Start, l.End, loc)
		if spanErr != nil {
			return finish(res, nil, nil, "Could not read the times: "+spanErr.Error(), StatusUnpriceable)
		}
	}
	var lineDate time.Time
	if l.Date != "" {
		lineDate, _ = ParseDate(l.Date, loc)
	}

	var readings []Reading
	var err string
	var info []string
	switch primary[0].Kind {
	case KindHourly:
		switch {
		case hasSpan:
			readings, err = ix.priceHourly(svc, span, l, true)
		case l.Qty > 0:
			// "4 hours, $88" or an undated "12 hours @ $22": no clock times.
			if l.Date != "" && l.Qty > 24 {
				return finish(res, nil, nil, "More than 24 hours on one date; the line needs start and end times or one line per day.", StatusUnpriceable)
			}
			outside, inside, unjudged, e := ix.priceHours(svc, l, lineDate, loc)
			if e != "" {
				return finish(res, nil, nil, e, StatusUnpriceable)
			}
			if len(unjudged) == 0 {
				readings = outside
				break
			}
			what := "No start and end times were given"
			if l.Date == "" {
				what = "No date or times were given"
			}
			what += ", so these terms could not be checked: " + strings.Join(unjudged, "; ")
			if l.Date != "" && len(outside) == 1 && l.ClaimedCents <= outside[0].ExpectedCents {
				// billed no more than the hours cost without those rules: pay as billed, and say what was not checked
				readings = outside
				info = append(info, what+". Priced without them.")
				break
			}
			all := dedupeReadings(append(append([]Reading{}, outside...), inside...))
			var without, with []string
			for _, rd := range outside {
				without = append(without, fmt.Sprintf("%s (%s)", rd.ExpectedCents, rd.Explanation))
			}
			for _, rd := range inside {
				with = append(with, fmt.Sprintf("%s (%s)", rd.ExpectedCents, rd.Explanation))
			}
			return needsCall(res, all, fmt.Sprintf("%s. Without them %s; if they covered every hour %s. Billed %s.", what,
				joinOr(without), joinOr(with), l.ClaimedCents))
		default:
			return finish(res, nil, nil, "Hourly line without start/end times or a number of hours.", StatusUnpriceable)
		}
	case KindFlat:
		if !hasSpan {
			readings = []Reading{{Service: svc, ExpectedCents: primary[0].AmountCents, Basis: []string{primary[0].ID},
				Explanation: fmt.Sprintf("%s: flat %s", niceName(primary[0]), primary[0].AmountCents)}}
			readings = ix.applyDateMultipliers(readings, lineDate)
		} else {
			// priceFlat applies the date multipliers itself: to the flat amount, while the overflow is priced hourly (which already applies them)
			readings, err = ix.priceFlat(primary[0], span, l)
		}
	case KindPerUnit, KindPerPeriod:
		r := primary[0]
		qty := l.Qty
		if qty == 0 {
			if countable(r.Unit) {
				qty = 1
			} else {
				return finish(res, nil, nil, fmt.Sprintf("%s is priced per %s but the line gives no number of %ss.", niceName(r), r.Unit, r.Unit), StatusUnpriceable)
			}
		}
		exp := Cents(math.Round(float64(r.AmountCents) * qty))
		unit := firstNonEmpty(r.Unit, r.Period, "each")
		readings = []Reading{{Service: svc, ExpectedCents: exp, Basis: []string{r.ID},
			Explanation: fmt.Sprintf("%s × %s per %s (%s) = %s", trimFloat(qty), r.AmountCents, unit, niceName(r), exp)}}
		if !lineDate.IsZero() {
			readings = ix.applyDateMultipliers(readings, lineDate)
		}
	case KindFee:
		r := primary[0]
		ok, why, unsure := feeTriggered(r, l)
		if !ok {
			return finish(res, nil, nil, why, StatusUnagreed)
		}
		exp := r.AmountCents
		expl := fmt.Sprintf("%s: %s", niceName(r), r.AmountCents)
		if r.PercentOfBooking > 0 {
			booking, how := l.BookingCents, ""
			if booking == 0 {
				// price the cancelled booking from its times or hours, so the model never has to
				booking, how = ix.bookingFromLine(l, span, hasSpan, lineDate, loc)
			}
			if booking == 0 {
				return finish(res, nil, nil, fmt.Sprintf("%s is %v%% of the booking, but the booking amount is unknown: the line needs the booked times or hours.", niceName(r), r.PercentOfBooking), StatusUnpriceable)
			}
			exp = Cents(math.Round(float64(booking) * r.PercentOfBooking / 100))
			expl = fmt.Sprintf("%s: %v%% of %s%s = %s", niceName(r), r.PercentOfBooking, booking, how, exp)
		}
		readings = []Reading{{Service: svc, ExpectedCents: exp, Basis: []string{r.ID}, Explanation: expl}}
		if unsure != "" {
			return needsCall(res, readings, fmt.Sprintf("%s. %s Billed %s.", expl, unsure, l.ClaimedCents))
		}
	case KindReimbursement:
		r := primary[0]
		exp := l.ClaimedCents
		expl := fmt.Sprintf("%s reimbursed at cost (%s)", niceName(r), l.ClaimedCents)
		if r.CapCents > 0 && exp > r.CapCents {
			exp = r.CapCents
			expl = fmt.Sprintf("%s reimbursed up to %s (billed %s)", niceName(r), r.CapCents, l.ClaimedCents)
		}
		if r.ReceiptRequired && !l.Flags["receipt"] {
			info = append(info, "A receipt was agreed for this expense; none is attached.")
		}
		readings = []Reading{{Service: svc, ExpectedCents: exp, Basis: []string{r.ID}, Explanation: expl}}
	default:
		return finish(res, nil, nil, fmt.Sprintf("Rule kind %q cannot be priced on a bill line.", primary[0].Kind), StatusUnpriceable)
	}
	if err != "" {
		return finish(res, nil, nil, err, StatusUnpriceable)
	}
	// an adder counts units from the line's extras; extras listed under another name are not silently dropped
	if len(l.Extras) > 0 && (primary[0].Kind == KindHourly || primary[0].Kind == KindFlat) {
		for _, a := range ix.adders {
			if _, ok := l.Extras[a.Per]; a.Per != "" && !ok {
				info = append(info, fmt.Sprintf("%s counts %q, which this line does not give (it lists %s); it was not added.", firstNonEmpty(a.Label, "The adder"), a.Per, extrasText(l.Extras)))
			}
		}
	}
	res.Info = append(res.Info, info...)

	// Alternative readings: other ways the same span could be priced under the terms.
	var alts []Reading
	if hasSpan {
		for _, r := range t.Rules {
			if r.Kind == KindFlat && r.Service != svc && flatFits(r, span) {
				if rs, e := ix.priceFlat(r, span, l); e == "" && len(rs) == 1 {
					alts = append(alts, rs[0])
				}
			}
		}
		if primary[0].Kind == KindFlat && len(ix.byService["hourly"]) > 0 {
			if rs, e := ix.priceHourly("hourly", span, l, true); e == "" && len(rs) == 1 {
				alts = append(alts, rs[0])
			}
		}
	}
	return finish(res, readings, alts, "", "")
}

// needsCall returns a line Jordan has to rule on: the engine can price it,
// but something the line leaves open decides whether that price holds.
func needsCall(res LineResult, readings []Reading, why string) LineResult {
	res.Status = StatusAmbiguous
	res.Readings = readings
	res.ExpectedCents = readings[0].ExpectedCents
	res.Basis = readings[0].Basis
	res.Explanation = why
	return res
}

// finish derives the status from the readings and the claimed amount.
func finish(res LineResult, readings, alts []Reading, msg string, forced Status) LineResult {
	if forced != "" {
		res.Status = forced
		res.Explanation = msg
		return res
	}
	res.Readings = readings
	res.ExpectedCents = readings[0].ExpectedCents
	res.Basis = readings[0].Basis
	claimed := res.Line.ClaimedCents
	if len(readings) > 1 {
		res.Status = StatusAmbiguous
		parts := make([]string, 0, len(readings))
		for _, r := range readings {
			parts = append(parts, fmt.Sprintf("%s (%s)", r.ExpectedCents, r.Explanation))
		}
		res.Explanation = "Two readings of the agreed rates fit this line: " + strings.Join(parts, " or ") + fmt.Sprintf(". Billed %s.", claimed)
		res.Readings = append(res.Readings, alts...)
		return res
	}
	switch {
	case claimed == res.ExpectedCents:
		res.Status = StatusMatch
		res.Explanation = fmt.Sprintf("%s. Billed %s, as agreed.", readings[0].Explanation, claimed)
	case claimed > res.ExpectedCents:
		res.Status = StatusOver
		res.Explanation = fmt.Sprintf("%s. Billed %s, %s over.", readings[0].Explanation, claimed, claimed-res.ExpectedCents)
	default:
		res.Status = StatusUnder
		res.Explanation = fmt.Sprintf("%s. Billed %s, %s under; paying as billed.", readings[0].Explanation, claimed, res.ExpectedCents-claimed)
	}
	for _, a := range alts {
		if a.ExpectedCents < res.ExpectedCents && claimed > a.ExpectedCents {
			res.Status = StatusAmbiguous
			res.Explanation = fmt.Sprintf("%s. But read as %s it would be %s (%s). Billed %s.", readings[0].Explanation, withArticle(humanize(a.Service)), a.ExpectedCents, a.Explanation, claimed)
		} else {
			res.Info = append(res.Info, fmt.Sprintf("Read as %s this would be %s (%s), which is more.", withArticle(humanize(a.Service)), a.ExpectedCents, a.Explanation))
		}
		res.Readings = append(res.Readings, a)
	}
	return res
}

// niceName is how a rule is referred to in explanations: the helper's own
// words when they add something, otherwise the service key in plain words.
func niceName(r Rule) string {
	if l := stripMoney(r.Label); l != "" && len(l) <= 48 {
		return l
	}
	return humanize(firstNonEmpty(r.Service, string(r.Kind)))
}

var moneyRe = regexp.MustCompile(`\$\s?[\d,]+(\.\d+)?(\s?(/|per\s)\s?(hr|hour|h|visit|walk|mile|day|session))?`)
var fillerRe = regexp.MustCompile(`^(for|a|an|each|per|is|it's|its|at|to|the|my|,|:|-)\s+`)
var trailingRe = regexp.MustCompile(`\s+(for|a|an|each|per|is|it's|its|at|to|the|my|are|be|will|now|,|:|-)$`)
var genericRe = regexp.MustCompile(`^(visit|visits|walk|walks|hour|hours|session|sessions|each|time|rate)$`)

// stripMoney turns "$28 for a 30-min walk" into "30-min walk" and "$22/hr" into "".
func stripMoney(label string) string {
	l := moneyRe.ReplaceAllString(label, " ")
	l = strings.Join(strings.Fields(l), " ")
	for {
		n := fillerRe.ReplaceAllString(l, "")
		if n == l {
			break
		}
		l = n
	}
	for {
		n := trailingRe.ReplaceAllString(l, "")
		if n == l {
			break
		}
		l = n
	}
	l = strings.Trim(l, " ,:;-.")
	if genericRe.MatchString(strings.ToLower(l)) {
		return ""
	}
	letters := 0
	for _, r := range l {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			letters++
		}
	}
	if letters < 3 {
		return ""
	}
	return l
}

// humanize turns a service key into words: date_night → date night.
func humanize(key string) string { return strings.ReplaceAll(key, "_", " ") }

// clock12 renders "23:00" as "11pm".
func clock12(c Clock) string {
	h, m, err := parseClock(c)
	if err != nil {
		return c
	}
	if h == 24 || h == 0 {
		return "12am"
	}
	suffix := "am"
	if h >= 12 {
		suffix = "pm"
	}
	if h > 12 {
		h -= 12
	}
	if m == 0 {
		return fmt.Sprintf("%d%s", h, suffix)
	}
	return fmt.Sprintf("%d:%02d%s", h, m, suffix)
}

func countable(unit string) bool {
	switch strings.ToLower(unit) {
	case "", "visit", "walk", "session", "each", "day", "night", "clean", "lesson", "week", "month":
		return true
	}
	return false
}

// feeTriggered says whether a fee's trigger is met. unsure is set when the
// line leaves the trigger open (a cancellation with no notice period stated).
func feeTriggered(r Rule, l Line) (ok bool, why, unsure string) {
	if r.Trigger == nil {
		return true, "", ""
	}
	name := firstNonEmpty(r.Label, r.Service)
	switch {
	case r.Trigger.SameDay && !l.Flags["same_day"]:
		return false, fmt.Sprintf("%s applies only to same-day bookings; this line is not marked same-day.", name), ""
	case r.Trigger.CancelledWithinHours > 0:
		if !l.Flags["cancelled"] {
			return false, fmt.Sprintf("%s applies only to cancellations within %d hours; this line is not a cancellation.", name, r.Trigger.CancelledWithinHours), ""
		}
		if l.NoticeHours == nil {
			return true, "", fmt.Sprintf("The notice period was not stated, so whether it was inside %d hours could not be checked.", r.Trigger.CancelledWithinHours)
		}
		if *l.NoticeHours > float64(r.Trigger.CancelledWithinHours) {
			return false, fmt.Sprintf("%s applies to cancellations within %d hours; this one had %s hours of notice.", name, r.Trigger.CancelledWithinHours, trimFloat(*l.NoticeHours)), ""
		}
	case r.Trigger.Holiday:
		if _, ok := USFederalHolidays(yearOf(l.Date))[l.Date]; !ok {
			return false, fmt.Sprintf("%s applies only on holidays; %s is not one.", name, l.Date), ""
		}
	}
	return true, "", ""
}

// ---- index ----

type ruleIndex struct {
	byService map[string][]Rule
	hourly    map[string][]Rule // hourly rules by service
	mults     []Rule
	adders    []Rule
	minimum   *Rule
	rounding  *Rule
}

func index(t Terms) ruleIndex {
	ix := ruleIndex{byService: map[string][]Rule{}, hourly: map[string][]Rule{}}
	for i := range t.Rules {
		r := t.Rules[i]
		if r.Service != "" && priceable[r.Kind] {
			ix.byService[r.Service] = append(ix.byService[r.Service], r)
		}
		switch r.Kind {
		case KindHourly:
			ix.hourly[r.Service] = append(ix.hourly[r.Service], r)
		case KindMultiplier:
			ix.mults = append(ix.mults, r)
		case KindAdder:
			ix.adders = append(ix.adders, r)
		case KindMinimum:
			ix.minimum = &t.Rules[i]
		case KindRounding:
			ix.rounding = &t.Rules[i]
		}
	}
	return ix
}

// baseRate is the unwindowed hourly rule for a service; exactly one must exist.
func (ix ruleIndex) baseRate(svc string) (Rule, string) {
	var bases []Rule
	for _, h := range ix.hourly[svc] {
		if h.When == nil || h.When.IsEmpty() {
			bases = append(bases, h)
		}
	}
	switch len(bases) {
	case 1:
		return bases[0], ""
	case 0:
		return Rule{}, fmt.Sprintf("No base hourly rate agreed for %s.", svc)
	}
	return Rule{}, fmt.Sprintf("Two base hourly rates are on record for %s (%s); the terms need to say which applies.", svc, ids(bases))
}

// applyDateMultipliers scales readings by date-only multipliers (Sundays,
// holidays) that match the line's date. Two matching multipliers make the
// line ambiguous: stacked, or the larger one alone.
func (ix ruleIndex) applyDateMultipliers(readings []Reading, date time.Time) []Reading {
	if date.IsZero() {
		return readings
	}
	var ms []Rule
	for _, m := range ix.mults {
		if m.When != nil && m.When.IsDateOnly() && m.When.Matches(clockOn(date, 0, 12, 0)) {
			ms = append(ms, m)
		}
	}
	if len(ms) == 0 {
		return readings
	}
	product, maxF := 1.0, 0.0
	var labels []string
	var basis []string
	for _, m := range ms {
		product *= m.Factor
		maxF = math.Max(maxF, m.Factor)
		labels = append(labels, firstNonEmpty(m.Label, "multiplier"))
		basis = append(basis, m.ID)
	}
	var out []Reading
	for _, r := range readings {
		s := r
		s.ExpectedCents = Cents(math.Round(float64(r.ExpectedCents) * product))
		s.Basis = append(append([]string{}, r.Basis...), basis...)
		s.Explanation = fmt.Sprintf("%s ×%s (%s) = %s", r.Explanation, trimFloat(product), strings.Join(labels, ", "), s.ExpectedCents)
		out = append(out, s)
		if len(ms) > 1 {
			e := r
			e.ExpectedCents = Cents(math.Round(float64(r.ExpectedCents) * maxF))
			e.Basis = s.Basis
			e.Explanation = fmt.Sprintf("%s ×%s (largest of %s, not stacked) = %s", r.Explanation, trimFloat(maxF), strings.Join(labels, ", "), e.ExpectedCents)
			out = append(out, e)
		}
	}
	return out
}

// ---- hourly ----

type run struct {
	start   time.Time
	minutes int
	spec    []Rule // windowed hourly rules that match
	base    []Rule // unwindowed hourly rules
	mults   []Rule
	adders  []Rule
}

func (ix ruleIndex) signatureAt(svc string, t time.Time) run {
	r := run{}
	for _, h := range ix.hourly[svc] {
		if h.When == nil || h.When.IsEmpty() {
			r.base = append(r.base, h)
		} else if h.When.Matches(t) {
			r.spec = append(r.spec, h)
		}
	}
	for _, m := range ix.mults {
		if m.When == nil || m.When.Matches(t) {
			r.mults = append(r.mults, m)
		}
	}
	for _, a := range ix.adders {
		if a.When == nil || a.When.Matches(t) {
			r.adders = append(r.adders, a)
		}
	}
	return r
}

func ids(rs []Rule) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = r.ID
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

// priceHourly prices a span for one hourly service minute by minute, grouping
// runs with the same applicable rules. Returns one reading, or two when
// stacking is a genuine question (a windowed rate and a multiplier both
// apply, or two multipliers apply).
func sameRate(rs []Rule) bool {
	for _, r := range rs[1:] {
		if r.RateCents != rs[0].RateCents {
			return false
		}
	}
	return true
}

// bookingFromLine prices what a cancelled booking would have cost, from the
// line's times or hours, using the helper's hourly rate (minimum included).
func (ix ruleIndex) bookingFromLine(l Line, span Span, hasSpan bool, date time.Time, loc *time.Location) (Cents, string) {
	svc := ""
	if len(ix.hourly["hourly"]) > 0 {
		svc = "hourly"
	} else if len(ix.hourly) == 1 {
		for k := range ix.hourly {
			svc = k
		}
	}
	if svc == "" {
		return 0, ""
	}
	var rs []Reading
	switch {
	case hasSpan:
		rs, _ = ix.priceHourly(svc, span, l, true)
	case l.Qty > 0:
		rs, _, _, _ = ix.priceHours(svc, l, date, loc)
	}
	if len(rs) == 0 || rs[0].ExpectedCents <= 0 {
		return 0, ""
	}
	return rs[0].ExpectedCents, " (the booking: " + rs[0].Explanation + ")"
}

func (ix ruleIndex) priceHourly(svc string, span Span, l Line, applyMinimum bool) ([]Reading, string) {
	if len(ix.hourly[svc]) == 0 {
		return nil, fmt.Sprintf("No hourly rate agreed for %s.", svc)
	}
	if _, e := ix.baseRate(svc); e != "" && !hasOnlyWindowed(ix.hourly[svc]) {
		return nil, e
	}
	if ix.rounding != nil && ix.rounding.Minutes > 0 {
		if rem := span.Minutes() % ix.rounding.Minutes; rem != 0 {
			span.End = span.End.Add(time.Duration(ix.rounding.Minutes-rem) * time.Minute)
		}
	}
	var runs []run
	sticky := map[string]bool{} // "from 22:00" with no end stays on for the rest of the booking
	for i := 0; i < span.Minutes(); i++ {
		at := span.Start.Add(time.Duration(i) * time.Minute)
		sig := ix.signatureAt(svc, at)
		for _, h := range sig.spec {
			if h.When != nil && h.When.From != "" && h.When.To == "" {
				sticky[h.ID] = true
			}
		}
		for _, h := range ix.hourly[svc] {
			if sticky[h.ID] && !containsRule(sig.spec, h.ID) {
				sig.spec = append(sig.spec, h)
			}
		}
		if len(sig.spec) > 1 && sameRate(sig.spec) {
			sig.spec = sig.spec[:1] // same rate written twice: one run, one explanation line
		}
		if n := len(runs); n > 0 && ids(runs[n-1].spec) == ids(sig.spec) && ids(runs[n-1].mults) == ids(sig.mults) && ids(runs[n-1].adders) == ids(sig.adders) {
			runs[n-1].minutes++
			continue
		}
		sig.start = at
		sig.minutes = 1
		runs = append(runs, sig)
	}
	var stacked, exclusive Cents
	var stackedExpl, exclusiveExpl []string
	basis := map[string]bool{}
	differs := false
	for _, r := range runs {
		if len(r.spec) > 1 {
			// "$28 after 10pm" and "$28 after 11pm" written as two rules: the same
			// rate either way, so there is nothing to decide. Different rates are a conflict.
			if !sameRate(r.spec) {
				return nil, fmt.Sprintf("Two hourly rates both apply at %s (%s); the terms need to say which wins.", r.start.Format("Mon 15:04"), ids(r.spec))
			}
			r.spec = r.spec[:1]
		}
		var base PerHour
		var baseLabel string
		if len(r.base) > 0 {
			base = PerHour(r.base[0].RateCents)
			baseLabel = niceName(r.base[0])
			basis[r.base[0].ID] = true
		} else if len(r.spec) == 0 {
			return nil, fmt.Sprintf("No hourly rate applies at %s.", r.start.Format("Mon 15:04"))
		}
		rate, label := base, baseLabel
		if len(r.spec) == 1 {
			rate, label = PerHour(r.spec[0].RateCents), niceName(r.spec[0])
			basis[r.spec[0].ID] = true
		}
		product, maxF := 1.0, 1.0
		var mlabels []string
		for _, m := range r.mults {
			product *= m.Factor
			maxF = math.Max(maxF, m.Factor)
			mlabels = append(mlabels, firstNonEmpty(m.Label, "multiplier"))
			basis[m.ID] = true
		}
		hrs := fmt.Sprintf("%s × ", minutesText(r.minutes))
		// stacked: every multiplier on the applicable (windowed or base) rate.
		// Amounts are rate × factor × minutes rounded once; the shown rate is rounded for display only.
		sRate := rate.Times(product)
		sAmt := rate.TimesForMinutes(product, r.minutes)
		sExpl := hrs + fmt.Sprintf("%s/hr", Cents(sRate))
		if len(r.spec) == 1 {
			sExpl += " (" + label + ")"
		}
		if product != 1 {
			sExpl = hrs + fmt.Sprintf("%s/hr (%s ×%s, %s)", Cents(sRate), Cents(rate), trimFloat(product), strings.Join(mlabels, ", "))
		}
		// exclusive: the single largest effect, nothing stacked
		eAmt, eExpl := sAmt, sExpl
		if len(r.mults) > 0 && (len(r.spec) == 1 && len(r.base) > 0 || len(r.mults) > 1) {
			differs = true
			type candidate struct {
				rate   PerHour
				factor float64
				expl   string
			}
			candidates := []candidate{{rate, 1, hrs + fmt.Sprintf("%s/hr (%s)", Cents(rate), label)}}
			if len(r.base) > 0 {
				candidates = append(candidates, candidate{base, maxF, hrs + fmt.Sprintf("%s/hr (%s ×%s, largest of %s)", Cents(base.Times(maxF)), baseLabel, trimFloat(maxF), strings.Join(mlabels, ", "))})
			}
			best := candidates[0]
			for _, c := range candidates[1:] {
				if float64(c.rate)*c.factor > float64(best.rate)*best.factor {
					best = c
				}
			}
			eAmt, eExpl = best.rate.TimesForMinutes(best.factor, r.minutes), best.expl
		}
		for _, a := range r.adders {
			units := 1.0
			if a.Per != "" {
				units = l.Extras[a.Per]
			}
			if units == 0 {
				continue
			}
			add := PerHour(math.Round(float64(a.PerHourCents) * units)).ForMinutes(r.minutes)
			sAmt += add
			eAmt += add
			basis[a.ID] = true
			sExpl += fmt.Sprintf(" + %s (%s)", add, firstNonEmpty(a.Label, "adder"))
			eExpl += fmt.Sprintf(" + %s (%s)", add, firstNonEmpty(a.Label, "adder"))
		}
		stacked += sAmt
		exclusive += eAmt
		stackedExpl = append(stackedExpl, sExpl+" = "+sAmt.String())
		exclusiveExpl = append(exclusiveExpl, eExpl+" = "+eAmt.String())
	}
	if applyMinimum && ix.minimum != nil {
		m := ix.minimum
		// judged on the rounded booking, and only ever scaled up
		if m.Hours > 0 && span.Minutes() > 0 && float64(span.Minutes()) < m.Hours*60 {
			// scale the priced booking up to the minimum length, keeping its rates and multipliers
			f := m.Hours * 60 / float64(span.Minutes())
			stacked = Cents(math.Round(float64(stacked) * f))
			exclusive = Cents(math.Round(float64(exclusive) * f))
			stackedExpl = append(stackedExpl, fmt.Sprintf("%s-hour minimum applied = %s", trimFloat(m.Hours), stacked))
			exclusiveExpl = append(exclusiveExpl, fmt.Sprintf("%s-hour minimum applied = %s", trimFloat(m.Hours), exclusive))
			basis[m.ID] = true
		}
		if m.AmountCents > 0 && stacked < m.AmountCents {
			stacked = m.AmountCents
			stackedExpl = []string{fmt.Sprintf("minimum charge %s", m.AmountCents)}
			basis[m.ID] = true
		}
		if m.AmountCents > 0 && exclusive < m.AmountCents {
			exclusive = m.AmountCents
			exclusiveExpl = []string{fmt.Sprintf("minimum charge %s", m.AmountCents)}
			basis[m.ID] = true
		}
	}
	b := make([]string, 0, len(basis))
	for k := range basis {
		b = append(b, k)
	}
	sort.Strings(b)
	out := []Reading{{Service: svc, ExpectedCents: stacked, Basis: b, Explanation: strings.Join(stackedExpl, "; ")}}
	if differs && exclusive != stacked {
		out = append(out, Reading{Service: svc, ExpectedCents: exclusive, Basis: b, Explanation: strings.Join(exclusiveExpl, "; ")})
	}
	return out, ""
}

func hasOnlyWindowed(rs []Rule) bool {
	for _, r := range rs {
		if r.When == nil || r.When.IsEmpty() {
			return false
		}
	}
	return true
}

// priceFlat: the flat amount covers one window on the booking's evening
// ([From, Until], crossing midnight if From > Until); the rest of the span is
// priced hourly as whole pieces so a late rate carries through midnight.
func (ix ruleIndex) priceFlat(r Rule, span Span, l Line) ([]Reading, string) {
	day := time.Date(span.Start.Year(), span.Start.Month(), span.Start.Day(), 0, 0, 0, 0, span.Start.Location())
	covStart, covEnd := span.Start, span.End
	if r.From != "" {
		h, m, _ := parseClock(r.From)
		covStart = clockOn(day, 0, h, m)
	}
	if r.Until != "" {
		h, m, _ := parseClock(r.Until)
		covEnd = clockOn(day, 0, h, m)
		// only a package with a start can run past midnight ("21:00 until 07:00");
		// with only an end, a booking that starts after it is not covered
		if r.From != "" && !covEnd.After(covStart) {
			covEnd = clockOn(day, 1, h, m)
		}
	}
	in := Span{Start: maxT(covStart, span.Start), End: minT(covEnd, span.End)}
	if !in.End.After(in.Start) {
		return nil, fmt.Sprintf("%s covers %s–%s; this line does not overlap it.", niceName(r), firstNonEmpty(r.From, "any time"), firstNonEmpty(r.Until, "end of day"))
	}
	var out []Span
	if span.Start.Before(in.Start) {
		out = append(out, Span{span.Start, in.Start})
	}
	if span.End.After(in.End) {
		out = append(out, Span{in.End, span.End})
	}
	expl := fmt.Sprintf("%s: flat %s", niceName(r), r.AmountCents)
	if r.Until != "" {
		expl += " until " + clock12(r.Until)
	}
	// Sunday/holiday multipliers scale the flat amount here; the overflow below
	// is priced hourly, which applies them on its own. Two matching multipliers
	// give a stacked and a not-stacked flat amount.
	flat := ix.applyDateMultipliers([]Reading{{Service: r.Service, ExpectedCents: r.AmountCents, Basis: []string{r.ID}, Explanation: expl}}, day)
	total, expl, basis := flat[0].ExpectedCents, flat[0].Explanation, flat[0].Basis
	alone := flat[len(flat)-1] // the not-stacked flat amount (the same as flat[0] with at most one multiplier)
	var extra []Reading
	if len(flat) > 1 && len(out) == 0 {
		extra = append(extra, alone)
	}
	if len(out) > 0 {
		if len(ix.hourly["hourly"]) == 0 {
			return nil, fmt.Sprintf("%s of this line fall outside %s and no hourly rate is agreed.", minutesText(sumMinutes(out)), niceName(r))
		}
		var stacked, exclusive Cents
		var sx, ex []string
		differs := false
		for _, piece := range out {
			rs, e := ix.priceHourly("hourly", piece, l, false)
			if e != "" {
				return nil, e
			}
			stacked += rs[0].ExpectedCents
			sx = append(sx, rs[0].Explanation)
			basis = append(basis, rs[0].Basis...)
			if len(rs) > 1 {
				differs = true
				exclusive += rs[1].ExpectedCents
				ex = append(ex, rs[1].Explanation)
			} else {
				exclusive += rs[0].ExpectedCents
				ex = append(ex, rs[0].Explanation)
			}
		}
		if differs || len(flat) > 1 {
			extra = append(extra, Reading{Service: r.Service, ExpectedCents: alone.ExpectedCents + exclusive, Basis: basis, Explanation: alone.Explanation + ", plus " + strings.Join(ex, "; ")})
		}
		total += stacked
		expl += ", plus " + strings.Join(sx, "; ")
	}
	readings := []Reading{{Service: r.Service, ExpectedCents: total, Basis: basis, Explanation: expl}}
	return append(readings, extra...), ""
}

// flatFits says whether a flat package is a plausible reading of a span: the
// booking has to run up to the package's end (within an hour) and, if the
// package has a start, begin near it. A Sunday-morning sit is not a date night.
func flatFits(r Rule, span Span) bool {
	day := time.Date(span.Start.Year(), span.Start.Month(), span.Start.Day(), 0, 0, 0, 0, span.Start.Location())
	if r.Until != "" {
		h, m, err := parseClock(r.Until)
		if err != nil {
			return false
		}
		until := clockOn(day, 0, h, m)
		if r.From != "" {
			if fh, fm, err := parseClock(r.From); err == nil && fh*60+fm > h*60+m {
				until = clockOn(day, 1, h, m)
			}
		}
		if span.End.Before(until.Add(-60 * time.Minute)) {
			return false
		}
	}
	if r.From != "" {
		h, m, err := parseClock(r.From)
		if err != nil {
			return false
		}
		from := clockOn(day, 0, h, m)
		if span.Start.After(from.Add(60 * time.Minute)) {
			return false
		}
	}
	return true
}

// priceHours prices an hourly line given as a number of hours with no clock
// times. The hours are priced as a span from midnight on the line's date, so
// everything the date decides (Sunday and holiday multipliers, date-only
// rates) and everything the length decides (minimum, rounding, adders) applies.
// Rules the line cannot be judged by (clock windows such as "after 10pm"; with
// no date, every window) are left out of outside and taken to cover every hour
// in inside; unjudged names them. inside is empty when nothing was unjudged.
func (ix ruleIndex) priceHours(svc string, l Line, date time.Time, loc *time.Location) (outside, inside []Reading, unjudged []string, err string) {
	unknown := func(w *Window) bool {
		if w == nil || w.IsEmpty() {
			return false
		}
		return date.IsZero() || w.From != "" || w.To != ""
	}
	strip := func(w *Window) *Window {
		if date.IsZero() {
			return nil
		}
		c := *w
		c.From, c.To = "", ""
		if c.IsEmpty() {
			return nil
		}
		return &c
	}
	seen := map[string]bool{}
	note := func(r Rule) {
		if n := niceName(r); !seen[n] {
			seen[n] = true
			unjudged = append(unjudged, n)
		}
	}
	out, in := ix, ix
	out.hourly, in.hourly = map[string][]Rule{}, map[string][]Rule{}
	out.mults, in.mults, out.adders, in.adders = nil, nil, nil, nil
	promoted := false
	for _, h := range ix.hourly[svc] {
		if !unknown(h.When) {
			out.hourly[svc] = append(out.hourly[svc], h)
			continue
		}
		note(h)
		h.When = strip(h.When)
		promoted = promoted || h.When == nil
		in.hourly[svc] = append(in.hourly[svc], h)
	}
	for _, h := range out.hourly[svc] {
		// an evening rate taken as covering every hour replaces the base rate
		if !promoted || (h.When != nil && !h.When.IsEmpty()) {
			in.hourly[svc] = append(in.hourly[svc], h)
		}
	}
	for _, m := range ix.mults {
		if unknown(m.When) {
			note(m)
			m.When = strip(m.When)
			in.mults = append(in.mults, m)
		} else {
			out.mults = append(out.mults, m)
			in.mults = append(in.mults, m)
		}
	}
	for _, a := range ix.adders {
		if unknown(a.When) {
			note(a)
			a.When = strip(a.When)
			in.adders = append(in.adders, a)
		} else {
			out.adders = append(out.adders, a)
			in.adders = append(in.adders, a)
		}
	}
	day := date
	if day.IsZero() {
		day = time.Date(2001, 1, 2, 0, 0, 0, 0, loc) // any date: with no date every dated rule was set aside above
	}
	span := Span{Start: day, End: day.Add(time.Duration(math.Round(l.Qty*60)) * time.Minute)}
	outside, e1 := out.priceHourly(svc, span, l, true)
	if len(unjudged) > 0 {
		var e2 string
		if inside, e2 = in.priceHourly(svc, span, l, true); e2 != "" {
			inside = nil
		}
	}
	if e1 != "" {
		if len(inside) == 0 {
			return nil, nil, nil, e1
		}
		return nil, inside, unjudged, "" // only windowed rates are agreed: nothing prices the hours without them
	}
	return outside, inside, unjudged, ""
}

func extrasText(ex map[string]float64) string {
	ks := make([]string, 0, len(ex))
	for k, v := range ex {
		ks = append(ks, fmt.Sprintf("%s: %s", k, trimFloat(v)))
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// dedupeReadings drops readings with the same amount as an earlier one.
func dedupeReadings(rs []Reading) []Reading {
	seen := map[Cents]bool{}
	var out []Reading
	for _, r := range rs {
		if !seen[r.ExpectedCents] {
			seen[r.ExpectedCents] = true
			out = append(out, r)
		}
	}
	return out
}

func joinOr(parts []string) string {
	if len(parts) == 0 {
		return "no agreed rate applies"
	}
	return strings.Join(parts, " or ")
}

// ---- helpers ----

func containsRule(rs []Rule, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func sumMinutes(ss []Span) int {
	n := 0
	for _, s := range ss {
		n += s.Minutes()
	}
	return n
}

func minutesText(m int) string {
	h, mm := m/60, m%60
	switch {
	case mm == 0:
		return fmt.Sprintf("%dh", h)
	case h == 0:
		return fmt.Sprintf("%dmin", mm)
	default:
		return fmt.Sprintf("%dh%02d", h, mm)
	}
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y := 0
	for _, r := range date[:4] {
		if r < '0' || r > '9' {
			return 0
		}
		y = y*10 + int(r-'0')
	}
	return y
}

// withArticle prefixes "a"/"an" for the information notes ("an hourly", "a date night").
func withArticle(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "hour"), strings.HasPrefix(lower, "honor"), strings.HasPrefix(lower, "honour"):
		return "an " + s
	case strings.ContainsRune("aeiou", rune(lower[0])):
		return "an " + s
	}
	return "a " + s
}
