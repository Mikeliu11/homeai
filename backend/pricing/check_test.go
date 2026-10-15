package pricing

import (
	"strings"
	"testing"
)

func theoVersions() []Terms {
	return []Terms{
		{HelperID: "theo", EffectiveFrom: "", Label: "Theo, from Jordan's note (undated)", Rules: []Rule{
			{ID: "t0", Kind: KindPerUnit, Service: "walk", Label: "walk", Unit: "walk", AmountCents: 2500}}, DefaultPerVisit: "walk"},
		{HelperID: "theo", EffectiveFrom: "2026-09-01", Label: "Theo, terms since 2026-09-01", Rules: []Rule{
			{ID: "t1", Kind: KindPerUnit, Service: "walk_30", Label: "30-min walk", Unit: "walk", AmountCents: 2800},
			{ID: "t2", Kind: KindPerUnit, Service: "walk_60", Label: "60-min walk", Unit: "walk", AmountCents: 3800}}, DefaultPerVisit: "walk_30"},
	}
}

func lupeVersions() []Terms {
	return []Terms{lupeTerms("2026-01-14", 14000), lupeTerms("2026-09-15", 15000)}
}

func TestTermsInForce(t *testing.T) {
	vs := lupeVersions()
	if tf, ok := TermsInForce(vs, "2026-09-08"); !ok || tf.EffectiveFrom != "2026-01-14" {
		t.Errorf("Sep 8 → Jan terms, got %v %v", tf.EffectiveFrom, ok)
	}
	if tf, ok := TermsInForce(vs, "2026-09-15"); !ok || tf.EffectiveFrom != "2026-09-15" {
		t.Errorf("Sep 15 inclusive → Sep terms, got %v %v", tf.EffectiveFrom, ok)
	}
	// reverse order gives the same answer
	rev := []Terms{vs[1], vs[0]}
	if tf, _ := TermsInForce(rev, "2026-09-08"); tf.EffectiveFrom != "2026-01-14" {
		t.Errorf("order dependence: %v", tf.EffectiveFrom)
	}
	if _, ok := TermsInForce(vs, "2026-01-01"); ok {
		t.Errorf("before any terms → none")
	}
	// undated version counts as lowest precedence and is in force when nothing else is
	if tf, ok := TermsInForce(theoVersions(), "2026-08-01"); !ok || tf.EffectiveFrom != "" {
		t.Errorf("undated note should cover August, got %v %v", tf.EffectiveFrom, ok)
	}
	if tf, _ := TermsInForce(theoVersions(), "2026-09-10"); tf.EffectiveFrom != "2026-09-01" {
		t.Errorf("Sept note superseded by dated text, got %v", tf.EffectiveFrom)
	}
}

func starterBills() (priya, lupe, theo Bill) {
	priya = Bill{HelperID: "priya", Period: "2026-09", ClaimedTotalCents: 62600, Lines: []Line{
		line("2026-09-04", "date_night", "18:00", "23:30", 11400),
		line("2026-09-09", "hourly", "15:00", "19:00", 8800),
		line("2026-09-12", "hourly", "19:00", "24:00", 12200),
		line("2026-09-20", "hourly", "10:00", "14:00", 13200),
		{Date: "2026-09-20", Service: "last_minute_fee", ClaimedCents: 4000, Note: "last minute fee $40 (Sam said that was fine since it was same-day!)", Flags: map[string]bool{"same_day": true}},
		line("2026-09-25", "date_night", "18:00", "23:00", 10000),
	}}
	lupe = Bill{HelperID: "lupe", Period: "2026-09", ClaimedTotalCents: 82000, Lines: []Line{
		{Date: "2026-09-01", ClaimedCents: 15000}, {Date: "2026-09-08", ClaimedCents: 15000}, {Date: "2026-09-15", ClaimedCents: 15000},
		{Date: "2026-09-22", Service: "deep_clean", ClaimedCents: 22000, Note: "Sep 22 deep"}, {Date: "2026-09-29", ClaimedCents: 15000},
	}}
	theo = Bill{HelperID: "theo", Period: "2026-09", ClaimedTotalCents: 58000, Lines: []Line{
		{Service: "walk_30", Qty: 18, ClaimedCents: 50400, Note: "30-min walks: 18 x $28 = $504"},
		{Service: "walk_60", Qty: 2, ClaimedCents: 7600, Note: "60-min walks: 2 x $38 = $76"},
	}}
	return
}

func TestCheckStarterPack(t *testing.T) {
	loc := NY()
	priya, lupe, theo := starterBills()

	calP, _ := Calendar("2026-09", []Day{"wed", "sat", "sun"}, loc)
	rp := CheckBill(priya, []Terms{priyaTerms()}, calP, nil, loc)
	if rp.ClaimedTotalCents != 62600 || rp.LineSumCents != 59600 || rp.AgreedTotalCents != 55600 || rp.ArithmeticOK {
		t.Errorf("priya totals: claimed %d sum %d agreed %d arith %v", rp.ClaimedTotalCents, rp.LineSumCents, rp.AgreedTotalCents, rp.ArithmeticOK)
	}
	if len(rp.PendingDecisions) != 1 || rp.PendingDecisions[0].Status != StatusUnagreed || rp.PendingDecisions[0].LineIndex != 4 {
		t.Errorf("priya pending: %+v", rp.PendingDecisions)
	}
	if rp.Headline != "Pay $556, not $626 — 2 problems" {
		t.Errorf("priya headline: %q", rp.Headline)
	}
	if rp.Lines[2].Status != StatusMatch || len(rp.Lines[2].Info) == 0 {
		t.Errorf("sat 9/12 should match with info: %s %v", rp.Lines[2].Status, rp.Lines[2].Info)
	}

	calL, _ := Calendar("2026-09", []Day{"tue"}, loc)
	rl := CheckBill(lupe, lupeVersions(), calL, nil, loc)
	if rl.AgreedTotalCents != 80000 || rl.LineSumCents != 82000 || !rl.ArithmeticOK {
		t.Errorf("lupe totals: agreed %d sum %d arith %v", rl.AgreedTotalCents, rl.LineSumCents, rl.ArithmeticOK)
	}
	if rl.Lines[0].Status != StatusOver || rl.Lines[1].Status != StatusOver || rl.Lines[2].Status != StatusMatch || rl.Lines[3].Status != StatusMatch || rl.Lines[4].Status != StatusMatch {
		for _, l := range rl.Lines {
			t.Logf("%s %s %d %s", l.Line.Date, l.Status, l.ExpectedCents, l.Explanation)
		}
		t.Errorf("lupe line statuses wrong")
	}
	if rl.Headline != "Pay $800, not $820 — 2 problems" {
		t.Errorf("lupe headline: %q", rl.Headline)
	}
	// reverse version order → identical
	rl2 := CheckBill(lupe, []Terms{lupeVersions()[1], lupeVersions()[0]}, calL, nil, loc)
	if rl2.AgreedTotalCents != rl.AgreedTotalCents || rl2.Headline != rl.Headline {
		t.Errorf("order dependence in CheckBill")
	}

	calT, _ := Calendar("2026-09", []Day{"mon", "tue", "wed", "thu", "fri"}, loc)
	rt := CheckBill(theo, theoVersions(), calT, nil, loc)
	if rt.AgreedTotalCents != 58000 || !rt.ArithmeticOK || len(rt.PendingDecisions) != 0 {
		t.Errorf("theo totals: agreed %d arith %v pending %v", rt.AgreedTotalCents, rt.ArithmeticOK, rt.PendingDecisions)
	}
	if rt.Headline != "Pay $580 — matches what you agreed" {
		t.Errorf("theo headline: %q", rt.Headline)
	}
	if !hasFlag(rt.Flags, "count_vs_usual") {
		t.Errorf("theo should note 20 of 21 usual days: %+v", rt.Flags)
	}
}

func TestCheckFlags(t *testing.T) {
	loc := NY()
	cal, _ := Calendar("2026-09", []Day{"tue"}, loc)
	b := Bill{HelperID: "lupe", Period: "2026-09", ClaimedTotalCents: 30000, Lines: []Line{
		{Date: "2026-09-01", ClaimedCents: 15000}, {Date: "2026-09-01", ClaimedCents: 15000}, // duplicate
		{Date: "2026-10-06", ClaimedCents: 15000}, // outside period
		{Date: "2026-09-07", ClaimedCents: 15000}, // Labor Day, a Monday (not usual)
	}}
	r := CheckBill(b, lupeVersions(), cal, map[string]bool{"2026-09-01": true}, loc)
	for _, code := range []string{"duplicate_line", "outside_period", "holiday", "non_usual_day", "duplicate_date_other_bill"} {
		if !hasFlag(r.Flags, code) {
			t.Errorf("missing flag %s in %+v", code, r.Flags)
		}
	}
	if r.ArithmeticOK {
		t.Errorf("30000 claimed vs 60000 lines should fail arithmetic")
	}
}

func TestUndatedLinesAcrossRateChange(t *testing.T) {
	loc := NY()
	cal, _ := Calendar("2026-09", []Day{"tue"}, loc)
	// Lupe billed as an aggregate: "4 visits $600" with a change on the 15th
	b := Bill{HelperID: "lupe", Period: "2026-09", ClaimedTotalCents: 60000, Lines: []Line{{Service: "regular_visit", Qty: 4, ClaimedCents: 60000}}}
	r := CheckBill(b, lupeVersions(), cal, nil, loc)
	if r.Lines[0].Status != StatusAmbiguous || len(r.Lines[0].Readings) != 2 {
		t.Errorf("undated line across a rate change should be ambiguous with two readings: %s %+v", r.Lines[0].Status, r.Lines[0].Readings)
	}
	// No terms in force at all
	r = CheckBill(b, nil, cal, nil, loc)
	if r.Lines[0].Status != StatusUnpriceable {
		t.Errorf("no terms → unpriceable, got %s", r.Lines[0].Status)
	}
}

func hasFlag(fs []Flag, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestTermsInForceTieBreaksByConfirmation(t *testing.T) {
	a := Terms{EffectiveFrom: "2026-09-15", ConfirmedAt: "2026-10-01T10:00:00Z", Label: "first"}
	b := Terms{EffectiveFrom: "2026-09-15", ConfirmedAt: "2026-10-02T10:00:00Z", Label: "correction"}
	for _, vs := range [][]Terms{{a, b}, {b, a}} {
		if tf, _ := TermsInForce(vs, "2026-09-20"); tf.Label != "correction" {
			t.Errorf("latest confirmation should win regardless of order, got %s", tf.Label)
		}
	}
}

func TestHeadlineWithOnlyPendingAndUnderLines(t *testing.T) {
	loc := NY()
	cal, _ := Calendar("2026-09", nil, loc)
	b := Bill{HelperID: "priya", Period: "2026-09", ClaimedTotalCents: 11600, Lines: []Line{line("2026-09-11", "hourly", "18:00", "23:00", 11600)}}
	r := CheckBill(b, []Terms{priyaTerms()}, cal, nil, loc)
	if strings.Contains(r.Headline, "Pay $0") {
		t.Errorf("headline must not say pay $0 when a line needs a decision: %q", r.Headline)
	}
	// under: billed less than agreed → pay as billed, not a problem
	b = Bill{HelperID: "priya", Period: "2026-09", ClaimedTotalCents: 8000, Lines: []Line{line("2026-09-09", "hourly", "15:00", "19:00", 8000)}}
	r = CheckBill(b, []Terms{priyaTerms()}, cal, nil, loc)
	if r.AgreedTotalCents != 8000 || r.Problems != 0 || r.Lines[0].Status != StatusUnder {
		t.Errorf("under line: agreed %d problems %d status %s", r.AgreedTotalCents, r.Problems, r.Lines[0].Status)
	}
}

func TestCountVsUsualUsesDistinctServiceDates(t *testing.T) {
	loc := NY()
	priya, _, _ := starterBills()
	cal, _ := Calendar("2026-09", []Day{"wed"}, loc)
	r := CheckBill(priya, []Terms{priyaTerms()}, cal, nil, loc)
	if r.Problems != 2 {
		t.Errorf("usual days must not add problems for an hourly helper: %d problems, flags %+v", r.Problems, r.Flags)
	}
	for _, f := range r.Flags {
		if f.Code == "count_vs_usual" && (f.Level != "info" || strings.Contains(f.Message, "6 billed")) {
			t.Errorf("count flag: %+v", f)
		}
	}
}
