package pricing

import (
	"strings"
	"testing"
)

// priyaTerms mirrors the Jun 14 text thread.
func priyaTerms() Terms {
	return Terms{
		HelperID: "priya", EffectiveFrom: "2026-06-14", Label: "Priya, terms since 2026-06-14",
		Rules: []Rule{
			{ID: "p1", Kind: KindHourly, Service: "hourly", Label: "hourly", RateCents: 2200},
			{ID: "p2", Kind: KindHourly, Service: "hourly", Label: "after 10pm", RateCents: 2800, When: &Window{From: "22:00"}},
			{ID: "p3", Kind: KindMultiplier, Label: "Sundays time and a half", Factor: 1.5, When: &Window{Days: []Day{"sun"}}},
			{ID: "p4", Kind: KindFlat, Service: "date_night", Label: "date night", AmountCents: 10000, Until: "23:00"},
		},
	}
}

func line(date, service, start, end string, claimed Cents) Line {
	return Line{Date: date, Service: service, Start: start, End: end, ClaimedCents: claimed}
}

func TestPriyaLines(t *testing.T) {
	terms := priyaTerms()
	loc := NY()
	cases := []struct {
		name   string
		l      Line
		status Status
		exp    Cents
	}{
		{"date night with half hour late", line("2026-09-04", "date_night", "18:00", "23:30", 11400), StatusMatch, 11400},
		{"after school", line("2026-09-09", "hourly", "15:00", "19:00", 8800), StatusMatch, 8800},
		{"saturday hourly into late", line("2026-09-12", "hourly", "19:00", "24:00", 12200), StatusMatch, 12200},
		{"sunday time and a half", line("2026-09-20", "hourly", "10:00", "14:00", 13200), StatusMatch, 13200},
		{"date night flat", line("2026-09-25", "date_night", "18:00", "23:00", 10000), StatusMatch, 10000},
		{"unagreed fee", Line{Date: "2026-09-20", Service: "last_minute_fee", ClaimedCents: 4000, Note: "last minute fee"}, StatusUnagreed, 0},
		{"over on hourly", line("2026-09-09", "hourly", "15:00", "19:00", 9000), StatusOver, 8800},
		{"under on hourly", line("2026-09-09", "hourly", "15:00", "19:00", 8000), StatusUnder, 8800},
		{"friday evening billed hourly when date night is cheaper", line("2026-09-11", "hourly", "18:00", "23:00", 11600), StatusAmbiguous, 11600},
		{"unlabelled span prices as hourly", line("2026-09-09", "", "15:00", "19:00", 8800), StatusMatch, 8800},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := PriceLine(c.l, terms, loc)
			if r.Status != c.status || r.ExpectedCents != c.exp {
				t.Errorf("got status=%s expected=%d (%s); want %s %d", r.Status, r.ExpectedCents, r.Explanation, c.status, c.exp)
			}
			if r.Explanation == "" {
				t.Errorf("empty explanation")
			}
		})
	}
	// Sat 9/12: alternative date-night reading is $128 and shows as info, not ambiguity.
	r := PriceLine(line("2026-09-12", "hourly", "19:00", "24:00", 12200), terms, loc)
	if len(r.Readings) < 2 || r.Readings[1].Service != "date_night" || r.Readings[1].ExpectedCents != 12800 {
		t.Errorf("want date_night alternative 12800, got %+v", r.Readings)
	}
	if len(r.Info) == 0 {
		t.Errorf("want info note about the alternative reading")
	}
	// Friday ambiguous: both readings present
	r = PriceLine(line("2026-09-11", "hourly", "18:00", "23:00", 11600), terms, loc)
	if len(r.Readings) != 2 || r.Readings[1].ExpectedCents != 10000 {
		t.Errorf("ambiguous readings: %+v", r.Readings)
	}
}

func lupeTerms(from string, regular Cents) Terms {
	return Terms{HelperID: "lupe", EffectiveFrom: from, DefaultPerVisit: "regular_visit", Label: "Lupe, terms since " + from,
		Rules: []Rule{
			{ID: "l1", Kind: KindPerUnit, Service: "regular_visit", Label: "regular visit", Unit: "visit", AmountCents: regular},
			{ID: "l2", Kind: KindPerUnit, Service: "deep_clean", Label: "deep clean", Unit: "visit", AmountCents: 22000},
		}}
}

func TestPerUnit(t *testing.T) {
	loc := NY()
	terms := lupeTerms("2026-01-14", 14000)
	r := PriceLine(Line{Date: "2026-09-01", ClaimedCents: 15000}, terms, loc) // unlabelled → default per visit
	if r.Status != StatusOver || r.ExpectedCents != 14000 {
		t.Errorf("unlabelled visit: %s %d", r.Status, r.ExpectedCents)
	}
	r = PriceLine(Line{Date: "2026-09-22", Service: "deep_clean", ClaimedCents: 22000}, terms, loc)
	if r.Status != StatusMatch {
		t.Errorf("deep clean: %s %d", r.Status, r.ExpectedCents)
	}
	theo := Terms{HelperID: "theo", EffectiveFrom: "2026-09-01", Rules: []Rule{
		{ID: "t1", Kind: KindPerUnit, Service: "walk_30", Unit: "walk", AmountCents: 2800},
		{ID: "t2", Kind: KindPerUnit, Service: "walk_60", Unit: "walk", AmountCents: 3800},
	}}
	r = PriceLine(Line{Service: "walk_30", Qty: 18, ClaimedCents: 50400}, theo, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 50400 {
		t.Errorf("walks: %s %d", r.Status, r.ExpectedCents)
	}
	r = PriceLine(Line{Service: "grooming", Qty: 1, ClaimedCents: 5000}, theo, loc)
	if r.Status != StatusUnagreed {
		t.Errorf("unknown service: %s", r.Status)
	}
}

func TestExtraKinds(t *testing.T) {
	loc := NY()
	terms := Terms{HelperID: "x", EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 3000},
		{ID: "min", Kind: KindMinimum, Hours: 3, Per: "booking"},
		{ID: "sd", Kind: KindFee, Service: "same_day_fee", Label: "same-day booking fee", AmountCents: 2000, Trigger: &Trigger{SameDay: true}},
		{ID: "cx", Kind: KindFee, Service: "late_cancel", Label: "late cancellation", PercentOfBooking: 50, Trigger: &Trigger{CancelledWithinHours: 24}},
		{ID: "ad", Kind: KindAdder, Label: "+$3/hr per extra child", PerHourCents: 300, Per: "extra_child"},
		{ID: "re", Kind: KindReimbursement, Service: "groceries", CapCents: 5000, ReceiptRequired: true},
		{ID: "pp", Kind: KindPerPeriod, Service: "retainer", AmountCents: 40000, Period: "week"},
		{ID: "ov", Kind: KindFlat, Service: "overnight", AmountCents: 15000, From: "21:00", Until: "07:00"},
		{ID: "hol", Kind: KindMultiplier, Label: "holidays double", Factor: 2, When: &Window{Dates: "us_federal_holidays"}},
	}}
	check := func(name string, l Line, status Status, exp Cents) {
		t.Helper()
		r := PriceLine(l, terms, loc)
		if r.Status != status || r.ExpectedCents != exp {
			t.Errorf("%s: got %s %d (%s) want %s %d", name, r.Status, r.ExpectedCents, r.Explanation, status, exp)
		}
	}
	twoHours := 2.0
	check("minimum 3h on 2h", line("2026-03-03", "hourly", "10:00", "12:00", 9000), StatusMatch, 9000)
	check("same-day fee with flag", Line{Date: "2026-03-03", Service: "same_day_fee", ClaimedCents: 2000, Flags: map[string]bool{"same_day": true}}, StatusMatch, 2000)
	check("same-day fee without flag", Line{Date: "2026-03-03", Service: "same_day_fee", ClaimedCents: 2000}, StatusUnagreed, 0)
	check("late cancel 50% of 9000 booking", Line{Date: "2026-03-03", Service: "late_cancel", ClaimedCents: 4500, BookingCents: 9000, Flags: map[string]bool{"cancelled": true}, NoticeHours: &twoHours}, StatusMatch, 4500)
	check("adder extra child 4h", Line{Date: "2026-03-03", Service: "hourly", Start: "10:00", End: "14:00", ClaimedCents: 13200, Extras: map[string]float64{"extra_child": 1}}, StatusMatch, 13200)
	check("reimbursement over cap", Line{Date: "2026-03-03", Service: "groceries", ClaimedCents: 6000}, StatusOver, 5000)
	check("retainer", Line{Service: "retainer", Qty: 2, ClaimedCents: 80000}, StatusMatch, 80000)
	check("overnight flat crossing midnight", line("2026-03-03", "overnight", "21:00", "07:00", 15000), StatusMatch, 15000)
	check("overnight with overflow priced hourly", line("2026-03-03", "overnight", "21:00", "09:00", 21000), StatusMatch, 21000)
	check("holiday double", line("2026-09-07", "hourly", "10:00", "14:00", 24000), StatusMatch, 24000)

	r := PriceLine(Line{Date: "2026-03-03", Service: "groceries", ClaimedCents: 4000}, terms, loc)
	if len(r.Info) == 0 {
		t.Errorf("receipt required should add an info note")
	}
}

func TestRounding(t *testing.T) {
	loc := NY()
	base := Terms{HelperID: "x", EffectiveFrom: "2026-01-01", Rules: []Rule{{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2400}}}
	r := PriceLine(line("2026-03-03", "hourly", "10:00", "14:05", 9800), base, loc)
	if r.ExpectedCents != 9800 { // exact minutes: 245 min × $24/h = $98
		t.Errorf("no rounding rule: %d", r.ExpectedCents)
	}
	base.Rules = append(base.Rules, Rule{ID: "r", Kind: KindRounding, Minutes: 15})
	r = PriceLine(line("2026-03-03", "hourly", "10:00", "14:05", 10200), base, loc)
	if r.ExpectedCents != 10200 { // 4h15 × $24 = $102
		t.Errorf("rounding 15: %d", r.ExpectedCents)
	}
}

func TestStackingAmbiguity(t *testing.T) {
	loc := NY()
	terms := priyaTerms()
	// Sunday 22:00–23:00: late $28 vs Sunday ×1.5 → stacked $42 or exclusive $33
	r := PriceLine(line("2026-09-20", "hourly", "22:00", "23:00", 4200), terms, loc)
	if r.Status != StatusAmbiguous {
		t.Fatalf("want ambiguous, got %s %d %s", r.Status, r.ExpectedCents, r.Explanation)
	}
	got := map[Cents]bool{}
	for _, rd := range r.Readings {
		got[rd.ExpectedCents] = true
	}
	if !got[4200] || !got[3300] {
		t.Errorf("want readings 4200 and 3300, got %+v", r.Readings)
	}
	// Saturday 23:00 → Sunday 01:00 : Sat part late $28, Sun part ambiguous (late vs sunday stack)
	r = PriceLine(line("2026-09-19", "hourly", "23:00", "01:00", 7000), terms, loc)
	if r.Status != StatusAmbiguous {
		t.Errorf("crossing into Sunday should be ambiguous: %s %d", r.Status, r.ExpectedCents)
	}
	// Sunday daytime only: no stacking question
	r = PriceLine(line("2026-09-20", "hourly", "10:00", "14:00", 13200), terms, loc)
	if r.Status != StatusMatch {
		t.Errorf("sunday daytime: %s", r.Status)
	}
}

func TestUnpriceable(t *testing.T) {
	loc := NY()
	terms := priyaTerms()
	r := PriceLine(Line{Date: "2026-09-09", Service: "hourly", ClaimedCents: 8800}, terms, loc) // no span, no qty
	if r.Status != StatusUnpriceable {
		t.Errorf("hourly without span: %s", r.Status)
	}
	r = PriceLine(line("2026-09-09", "hourly", "3pm", "7pm", 8800), terms, loc)
	if r.Status != StatusUnpriceable {
		t.Errorf("bad clock: %s", r.Status)
	}
}

// ---- cases from the pre-push review ----

func TestDateNightPastMidnight(t *testing.T) {
	loc := NY()
	terms := priyaTerms()
	// $100 flat until 23:00, then late rate carries through midnight: 100 + 2h × $28 = $156
	r := PriceLine(line("2026-09-25", "date_night", "18:00", "01:00", 15600), terms, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 15600 {
		t.Errorf("date night to 1am: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// same with an explicit from
	terms.Rules[3].From = "18:00"
	r = PriceLine(line("2026-09-25", "date_night", "18:00", "01:00", 15600), terms, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 15600 {
		t.Errorf("date night with from: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
}

func TestTwoHourlyServices(t *testing.T) {
	loc := NY()
	tt := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "a", Kind: KindHourly, Service: "hourly", RateCents: 2200},
		{ID: "b", Kind: KindHourly, Service: "tutoring", RateCents: 3500}}}
	r := PriceLine(line("2026-09-09", "tutoring", "15:00", "17:00", 7000), tt, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 7000 {
		t.Errorf("tutoring: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	r = PriceLine(line("2026-09-09", "hourly", "15:00", "17:00", 4400), tt, loc)
	if r.Status != StatusMatch {
		t.Errorf("babysitting: %s %d", r.Status, r.ExpectedCents)
	}
	// two base rates on the same service → unpriceable, never a silent pick
	bad := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "a", Kind: KindHourly, Service: "hourly", RateCents: 2200},
		{ID: "b", Kind: KindHourly, Service: "hourly", RateCents: 2500}}}
	if r := PriceLine(line("2026-09-09", "hourly", "15:00", "17:00", 4400), bad, loc); r.Status != StatusUnpriceable {
		t.Errorf("two base rates: %s", r.Status)
	}
}

func TestMultipliersOnPerUnitAndFlat(t *testing.T) {
	loc := NY()
	th := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "w", Kind: KindPerUnit, Service: "walk_30", Unit: "walk", AmountCents: 2800},
		{ID: "x", Kind: KindMultiplier, Label: "holidays double", Factor: 2, When: &Window{Dates: "us_federal_holidays"}}}}
	r := PriceLine(Line{Date: "2026-09-07", Service: "walk_30", Qty: 1, ClaimedCents: 5600}, th, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 5600 {
		t.Errorf("holiday walk: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	r = PriceLine(Line{Date: "2026-09-08", Service: "walk_30", Qty: 1, ClaimedCents: 2800}, th, loc)
	if r.Status != StatusMatch {
		t.Errorf("normal walk: %s %d", r.Status, r.ExpectedCents)
	}
	// holiday date night: flat doubles too
	p := priyaTerms()
	p.Rules = append(p.Rules, Rule{ID: "h", Kind: KindMultiplier, Label: "holidays double", Factor: 2, When: &Window{Dates: "us_federal_holidays"}})
	r = PriceLine(line("2026-09-07", "date_night", "18:00", "23:00", 20000), p, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 20000 {
		t.Errorf("holiday date night: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// July 4 2026 is a Saturday: the actual date counts as the holiday, and so does the observed Friday
	r = PriceLine(Line{Date: "2026-07-04", Service: "walk_30", Qty: 1, ClaimedCents: 5600}, th, loc)
	if r.ExpectedCents != 5600 {
		t.Errorf("july 4 actual: %d", r.ExpectedCents)
	}
}

func TestHourlyWithQtyHoursOnly(t *testing.T) {
	loc := NY()
	r := PriceLine(Line{Date: "2026-09-09", Service: "hourly", Qty: 4, ClaimedCents: 8800, Note: "Wed 9/9 4 hours"}, priyaTerms(), loc)
	if r.Status != StatusMatch || r.ExpectedCents != 8800 || len(r.Info) == 0 {
		t.Errorf("4 hours no times: %s %d info=%v", r.Status, r.ExpectedCents, r.Info)
	}
}

func TestTwoMultipliersAmbiguousAndMinimumScales(t *testing.T) {
	loc := NY()
	p := priyaTerms()
	p.Rules = append(p.Rules, Rule{ID: "h", Kind: KindMultiplier, Label: "holidays double", Factor: 2, When: &Window{Dates: "us_federal_holidays"}})
	// a Sunday that is also a holiday? None in 2026 federal list falls on Sunday... use exact date on a Sunday
	p.Rules[len(p.Rules)-1].When = &Window{ExactDates: []string{"2026-09-20"}}
	r := PriceLine(line("2026-09-20", "hourly", "10:00", "12:00", 8800), p, loc)
	if r.Status != StatusAmbiguous {
		t.Fatalf("two multipliers should be ambiguous: %s %d", r.Status, r.ExpectedCents)
	}
	got := map[Cents]bool{}
	for _, rd := range r.Readings {
		got[rd.ExpectedCents] = true
	}
	if !got[13200] || !got[8800] { // ×3 stacked = $132, ×2 alone = $88
		t.Errorf("readings: %+v", r.Readings)
	}
	// minimum scales the priced booking, keeping the Sunday multiplier: 2h Sunday with 3h minimum → 3 × $33 = $99
	m := priyaTerms()
	m.Rules = append(m.Rules, Rule{ID: "min", Kind: KindMinimum, Hours: 3, Per: "booking"})
	r = PriceLine(line("2026-09-20", "hourly", "10:00", "12:00", 9900), m, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 9900 {
		t.Errorf("sunday minimum: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
}

func TestPerUnitQuantityRequiredForMileage(t *testing.T) {
	loc := NY()
	tt := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{{ID: "m", Kind: KindPerUnit, Service: "mileage", Unit: "mile", AmountCents: 70}}}
	r := PriceLine(Line{Date: "2026-09-09", Service: "mileage", ClaimedCents: 1400}, tt, loc)
	if r.Status != StatusUnpriceable {
		t.Errorf("mileage without miles: %s %d", r.Status, r.ExpectedCents)
	}
	r = PriceLine(Line{Date: "2026-09-09", Service: "mileage", Qty: 20, ClaimedCents: 1400}, tt, loc)
	if r.Status != StatusMatch {
		t.Errorf("20 miles: %s %d", r.Status, r.ExpectedCents)
	}
}

func TestCancellationNotice(t *testing.T) {
	loc := NY()
	tt := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 3000},
		{ID: "cx", Kind: KindFee, Service: "late_cancel", AmountCents: 5000, Trigger: &Trigger{CancelledWithinHours: 24}}}}
	h48, h3 := 48.0, 3.0
	r := PriceLine(Line{Date: "2026-03-03", Service: "late_cancel", ClaimedCents: 5000, Flags: map[string]bool{"cancelled": true}, NoticeHours: &h48}, tt, loc)
	if r.Status != StatusUnagreed {
		t.Errorf("48h notice should not trigger a 24h fee: %s", r.Status)
	}
	r = PriceLine(Line{Date: "2026-03-03", Service: "late_cancel", ClaimedCents: 5000, Flags: map[string]bool{"cancelled": true}, NoticeHours: &h3}, tt, loc)
	if r.Status != StatusMatch {
		t.Errorf("3h notice: %s", r.Status)
	}
	// no notice stated: never "as agreed"; Jordan decides, with the fee shown as the reading
	r = PriceLine(Line{Date: "2026-03-03", Service: "late_cancel", ClaimedCents: 5000, Flags: map[string]bool{"cancelled": true}}, tt, loc)
	if r.Status != StatusAmbiguous || len(r.Readings) != 1 || r.Readings[0].ExpectedCents != 5000 || !strings.Contains(r.Explanation, "notice period was not stated") {
		t.Errorf("notice not stated: %s %+v %s", r.Status, r.Readings, r.Explanation)
	}
	// zero hours stated is a real answer, not missing
	zero := 0.0
	if r = PriceLine(Line{Date: "2026-03-03", Service: "late_cancel", ClaimedCents: 5000, Flags: map[string]bool{"cancelled": true}, NoticeHours: &zero}, tt, loc); r.Status != StatusMatch {
		t.Errorf("0h notice: %s", r.Status)
	}
}

func TestBadClockInRuleIsRejectedNotSwallowed(t *testing.T) {
	loc := NY()
	p := priyaTerms()
	p.Rules[1].When.From = "10pm"
	r := PriceLine(line("2026-09-09", "hourly", "15:00", "19:00", 8800), p, loc)
	if r.Status != StatusUnpriceable {
		t.Errorf("bad clock in a rule must be unpriceable, got %s %d", r.Status, r.ExpectedCents)
	}
	if errs := ValidateTerms(p); len(errs) == 0 {
		t.Errorf("ValidateTerms should reject 10pm")
	}
	if errs := ValidateTerms(priyaTerms()); len(errs) != 0 {
		t.Errorf("valid terms rejected: %v", errs)
	}
	bad := Terms{EffectiveFrom: "Sept 15", Rules: []Rule{{Kind: "multiplier"}, {Kind: "per_unit", Service: "x"}, {Kind: "weird"}}}
	errs := ValidateTerms(bad)
	if len(errs) < 4 {
		t.Errorf("want errors for date, factor, amount, kind: %v", errs)
	}
}

func TestNiceNameKeepsWordsDropsMoney(t *testing.T) {
	cases := map[string]string{
		"$28 for a 30-min walk": "30-min walk", "$22/hr": "", "after 10pm it's $28/hr": "after 10pm", "deep clean (inside oven, fridge, baseboards) is $220": "deep clean (inside oven, fridge, baseboards)",
		"Sundays are time and a half": "Sundays are time and a half", "$140 each visit": "", "starting the 15th my regular visit will be $150": "starting the 15th my regular visit",
	}
	for in, want := range cases {
		if got := stripMoney(in); got != want {
			t.Errorf("stripMoney(%q)=%q want %q", in, got, want)
		}
	}
	if n := niceName(Rule{Kind: KindPerUnit, Service: "walk_30", Label: "$28 for a 30-min walk"}); n != "30-min walk" {
		t.Errorf("niceName: %q", n)
	}
	if n := niceName(Rule{Kind: KindPerUnit, Service: "regular_visit", Label: "$140 each visit"}); n != "regular visit" {
		t.Errorf("niceName fallback: %q", n)
	}
}

// ---- cases from the 2026-10-15 engine review ----

func readingSet(rs []Reading) map[Cents]bool {
	got := map[Cents]bool{}
	for _, rd := range rs {
		got[rd.ExpectedCents] = true
	}
	return got
}

func TestSundayDateNightOverflowMultipliedOnce(t *testing.T) {
	loc := NY()
	// $100 ×1.5 = $150, plus 30min late: $28 ×1.5 stacked = $21 → $171, or the larger of $28 and $22×1.5 = $33 → $16.50 → $166.50
	r := PriceLine(line("2026-09-20", "date_night", "18:00", "23:30", 17100), priyaTerms(), loc)
	got := readingSet(r.Readings)
	if r.Status != StatusAmbiguous || !got[17100] || !got[16650] || got[18150] || got[17475] {
		t.Errorf("sunday date night: %s %d %+v", r.Status, r.ExpectedCents, r.Readings)
	}
	// Sunday date night with no overflow: just the flat ×1.5
	if r = PriceLine(line("2026-09-20", "date_night", "18:00", "23:00", 15000), priyaTerms(), loc); r.Status != StatusMatch || r.ExpectedCents != 15000 {
		t.Errorf("sunday date night no overflow: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// Saturday hourly whose date-night alternative is shown: unchanged at $128
	r = PriceLine(line("2026-09-12", "hourly", "19:00", "24:00", 12200), priyaTerms(), loc)
	if r.Status != StatusMatch || len(r.Readings) < 2 || r.Readings[1].ExpectedCents != 12800 {
		t.Errorf("sat 9/12: %s %+v", r.Status, r.Readings)
	}
}

func TestDaylightSavingDaysKeepWallClock(t *testing.T) {
	loc := NY()
	// 2026-11-01 (fall back, a Sunday): 19:00–23:00 is 3h before 10pm + 1h late, all ×1.5
	s, _ := SpanOnDate("2026-11-01", "19:00", "23:00", loc)
	if s.Start.Hour() != 19 || s.End.Hour() != 23 || s.Minutes() != 240 {
		t.Errorf("fall-back span: %v–%v (%d min)", s.Start, s.End, s.Minutes())
	}
	r := PriceLine(line("2026-11-01", "hourly", "19:00", "23:00", 14100), priyaTerms(), loc)
	got := readingSet(r.Readings)
	if r.Status != StatusAmbiguous || !got[9900+4200] || !got[9900+3300] {
		t.Errorf("fall-back sunday: %s %d %+v", r.Status, r.ExpectedCents, r.Readings)
	}
	// 2026-03-08 (spring forward, a Sunday): 20:00–22:00 never touches the late rate
	r = PriceLine(line("2026-03-08", "hourly", "20:00", "22:00", 6600), priyaTerms(), loc)
	if r.Status != StatusMatch || r.ExpectedCents != 6600 {
		t.Errorf("spring-forward sunday: %s %d %+v", r.Status, r.ExpectedCents, r.Readings)
	}
	for _, b := range r.Basis {
		if b == "p2" {
			t.Errorf("late rate used on 20:00–22:00: %v", r.Basis)
		}
	}
	// a window split on the fall-back day keeps its clock edges
	in, out := SplitSpanByWindow(s, Window{From: "22:00"})
	if sum(in) != 60 || sum(out) != 180 || in[0].Start.Hour() != 22 {
		t.Errorf("fall-back split: in=%d out=%d %v", sum(in), sum(out), in)
	}
}

func TestHoursOnlyLinesApplyDateRules(t *testing.T) {
	loc := NY()
	// Sunday "4 hours" billed $132 = 4 × $22 × 1.5: not "$44 over"
	r := PriceLine(Line{Date: "2026-09-20", Service: "hourly", Qty: 4, ClaimedCents: 13200, Note: "Sun 9/20 4 hours"}, priyaTerms(), loc)
	if r.Status != StatusMatch || r.ExpectedCents != 13200 || len(r.Info) == 0 || !strings.Contains(r.Info[0], "after 10pm") {
		t.Errorf("sunday 4 hours: %s %d (%s) info=%v", r.Status, r.ExpectedCents, r.Explanation, r.Info)
	}
	// billed more than the hours cost without the late rate: Jordan decides, both readings shown, never a confident Over
	r = PriceLine(Line{Date: "2026-09-09", Service: "hourly", Qty: 4, ClaimedCents: 10000}, priyaTerms(), loc)
	got := readingSet(r.Readings)
	if r.Status != StatusAmbiguous || !got[8800] || !got[11200] {
		t.Errorf("4 hours billed $100: %s %+v (%s)", r.Status, r.Readings, r.Explanation)
	}
	// no clock-windowed rules: minimum, rounding and holiday double all apply, confidently
	m := marcoTerms()
	m.Rules = append(m.Rules, Rule{ID: "rd", Kind: KindRounding, Minutes: 30})
	if r = PriceLine(Line{Date: "2026-09-09", Service: "hourly", Qty: 1.5, ClaimedCents: 7000}, m, loc); r.Status != StatusMatch || r.ExpectedCents != 7000 || len(r.Info) != 0 {
		t.Errorf("1.5 hours under a 2-hour minimum: %s %d info=%v", r.Status, r.ExpectedCents, r.Info)
	}
	if r = PriceLine(Line{Date: "2026-09-09", Service: "hourly", Qty: 2.25, ClaimedCents: 8750}, m, loc); r.Status != StatusMatch || r.ExpectedCents != 8750 {
		t.Errorf("2.25 hours rounded to 2.5: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	if r = PriceLine(Line{Date: "2026-09-07", Service: "hourly", Qty: 2, ClaimedCents: 14000}, m, loc); r.Status != StatusMatch || r.ExpectedCents != 14000 {
		t.Errorf("Labor Day 2 hours: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	if r = PriceLine(Line{Date: "2026-09-08", Service: "hourly", Qty: 2, ClaimedCents: 14000}, m, loc); r.Status != StatusOver || r.ExpectedCents != 7000 {
		t.Errorf("ordinary day billed double: %s %d", r.Status, r.ExpectedCents)
	}
}

func TestUndatedAggregateHourly(t *testing.T) {
	loc := NY()
	// "12 hours @ $22 = $264" with no date: priced at the base rate, but Sundays and after 10pm cannot be checked → Jordan decides
	r := PriceLine(Line{Service: "hourly", Qty: 12, ClaimedCents: 26400, Note: "12 hours @ $22"}, priyaTerms(), loc)
	if r.Status != StatusAmbiguous || r.ExpectedCents != 26400 || !strings.Contains(r.Explanation, "No date or times") {
		t.Errorf("undated 12 hours: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// plain hourly terms: nothing to check, so it matches
	plain := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2200}}}
	if r = PriceLine(Line{Service: "hourly", Qty: 12, ClaimedCents: 26400}, plain, loc); r.Status != StatusMatch || r.ExpectedCents != 26400 {
		t.Errorf("undated plain: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// through CheckBill, which routes undated lines over the period's snapshots
	cal, _ := Calendar("2026-09", []Day{"wed"}, loc)
	b := Bill{HelperID: "x", Period: "2026-09", ClaimedTotalCents: 26400, Lines: []Line{{Service: "hourly", Qty: 12, ClaimedCents: 26400}}}
	res := CheckBill(b, []Terms{plain}, cal, nil, loc)
	if res.Headline != "Pay $264 — matches what you agreed" {
		t.Errorf("undated hourly bill: %q %+v", res.Headline, res.Flags)
	}
	res = CheckBill(b, []Terms{priyaTerms()}, cal, nil, loc)
	if len(res.PendingDecisions) != 1 || res.PendingDecisions[0].Status != StatusAmbiguous {
		t.Errorf("undated hourly bill under priya terms: %+v", res.PendingDecisions)
	}
}

func TestModifierRulesWithAServiceDoNotHijackPricing(t *testing.T) {
	loc := NY()
	// a minimum and a multiplier carrying service "hourly", listed before the rate
	tt := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "min", Kind: KindMinimum, Service: "hourly", Hours: 2},
		{ID: "sun", Kind: KindMultiplier, Service: "hourly", Factor: 1.5, When: &Window{Days: []Day{"sun"}}},
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2000}}}
	if errs := ValidateTerms(tt); len(errs) != 0 {
		t.Fatalf("valid terms rejected: %v", errs)
	}
	if r := PriceLine(line("2026-09-20", "hourly", "10:00", "11:00", 6000), tt, loc); r.Status != StatusMatch || r.ExpectedCents != 6000 {
		t.Errorf("hourly after modifiers: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// two per_unit rules for one service, a flat and a per_unit for one service, two minimums, two roundings: refused
	for name, rules := range map[string][]Rule{
		"two per_unit": {{ID: "a", Kind: KindPerUnit, Service: "walk", AmountCents: 2500}, {ID: "b", Kind: KindPerUnit, Service: "walk", AmountCents: 2800}},
		"flat+hourly":  {{ID: "a", Kind: KindHourly, Service: "sit", RateCents: 2000}, {ID: "b", Kind: KindFlat, Service: "sit", AmountCents: 10000}},
		"two minimums": {{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2000}, {ID: "a", Kind: KindMinimum, Hours: 2}, {ID: "b", Kind: KindMinimum, Hours: 3}},
		"two rounding": {{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2000}, {ID: "a", Kind: KindRounding, Minutes: 15}, {ID: "b", Kind: KindRounding, Minutes: 30}},
	} {
		bad := Terms{EffectiveFrom: "2026-01-01", Rules: rules}
		if errs := ValidateTerms(bad); len(errs) == 0 {
			t.Errorf("%s should be refused", name)
		}
		if r := PriceLine(Line{Date: "2026-09-09", Service: rules[0].Service, Qty: 1, ClaimedCents: 2500}, bad, loc); r.Status != StatusUnpriceable {
			t.Errorf("%s priced: %s", name, r.Status)
		}
	}
	// Priya: a base and an evening hourly rate on "hourly", and a separate date_night flat, stay valid
	if errs := ValidateTerms(priyaTerms()); len(errs) != 0 {
		t.Errorf("priya rejected: %v", errs)
	}
}

func TestMinimumJudgedOnRoundedBooking(t *testing.T) {
	loc := NY()
	tt := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2000},
		{ID: "r", Kind: KindRounding, Minutes: 60},
		{ID: "m", Kind: KindMinimum, Hours: 2.5}}}
	// 10:00–12:10 rounds to 3h = $60; the 2.5-hour minimum must not scale it down to $50
	r := PriceLine(line("2026-03-03", "hourly", "10:00", "12:10", 6000), tt, loc)
	if r.Status != StatusMatch || r.ExpectedCents != 6000 || strings.Contains(r.Explanation, "minimum") {
		t.Errorf("rounded past the minimum: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// 10:00–11:10 rounds to 2h, under 2.5h: scaled up to $50
	if r = PriceLine(line("2026-03-03", "hourly", "10:00", "11:10", 5000), tt, loc); r.Status != StatusMatch || r.ExpectedCents != 5000 {
		t.Errorf("under the minimum: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// an amount minimum holds on both the stacked and the not-stacked reading
	p := priyaTerms()
	p.Rules = append(p.Rules, Rule{ID: "ma", Kind: KindMinimum, AmountCents: 4000})
	r = PriceLine(line("2026-09-20", "hourly", "22:00", "23:00", 4200), p, loc)
	got := readingSet(r.Readings)
	if r.Status != StatusAmbiguous || !got[4200] || !got[4000] || got[3300] {
		t.Errorf("amount minimum on both readings: %s %+v", r.Status, r.Readings)
	}
}

func TestUntilOnlyFlatDoesNotCoverLaterBooking(t *testing.T) {
	loc := NY()
	// date night "until 11pm" and a booking 23:30–01:00: not a date night
	r := PriceLine(line("2026-09-25", "date_night", "23:30", "01:00", 10000), priyaTerms(), loc)
	if r.Status != StatusUnpriceable || !strings.Contains(r.Explanation, "does not overlap") {
		t.Errorf("after-package booking: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
	// billed hourly, the date night is not offered as a reading
	r = PriceLine(line("2026-09-25", "hourly", "23:30", "01:00", 4200), priyaTerms(), loc)
	if r.Status != StatusMatch || r.ExpectedCents != 4200 || len(r.Readings) != 1 {
		t.Errorf("late hourly: %s %d %+v", r.Status, r.ExpectedCents, r.Readings)
	}
	// overnight 21:00 until 07:00 still crosses midnight
	ov := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{{ID: "ov", Kind: KindFlat, Service: "overnight", AmountCents: 15000, From: "21:00", Until: "07:00"}}}
	if r = PriceLine(line("2026-03-03", "overnight", "21:00", "07:00", 15000), ov, loc); r.Status != StatusMatch {
		t.Errorf("overnight: %s (%s)", r.Status, r.Explanation)
	}
}

func TestAdderWithMissingExtraIsNoted(t *testing.T) {
	loc := NY()
	tt := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2000},
		{ID: "ad", Kind: KindAdder, Label: "+$3/hr per extra child", PerHourCents: 300, Per: "extra_child"}}}
	r := PriceLine(Line{Date: "2026-03-03", Service: "hourly", Start: "10:00", End: "12:00", ClaimedCents: 4600, Extras: map[string]float64{"kids": 2}}, tt, loc)
	if r.ExpectedCents != 4000 || len(r.Info) == 0 || !strings.Contains(r.Info[0], "extra_child") {
		t.Errorf("missing extra: %d %v", r.ExpectedCents, r.Info)
	}
	// no extras at all: nothing to note
	if r = PriceLine(line("2026-03-03", "hourly", "10:00", "12:00", 4000), tt, loc); len(r.Info) != 0 {
		t.Errorf("no extras: %v", r.Info)
	}
	tt.Rules[1].Per = ""
	if errs := ValidateTerms(tt); len(errs) == 0 {
		t.Errorf("an adder with neither per nor when should be refused")
	}
	tt.Rules[1].When = &Window{Days: []Day{"sat"}}
	if errs := ValidateTerms(tt); len(errs) != 0 {
		t.Errorf("a Saturday adder is fine: %v", errs)
	}
}

func TestAlternativeReadingUsesArticle(t *testing.T) {
	// a date-night line whose hourly reading is cheaper reads "as an hourly", not "as a hourly"
	p := priyaTerms()
	p.Rules[3].AmountCents = 15000
	r := PriceLine(line("2026-09-09", "date_night", "18:00", "23:00", 15000), p, NY())
	if r.Status != StatusAmbiguous || !strings.Contains(r.Explanation, "read as an hourly") {
		t.Errorf("article: %s %s", r.Status, r.Explanation)
	}
}

func TestCancellationFeePricedFromBookedTimesOrHours(t *testing.T) {
	terms := Terms{EffectiveFrom: "2026-09-01", Rules: []Rule{
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 3500},
		{ID: "m", Kind: KindMinimum, Hours: 2, Per: "booking"},
		{ID: "c", Kind: KindFee, Service: "cancellation_fee", PercentOfBooking: 50, Trigger: &Trigger{CancelledWithinHours: 24}},
	}}
	six := 6.0
	withTimes := Line{Date: "2026-09-28", Service: "cancellation_fee", Start: "15:00", End: "17:00", ClaimedCents: 3500, Flags: map[string]bool{"cancelled": true}, NoticeHours: &six}
	withHours := Line{Date: "2026-09-28", Service: "cancellation_fee", Qty: 2, ClaimedCents: 3500, Flags: map[string]bool{"cancelled": true}, NoticeHours: &six}
	noLength := Line{Date: "2026-09-28", Service: "cancellation_fee", ClaimedCents: 3500, Flags: map[string]bool{"cancelled": true}, NoticeHours: &six}
	for name, l := range map[string]Line{"times": withTimes, "hours": withHours} {
		r := PriceLine(l, terms, NY())
		if r.Status != StatusMatch || len(r.Readings) == 0 || r.Readings[0].ExpectedCents != 3500 {
			t.Fatalf("%s: want match $35, got %s %+v", name, r.Status, r.Readings)
		}
	}
	if r := PriceLine(noLength, terms, NY()); r.Status != StatusUnpriceable {
		t.Fatalf("no length: want unpriceable, got %s", r.Status)
	}
}

func TestOverlappingHourlyRulesWithSameRateAreNotAConflict(t *testing.T) {
	// Priya's screenshot read as "$28 after 10pm" AND "late rate after 11": same rate, two rules.
	terms := Terms{EffectiveFrom: "2026-06-14", Rules: []Rule{
		{ID: "r1", Kind: KindHourly, Service: "hourly", RateCents: 2200},
		{ID: "r2", Kind: KindHourly, Service: "hourly", RateCents: 2800, When: &Window{From: "22:00"}},
		{ID: "r5", Kind: KindHourly, Service: "hourly", RateCents: 2800, When: &Window{From: "23:00"}},
	}}
	l := Line{Date: "2026-09-12", Service: "hourly", Start: "19:00", End: "24:00", ClaimedCents: 12200}
	r := PriceLine(l, terms, NY())
	if r.Status != StatusMatch || len(r.Readings) == 0 || r.Readings[0].ExpectedCents != 12200 {
		t.Fatalf("want match $122, got %s %+v", r.Status, r.Readings)
	}
	terms.Rules[2].RateCents = 3000 // a genuinely different rate is still a conflict
	if r := PriceLine(l, terms, NY()); r.Status == StatusMatch {
		t.Fatalf("different overlapping rates must not price silently")
	}
}
