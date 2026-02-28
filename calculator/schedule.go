package calculator

import (
	"sort"
	"time"

	"bs-be/models"
)

// ScheduleRow represents one row of the amortization schedule
type ScheduleRow struct {
	Period           int       `json:"period"`
	PaymentDate      time.Time `json:"payment_date"`
	Payment          float64   `json:"payment"`
	Principal        float64   `json:"principal"`
	Interest         float64   `json:"interest"`
	RemainingBalance float64   `json:"remaining_balance"`
}

// daysInMonth returns the number of days in the given month
func daysInMonth(year int, month int) int {
	// month is 1-based
	return time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Day()
}

// generateDatesBackward generates payment dates backward from end_date,
// stepping by `frequency` months. Mirrors Python generate_dates_backward.
func generateDatesBackward(reportingDate, endDate time.Time, frequency *int) []time.Time {
	if frequency == nil {
		return []time.Time{endDate}
	}

	freq := *frequency
	dates := []time.Time{}
	current := endDate

	for current.After(reportingDate) {
		dates = append(dates, current)
		current = current.AddDate(0, -freq, 0)
	}

	// Sort ascending
	sort.Slice(dates, func(i, j int) bool {
		return dates[i].Before(dates[j])
	})

	return dates
}

// uniqueSortedDates merges two date slices, removes duplicates, and sorts ascending
func uniqueSortedDates(a, b []time.Time) []time.Time {
	set := make(map[int64]time.Time)
	for _, d := range a {
		set[d.Unix()] = d
	}
	for _, d := range b {
		set[d.Unix()] = d
	}

	result := make([]time.Time, 0, len(set))
	for _, d := range set {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Before(result[j])
	})
	return result
}

// containsDate checks if a date exists in a slice
func containsDate(dates []time.Time, target time.Time) bool {
	for _, d := range dates {
		if d.Equal(target) {
			return true
		}
	}
	return false
}

// GenerateSchedule builds the amortization schedule for a loan.
// This is an exact port of calculator.py Amortization.schedule()
func GenerateSchedule(loan *models.Loan) []ScheduleRow {
	principal := loan.Outstanding
	originalPrincipal := principal
	annualRate := loan.InterestRate
	installmentFreq := loan.InstallmentFrequency
	interestPaymentFreq := loan.InterestPaymentFrequency
	method := loan.Method
	dayCount := loan.DayCount

	reportingDate := loan.ReportingDate
	endDate := loan.EndDate

	if !reportingDate.Before(endDate) {
		return []ScheduleRow{}
	}

	// Generate event dates (end-date anchored)
	principalDates := generateDatesBackward(reportingDate, endDate, installmentFreq)
	interestDates := generateDatesBackward(reportingDate, endDate, interestPaymentFreq)
	eventDates := uniqueSortedDates(principalDates, interestDates)

	if len(eventDates) == 0 {
		return []ScheduleRow{}
	}

	// Initialize
	balance := principal
	prevDateInterest := reportingDate
	prevDatePrincipal := reportingDate

	var rows []ScheduleRow

	// Flat principal calculation
	var equalPrincipal float64
	if installmentFreq != nil && method == "flat" {
		nPrincipal := len(principalDates)
		if nPrincipal > 0 {
			equalPrincipal = principal / float64(nPrincipal)
		}
	}

	// Event-driven loop
	for i, payDate := range eventDates {
		interestPayment := 0.0
		principalPayment := 0.0

		// 1. Interest Accrual
		if containsDate(interestDates, payDate) {
			yfInterest := YearFraction(prevDateInterest, payDate, dayCount)

			var interestAccrued float64
			if method == "flat" {
				interestAccrued = originalPrincipal * annualRate * yfInterest
			} else {
				interestAccrued = balance * annualRate * yfInterest
			}

			interestPayment = interestAccrued
			prevDateInterest = payDate
		}

		// 2. Principal Payment
		if containsDate(principalDates, payDate) {
			if installmentFreq == nil {
				// Bullet
				principalPayment = balance
			} else if method == "flat" {
				// Flat principal
				principalPayment = equalPrincipal
				if principalPayment > balance {
					principalPayment = balance
				}
			} else if method == "annuity" {
				// Annuity
				remainingPeriods := 0
				for _, d := range principalDates {
					if !d.Before(payDate) {
						remainingPeriods++
					}
				}

				yfPrincipal := YearFraction(prevDatePrincipal, payDate, dayCount)
				rEff := annualRate * yfPrincipal

				var pmt float64
				if rEff == 0 {
					pmt = balance / float64(remainingPeriods)
				} else {
					pmt = PMT(rEff, remainingPeriods, -balance)
				}

				// Interest already paid separately
				interestForPeriod := balance * annualRate * yfPrincipal
				principalPayment = pmt - interestForPeriod
				if principalPayment > balance {
					principalPayment = balance
				}
			}

			prevDatePrincipal = payDate
		}

		// 3. Reduce balance
		balance -= principalPayment
		if balance < 0 {
			balance = 0
		}

		rows = append(rows, ScheduleRow{
			Period:           i + 1,
			PaymentDate:      payDate,
			Payment:          Round2(principalPayment + interestPayment),
			Principal:        Round2(principalPayment),
			Interest:         Round2(interestPayment),
			RemainingBalance: Round2(balance),
		})
	}

	return rows
}
