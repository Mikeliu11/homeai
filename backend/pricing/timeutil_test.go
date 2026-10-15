package pricing

import (
	"testing"
	"time"
)

func TestSpanOnDate(t *testing.T) {
	loc := NY()
	s, err := SpanOnDate("2026-09-04", "18:00", "23:30", loc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Minutes() != 330 {
		t.Errorf("minutes=%d", s.Minutes())
	}
	// end "24:00" and "12am" style end-of-day
	s, err = SpanOnDate("2026-09-12", "19:00", "24:00", loc)
	if err != nil || s.Minutes() != 300 {
		t.Errorf("24:00: err=%v minutes=%d", err, s.Minutes())
	}
	// end earlier than start means next day
	s, err = SpanOnDate("2026-09-12", "23:00", "01:00", loc)
	if err != nil || s.Minutes() != 120 {
		t.Errorf("overnight: err=%v minutes=%d", err, s.Minutes())
	}
	if s.End.Weekday() != time.Sunday {
		t.Errorf("end should be Sunday, got %v", s.End.Weekday())
	}
	if _, err := SpanOnDate("2026-09-04", "9pm", "11pm", loc); err == nil {
		t.Errorf("expected error on non HH:MM clock")
	}
}

func TestSplitAtMidnight(t *testing.T) {
	loc := NY()
	s, _ := SpanOnDate("2026-09-12", "23:00", "01:00", loc)
	parts := s.SplitAtMidnight()
	if len(parts) != 2 || parts[0].Minutes() != 60 || parts[1].Minutes() != 60 {
		t.Fatalf("parts=%v", parts)
	}
	s, _ = SpanOnDate("2026-09-12", "19:00", "24:00", loc)
	if parts := s.SplitAtMidnight(); len(parts) != 1 {
		t.Errorf("span ending exactly at midnight should not split: %v", parts)
	}
}
