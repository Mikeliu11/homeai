package pricing

import "testing"

func TestCalendar(t *testing.T) {
	c, err := Calendar("2026-09", []Day{"mon", "tue", "wed", "thu", "fri"}, NY())
	if err != nil {
		t.Fatal(err)
	}
	if c.Weekdays != 22 {
		t.Errorf("weekdays=%d", c.Weekdays)
	}
	if c.Holidays["2026-09-07"] != "Labor Day" {
		t.Errorf("holidays=%v", c.Holidays)
	}
	if c.UsualDays != 21 {
		t.Errorf("usual days (weekdays minus Labor Day)=%d", c.UsualDays)
	}
	c, _ = Calendar("2026-09", []Day{"tue"}, NY())
	if c.UsualDays != 5 {
		t.Errorf("tuesdays in Sep 2026=%d", c.UsualDays)
	}
	if c.Start != "2026-09-01" || c.End != "2026-09-30" {
		t.Errorf("start/end %s %s", c.Start, c.End)
	}
	if _, err := Calendar("2026-9", nil, NY()); err == nil {
		t.Error("bad period should error")
	}
}

func TestUSFederalHolidays(t *testing.T) {
	h := USFederalHolidays(2026)
	want := map[string]string{
		"2026-01-01": "New Year's Day", "2026-01-19": "Martin Luther King Jr. Day", "2026-02-16": "Presidents' Day",
		"2026-05-25": "Memorial Day", "2026-06-19": "Juneteenth", "2026-07-03": "Independence Day (observed)",
		"2026-09-07": "Labor Day", "2026-10-12": "Columbus Day", "2026-11-11": "Veterans Day",
		"2026-11-26": "Thanksgiving Day", "2026-12-25": "Christmas Day",
	}
	for d, name := range want {
		if h[d] != name {
			t.Errorf("%s: got %q want %q", d, h[d], name)
		}
	}
}
