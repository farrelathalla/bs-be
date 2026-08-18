package calculator

import (
	"math"
	"time"
)

// addMonthsClamped adds months to t, clamping the day to the last day of the
// target month rather than overflowing into the next one. This is what
// relativedelta does: 2025-01-31 plus one month is 2025-02-28, not 2025-03-03
// (which is what time.AddDate would give).
func addMonthsClamped(t time.Time, months int) time.Time {
	total := int(t.Month()) - 1 + months
	year := t.Year() + int(math.Floor(float64(total)/12.0))
	month := time.Month(((total%12)+12)%12 + 1)

	day := t.Day()
	if last := daysInMonth(year, int(month)); day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

// fullMonthsBetween counts whole months from start to end, matching
// dateutil.relativedelta as used by installment_software/calculator.py.
//
// The calendar-month difference alone overstates the period whenever end falls
// earlier in the month than start: 2025-12-31 → 2026-06-01 is five whole months
// plus a day, not six. Testing the day-of-month directly is not enough either,
// because relativedelta clamps at end of month — 2025-01-31 → 2026-02-28 is a
// full 13 months even though 28 < 31. So walk the candidate forward and only
// keep a month that has genuinely elapsed. Truncation is toward zero, which
// also gives the right answer when end precedes start.
func fullMonthsBetween(start, end time.Time) int {
	months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month())
	switch {
	case months > 0 && addMonthsClamped(start, months).After(end):
		months--
	case months < 0 && addMonthsClamped(start, months).Before(end):
		months++
	}
	return months
}

// YearFraction calculates the year fraction between two dates based on convention
func YearFraction(start, end time.Time, convention string) float64 {
	days := end.Sub(start).Hours() / 24

	switch convention {
	case "ACT/365":
		return days / 365.0
	case "ACT/360":
		return days / 360.0
	default:
		// 30/360, and the fallback for an unrecognised convention
		return float64(fullMonthsBetween(start, end)) / 12.0
	}
}

// PMT calculates payment for a loan (equivalent to numpy_financial.pmt)
// Returns the periodic payment amount (positive value for loan payments)
func PMT(rate float64, nper int, pv float64) float64 {
	if rate == 0 {
		return -pv / float64(nper)
	}
	factor := math.Pow(1+rate, float64(nper))
	return -pv * rate * factor / (factor - 1)
}

// Round2 rounds to 2 decimal places
func Round2(v float64) float64 {
	return math.Round(v*100) / 100
}
