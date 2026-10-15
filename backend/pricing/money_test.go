package pricing

import "testing"

func TestParseCents(t *testing.T) {
	cases := []struct {
		in   string
		want Cents
		err  bool
	}{
		{"$22", 2200, false},
		{"22", 2200, false},
		{"$1,234.50", 123450, false},
		{"140.", 14000, false},
		{"$0.05", 5, false},
		{"-$5", -500, false},
		{"22/hr", 0, true},
		{"", 0, true},
		{"$1.234", 0, true},
	}
	for _, c := range cases {
		got, err := ParseCents(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseCents(%q) err=%v want err=%v", c.in, err, c.err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCents(%q)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestCentsString(t *testing.T) {
	cases := map[Cents]string{0: "$0", 5: "$0.05", 2200: "$22", 123450: "$1,234.50", -500: "-$5", 59600: "$596"}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("%d.String()=%q want %q", c, got, want)
		}
	}
}

func TestMulHours(t *testing.T) {
	// 2200 cents/hour × 90 minutes = 3300
	if got := PerHour(2200).ForMinutes(90); got != 3300 {
		t.Errorf("got %d", got)
	}
	// 2800 × 30 min = 1400
	if got := PerHour(2800).ForMinutes(30); got != 1400 {
		t.Errorf("got %d", got)
	}
	// rounding: 2200 × 1 min = 36.67 → 37
	if got := PerHour(2200).ForMinutes(1); got != 37 {
		t.Errorf("got %d", got)
	}
}

func TestMultipliedRateRoundedOnce(t *testing.T) {
	// $22.01 × 1.15 × 8h = $202.492 → $202.49 (rounding the rate first gives $25.31 × 8 = $202.48)
	if got := PerHour(2201).TimesForMinutes(1.15, 480); got != 20249 {
		t.Errorf("one-step: %d", got)
	}
	terms := Terms{EffectiveFrom: "2026-01-01", Rules: []Rule{
		{ID: "h", Kind: KindHourly, Service: "hourly", RateCents: 2201},
		{ID: "s", Kind: KindMultiplier, Label: "Saturdays +15%", Factor: 1.15, When: &Window{Days: []Day{"sat"}}}}}
	r := PriceLine(Line{Date: "2026-09-12", Service: "hourly", Start: "09:00", End: "17:00", ClaimedCents: 20249}, terms, NY())
	if r.Status != StatusMatch || r.ExpectedCents != 20249 {
		t.Errorf("saturday 8h: %s %d (%s)", r.Status, r.ExpectedCents, r.Explanation)
	}
}
