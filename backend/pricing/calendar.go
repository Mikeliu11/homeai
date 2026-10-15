package pricing

import (
	"fmt"
	"time"
)

// CalendarFacts describes a billing period for sanity checks ("20 of 21 usual days").
type CalendarFacts struct {
	Period        string            `json:"period"`
	Start         string            `json:"start"`
	End           string            `json:"end"`
	Weekdays      int               `json:"weekdays"`
	Holidays      map[string]string `json:"holidays"`
	UsualDays     int               `json:"usualDays"` // helper's usual days in the period, minus federal holidays
	UsualDayNames []Day             `json:"usualDayNames"`
}

// Calendar computes facts for a YYYY-MM period.
func Calendar(period string, usual []Day, loc *time.Location) (CalendarFacts, error) {
	start, err := time.ParseInLocation("2006-01", period, loc)
	if err != nil {
		return CalendarFacts{}, fmt.Errorf("bad period %q (want YYYY-MM)", period)
	}
	end := start.AddDate(0, 1, 0)
	hol := USFederalHolidays(start.Year())
	f := CalendarFacts{Period: period, Start: start.Format("2006-01-02"), End: end.AddDate(0, 0, -1).Format("2006-01-02"),
		Holidays: map[string]string{}, UsualDayNames: usual}
	usualSet := map[Day]bool{}
	for _, d := range usual {
		usualSet[d] = true
	}
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		wd := d.Weekday()
		isHol := false
		if name, ok := hol[ds]; ok {
			f.Holidays[ds] = name
			isHol = true
		}
		if wd != time.Saturday && wd != time.Sunday {
			f.Weekdays++
		}
		if usualSet[dayOf[wd]] && !isHol {
			f.UsualDays++
		}
	}
	return f, nil
}

// USFederalHolidays returns observed federal holidays for a year, keyed by date.
func USFederalHolidays(year int) map[string]string {
	out := map[string]string{}
	fixed := func(m time.Month, d int, name string) {
		t := time.Date(year, m, d, 0, 0, 0, 0, time.UTC)
		out[t.Format("2006-01-02")] = name // the day itself always counts
		switch t.Weekday() {
		case time.Saturday:
			out[t.AddDate(0, 0, -1).Format("2006-01-02")] = name + " (observed)"
		case time.Sunday:
			out[t.AddDate(0, 0, 1).Format("2006-01-02")] = name + " (observed)"
		}
	}
	nth := func(m time.Month, wd time.Weekday, n int, name string) {
		t := time.Date(year, m, 1, 0, 0, 0, 0, time.UTC)
		for t.Weekday() != wd {
			t = t.AddDate(0, 0, 1)
		}
		t = t.AddDate(0, 0, 7*(n-1))
		out[t.Format("2006-01-02")] = name
	}
	last := func(m time.Month, wd time.Weekday, name string) {
		t := time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC)
		for t.Weekday() != wd {
			t = t.AddDate(0, 0, -1)
		}
		out[t.Format("2006-01-02")] = name
	}
	fixed(time.January, 1, "New Year's Day")
	nth(time.January, time.Monday, 3, "Martin Luther King Jr. Day")
	nth(time.February, time.Monday, 3, "Presidents' Day")
	last(time.May, time.Monday, "Memorial Day")
	fixed(time.June, 19, "Juneteenth")
	fixed(time.July, 4, "Independence Day")
	nth(time.September, time.Monday, 1, "Labor Day")
	nth(time.October, time.Monday, 2, "Columbus Day")
	fixed(time.November, 11, "Veterans Day")
	nth(time.November, time.Thursday, 4, "Thanksgiving Day")
	fixed(time.December, 25, "Christmas Day")
	return out
}
