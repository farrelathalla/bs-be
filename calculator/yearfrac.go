package calculator

import (
	"math"
	"time"
)

// YearFraction calculates the year fraction between two dates based on convention
func YearFraction(start, end time.Time, convention string) float64 {
	days := end.Sub(start).Hours() / 24

	switch convention {
	case "ACT/365":
		return days / 365.0
	case "ACT/360":
		return days / 360.0
	case "30/360":
		// relativedelta equivalent: count full months
		totalMonths := (end.Year()-start.Year())*12 + int(end.Month()-start.Month())
		return float64(totalMonths) / 12.0
	default:
		// Default to 30/360
		totalMonths := (end.Year()-start.Year())*12 + int(end.Month()-start.Month())
		return float64(totalMonths) / 12.0
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
