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
		case l.Qty > 0 && l.Date != "":
			// "4 hours, $88": no times, so no windows or multipliers can be judged; price at the base rate and say so.
			base, e := ix.baseRate(svc)
			if e != "" {
				return finish(res, nil, nil, e, StatusUnpriceable)
			}
			mins := int(math.Round(l.Qty * 60))
			amt := PerHour(base.RateCents).ForMinutes(mins)
			readings = []Reading{{Service: svc, ExpectedCents: amt, Basis: []string{base.ID}, Explanation: fmt.Sprintf("%s × %s/hr = %s", minutesText(mins), base.RateCents, amt)}}
			info = append(info, "No start and end times were given, so evening, Sunday or holiday rates could not be checked; priced at the base rate.")
		default:
			return finish(res, nil, nil, "Hourly line without a date and start/end times (or a number of hours).", StatusUnpriceable)
		}
	case KindFlat:
		if !hasSpan {
			readings = []Reading{{Service: svc, ExpectedCents: primary[0].AmountCents, Basis: []string{primary[0].ID},
				Explanation: fmt.Sprintf("%s: flat %s", niceName(primary[0]), primary[0].AmountCents)}}
		} else {
			readings, err = ix.priceFlat(primary[0], span, l)
		}
		if err == "" && !lineDate.IsZero() {
			readings = ix.applyDateMultipliers(readings, lineDate)
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
		if ok, why := feeTriggered(r, l); !ok {
			return finish(res, nil, nil, why, StatusUnagreed)
		}
		exp := r.AmountCents
		expl := fmt.Sprintf("%s: %s", niceName(r), r.AmountCents)
		if r.PercentOfBooking > 0 {
			if l.BookingCents == 0 {
				return finish(res, nil, nil, fmt.Sprintf("%s is %v%% of the booking, but the booking amount is unknown.", niceName(r), r.PercentOfBooking), StatusUnpriceable)
			}
			exp = Cents(math.Round(float64(l.BookingCents) * r.PercentOfBooking / 100))
			expl = fmt.Sprintf("%s: %v%% of %s = %s", niceName(r), r.PercentOfBooking, l.BookingCents, exp)
		}
		readings = []Reading{{Service: svc, ExpectedCents: exp, Basis: []string{r.ID}, Explanation: expl}}
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
	res.Info = append(res.Info, info...)

	// Alternative readings: other ways the same span could be priced under the terms.
	var alts []Reading
	if hasSpan {
		for _, r := range t.Rules {
			if r.Kind == KindFlat && r.Service != svc && flatFits(r, span) {
				if rs, e := ix.priceFlat(r, span, l); e == "" && len(rs) == 1 {
					alts = append(alts, ix.applyDateMultipliers(rs, lineDate)[0])
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
			res.Explanation = fmt.Sprintf("%s. But read as a %s it would be %s (%s). Billed %s.", readings[0].Explanation, humanize(a.Service), a.ExpectedCents, a.Explanation, claimed)
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

func feeTriggered(r Rule, l Line) (bool, string) {
	if r.Trigger == nil {
		return true, ""
	}
	name := firstNonEmpty(r.Label, r.Service)
	switch {
	case r.Trigger.SameDay && !l.Flags["same_day"]:
		return false, fmt.Sprintf("%s applies only to same-day bookings; this line is not marked same-day.", name)
	case r.Trigger.CancelledWithinHours > 0:
		if !l.Flags["cancelled"] {
			return false, fmt.Sprintf("%s applies only to cancellations within %d hours; this line is not a cancellation.", name, r.Trigger.CancelledWithinHours)
		}
		if l.NoticeHours > float64(r.Trigger.CancelledWithinHours) {
			return false, fmt.Sprintf("%s applies to cancellations within %d hours; this one had %s hours of notice.", name, r.Trigger.CancelledWithinHours, trimFloat(l.NoticeHours))
		}
	case r.Trigger.Holiday:
		if _, ok := USFederalHolidays(yearOf(l.Date))[l.Date]; !ok {
			return false, fmt.Sprintf("%s applies only on holidays; %s is not one.", name, l.Date)
		}
	}
	return true, ""
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
		if r.Service != "" {
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
		if m.When != nil && m.When.IsDateOnly() && m.When.Matches(date.Add(12*time.Hour)) {
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
func (ix ruleIndex) priceHourly(svc string, span Span, l Line, applyMinimum bool) ([]Reading, string) {
	if len(ix.hourly[svc]) == 0 {
		return nil, fmt.Sprintf("No hourly rate agreed for %s.", svc)
	}
	if _, e := ix.baseRate(svc); e != "" && !hasOnlyWindowed(ix.hourly[svc]) {
		return nil, e
	}
	origMinutes := span.Minutes()
	if ix.rounding != nil && ix.rounding.Minutes > 0 {
		if rem := origMinutes % ix.rounding.Minutes; rem != 0 {
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
			return nil, fmt.Sprintf("Two hourly rates both apply at %s (%s); the terms need to say which wins.", r.start.Format("Mon 15:04"), ids(r.spec))
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
		// stacked: every multiplier on the applicable (windowed or base) rate
		sRate := rate.Times(product)
		sAmt := sRate.ForMinutes(r.minutes)
		sExpl := hrs + fmt.Sprintf("%s/hr", Cents(sRate))
		if len(r.spec) == 1 {
			sExpl += " (" + label + ")"
		}
		if product != 1 {
			sExpl = hrs + fmt.Sprintf("%s/hr (%s ×%s, %s)", Cents(sRate), Cents(rate), trimFloat(product), strings.Join(mlabels, ", "))
		}
		// exclusive: the single largest effect, nothing stacked
		eRate, eExpl := sRate, sExpl
		if len(r.mults) > 0 && (len(r.spec) == 1 && len(r.base) > 0 || len(r.mults) > 1) {
			differs = true
			candidates := []struct {
				rate PerHour
				expl string
			}{{rate, hrs + fmt.Sprintf("%s/hr (%s)", Cents(rate), label)}}
			_ = baseLabel
			if len(r.base) > 0 {
				candidates = append(candidates, struct {
					rate PerHour
					expl string
				}{base.Times(maxF), hrs + fmt.Sprintf("%s/hr (%s ×%s, largest of %s)", Cents(base.Times(maxF)), baseLabel, trimFloat(maxF), strings.Join(mlabels, ", "))})
			}
			eRate, eExpl = candidates[0].rate, candidates[0].expl
			for _, c := range candidates[1:] {
				if c.rate > eRate {
					eRate, eExpl = c.rate, c.expl
				}
			}
		}
		eAmt := eRate.ForMinutes(r.minutes)
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
		if m.Hours > 0 && float64(origMinutes) < m.Hours*60 && origMinutes > 0 {
			// scale the priced booking up to the minimum length, keeping its rates and multipliers
			f := m.Hours * 60 / float64(span.Minutes())
			stacked = Cents(math.Round(float64(stacked) * f))
			exclusive = Cents(math.Round(float64(exclusive) * f))
			stackedExpl = append(stackedExpl, fmt.Sprintf("%s-hour minimum applied = %s", trimFloat(m.Hours), stacked))
			exclusiveExpl = append(exclusiveExpl, fmt.Sprintf("%s-hour minimum applied = %s", trimFloat(m.Hours), exclusive))
			basis[m.ID] = true
		}
		if m.AmountCents > 0 && stacked < m.AmountCents {
			stacked, exclusive = m.AmountCents, m.AmountCents
			stackedExpl = []string{fmt.Sprintf("minimum charge %s", m.AmountCents)}
			exclusiveExpl = stackedExpl
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
		covStart = day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	if r.Until != "" {
		h, m, _ := parseClock(r.Until)
		covEnd = day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		if !covEnd.After(covStart) {
			covEnd = covEnd.Add(24 * time.Hour)
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
	total := r.AmountCents
	expl := fmt.Sprintf("%s: flat %s", niceName(r), r.AmountCents)
	if r.Until != "" {
		expl += " until " + clock12(r.Until)
	}
	basis := []string{r.ID}
	var extra []Reading
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
		if differs {
			extra = append(extra, Reading{Service: r.Service, ExpectedCents: total + exclusive, Basis: basis, Explanation: expl + ", plus " + strings.Join(ex, "; ")})
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
		until := day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		if r.From != "" {
			if fh, fm, err := parseClock(r.From); err == nil && fh*60+fm > h*60+m {
				until = until.Add(24 * time.Hour)
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
		from := day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		if span.Start.After(from.Add(60 * time.Minute)) {
			return false
		}
	}
	return true
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
