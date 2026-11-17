package workdays

import (
	"sort"
	"testing"
	"time"
)

func TestHolidays2026(t *testing.T) {
	// Official 2026 calendar.
	want := []string{"2026-01-01", "2026-01-12", "2026-03-23", "2026-04-02", "2026-04-03", "2026-05-01", "2026-05-18",
		"2026-06-08", "2026-06-15", "2026-06-29", "2026-07-20", "2026-08-07", "2026-08-17", "2026-10-12", "2026-11-02",
		"2026-11-16", "2026-12-08", "2026-12-25"}
	var got []string
	for d := range Holidays(2026) {
		got = append(got, d)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("got %d holidays: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v\nwant %v", got, want)
		}
	}
}

func TestHolidays2027(t *testing.T) {
	// Easter 2027 is March 28.
	h := Holidays(2027)
	for _, d := range []string{"2027-03-25", "2027-03-26", "2027-05-10", "2027-05-31", "2027-06-07", "2027-01-11", "2027-11-01", "2027-11-15"} {
		if !h[d] {
			t.Errorf("%s should be a holiday", d)
		}
	}
	if len(h) != 18 {
		t.Errorf("got %d holidays", len(h))
	}
}

func TestWorkdaysAndClosing(t *testing.T) {
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, Colombia) }
	cases := []struct {
		t       time.Time
		workday bool
		closing string
	}{
		{at(11, 13, 9), true, "18:30"}, // Friday
		{at(11, 14, 9), true, "17:00"}, // Saturday
		{at(11, 15, 9), false, ""},     // Sunday
		{at(11, 16, 9), false, ""},     // holiday Monday
		{at(11, 17, 9), true, "18:30"}, // Tuesday
		{at(12, 25, 9), false, ""},     // Christmas (Friday)
	}
	for _, c := range cases {
		if IsWorkday(c.t) != c.workday {
			t.Errorf("%s workday=%v", c.t.Format("Mon 02 Jan"), !c.workday)
		}
		got := ""
		if cl := Closing(c.t); !cl.IsZero() {
			got = cl.Format("15:04")
		}
		if got != c.closing {
			t.Errorf("%s closing %q, want %q", c.t.Format("Mon 02 Jan"), got, c.closing)
		}
	}
	// A UTC time late at night is still the previous day in Colombia.
	// 2026-11-17 03:00 UTC is Monday 16 at 22:00 in Colombia: a holiday.
	if IsWorkday(time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)) {
		t.Error("Colombia time must be used")
	}
	if got := NextWorkday(at(11, 14, 18)).Format("2006-01-02"); got != "2026-11-17" {
		t.Errorf("after Saturday 14 comes Tuesday 17 (Sunday + holiday), got %s", got)
	}
}
