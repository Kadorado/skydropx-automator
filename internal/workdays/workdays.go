// Package workdays knows when the carrier's office works: Colombian public holidays and opening hours.
package workdays

import "time"

// Colombia is Colombia's time zone (UTC-5, no daylight saving).
var Colombia = time.FixedZone("COT", -5*3600)

// Office closing times (hour, minute) in Colombia; the office doesn't open on Sundays nor holidays.
var (
	weekdayClose  = [2]int{18, 30} // Monday to Friday
	saturdayClose = [2]int{17, 0}
)

// date is midnight of y-m-d in Colombia.
func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, Colombia) }

// easter returns Easter Sunday of a year (anonymous Gregorian algorithm).
func easter(y int) time.Time {
	a, b, c := y%19, y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return date(y, time.Month(month), day)
}

// nextMonday moves a holiday to the following Monday unless it already is one (Law 51 of 1983).
func nextMonday(t time.Time) time.Time {
	return t.AddDate(0, 0, (8-int(t.Weekday()))%7)
}

// Holidays returns the Colombian public holidays of a year, as "YYYY-MM-DD".
func Holidays(y int) map[string]bool {
	e := easter(y)
	days := []time.Time{
		// Fixed.
		date(y, 1, 1), date(y, 5, 1), date(y, 7, 20), date(y, 8, 7), date(y, 12, 8), date(y, 12, 25),
		// Moved to Monday.
		nextMonday(date(y, 1, 6)), nextMonday(date(y, 3, 19)), nextMonday(date(y, 6, 29)), nextMonday(date(y, 8, 15)),
		nextMonday(date(y, 10, 12)), nextMonday(date(y, 11, 1)), nextMonday(date(y, 11, 11)),
		// Around Easter: Holy Thursday and Good Friday stay; Ascension, Corpus Christi and Sacred Heart
		// fall on Mondays.
		e.AddDate(0, 0, -3), e.AddDate(0, 0, -2), e.AddDate(0, 0, 43), e.AddDate(0, 0, 64), e.AddDate(0, 0, 71),
	}
	out := make(map[string]bool, len(days))
	for _, d := range days {
		out[d.Format("2006-01-02")] = true
	}
	return out
}

// IsHoliday reports whether the day of t (in Colombia) is a public holiday.
func IsHoliday(t time.Time) bool {
	t = t.In(Colombia)
	return Holidays(t.Year())[t.Format("2006-01-02")]
}

// IsWorkday reports whether the office opens on the day of t: Monday to Saturday, except holidays.
func IsWorkday(t time.Time) bool {
	t = t.In(Colombia)
	return t.Weekday() != time.Sunday && !IsHoliday(t)
}

// Closing is when the office closes on the day of t (zero if it doesn't open that day).
func Closing(t time.Time) time.Time {
	t = t.In(Colombia)
	if !IsWorkday(t) {
		return time.Time{}
	}
	hm := weekdayClose
	if t.Weekday() == time.Saturday {
		hm = saturdayClose
	}
	return time.Date(t.Year(), t.Month(), t.Day(), hm[0], hm[1], 0, 0, Colombia)
}

// NextWorkday is the first workday after the day of t.
func NextWorkday(t time.Time) time.Time {
	d := t.In(Colombia)
	d = date(d.Year(), d.Month(), d.Day())
	for {
		d = d.AddDate(0, 0, 1)
		if IsWorkday(d) {
			return d
		}
	}
}
