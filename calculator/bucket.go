package calculator

import (
	"fmt"
	"math"
	"time"
)

// Bucket labels - mirrors Python bucket.py exactly
var IRRBBLabels = []string{
	"≤ 1 M",
	"1M ≤ 3M",
	"3M ≤ 6M",
	"6M ≤ 9M",
	"9M ≤ 1Y",
	"1Y ≤ 1.5Y",
	"1.5Y ≤ 2Y",
	"2Y ≤ 3Y",
	"3Y ≤ 4Y",
	"4Y ≤ 5Y",
	"5Y ≤ 6Y",
	"6Y ≤ 7Y",
	"7Y ≤ 8Y",
	"8Y ≤ 9Y",
	"9Y ≤ 10Y",
	"10Y ≤ 15Y",
	"15Y ≤ 20Y",
	"> 20Y",
}

var IRRBBMonthEdges = []float64{
	1, 3, 6, 9, 12,
	18, 24, 36, 48, 60,
	72, 84, 96, 108, 120,
	180, 240, math.Inf(1),
}

var LCRLabels = []string{"CF <= 30D", "CF > 30D"}
var NSFRLabels = []string{"CF < 6M", "CF 6M to 12M", "CF > 12M"}

// ILAAP: 41 granular buckets — mirrors Python bucket.py BucketILAAP.LABELS
var ILAAPLabels = []string{
	"No Maturity",
	"D-1", "D-2", "D-3", "D-4", "D-5", "D-6", "D-7", "D-8", "D-9", "D-10",
	"D-11", "D-12", "D-13", "D-14", "D-15", "D-16", "D-17", "D-18", "D-19", "D-20",
	"D-21", "D-22", "D-23", "D-24", "D-25", "D-26", "D-27", "D-28", "D-29", "D-30",
	"W4 <= W5",
	"W5 <= 2M",
	"2M <= 3M",
	"3M <= 4M",
	"4M <= 5M",
	"5M <= 6M",
	"6M <= 9M",
	"9M <= 12M",
	"12M <= 2Y",
	"2Y <= 5Y",
	">5Y",
}

// EmptyBucketMap creates a map with all bucket labels set to 0
func EmptyBucketMap(labels []string) map[string]float64 {
	m := make(map[string]float64)
	for _, l := range labels {
		m[l] = 0
	}
	return m
}

func daysBetween(a, b time.Time) int {
	return int(b.Sub(a).Hours() / 24)
}

func monthsBetween(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()-a.Month())
}

// ilaapMonthsBetween calculates months between two dates using relativedelta semantics.
// If there are residual days beyond the month boundary, months is incremented by 1.
// This matches Python's BucketILAAP.months property.
func ilaapMonthsBetween(a, b time.Time) int {
	months := (b.Year()-a.Year())*12 + int(b.Month()-a.Month())

	// Check for residual days: if b's day-of-month > a's day-of-month,
	// there are extra days beyond the month boundary → push forward
	// This mirrors: r = relativedelta(b, a); months = r.years*12 + r.months; if r.days > 0: months += 1
	dayA := a.Day()
	dayB := b.Day()
	if dayB > dayA {
		// residual days > 0
		months++
	} else if dayB < dayA {
		// b hasn't reached the anchor day yet — relativedelta would give one fewer month
		// but since we computed with month arithmetic, we need to NOT add
		// Actually relativedelta handles this: months is correct, days would be negative
		// meaning no residual days > 0, so no increment needed
	}
	// if dayB == dayA: exactly on boundary, no residual days
	return months
}

// getILAAPBucket classifies a payment into ILAAP time buckets.
// Mirrors Python bucket.py BucketILAAP.get_bucket()
func getILAAPBucket(reportingDate, paymentDate time.Time) string {
	days := daysBetween(reportingDate, paymentDate)
	months := ilaapMonthsBetween(reportingDate, paymentDate)

	// Daily buckets
	if days <= 30 {
		d := days
		if d < 1 {
			d = 1
		}
		return fmt.Sprintf("D-%d", d)
	}

	// Weekly
	if days >= 31 && days <= 35 {
		return "W4 <= W5"
	}

	if days >= 36 && months <= 2 {
		return "W5 <= 2M"
	}

	// Monthly/yearly
	if months <= 3 {
		return "2M <= 3M"
	}
	if months <= 4 {
		return "3M <= 4M"
	}
	if months <= 5 {
		return "4M <= 5M"
	}
	if months <= 6 {
		return "5M <= 6M"
	}
	if months <= 9 {
		return "6M <= 9M"
	}
	if months <= 12 {
		return "9M <= 12M"
	}
	if months <= 24 {
		return "12M <= 2Y"
	}
	if months <= 60 {
		return "2Y <= 5Y"
	}
	return ">5Y"
}

func getIRRBBBucket(days, months int) string {
	if days <= 30 {
		return "≤ 1 M"
	}

	m := float64(months)
	for i := 0; i < len(IRRBBMonthEdges)-1; i++ {
		if m > IRRBBMonthEdges[i] && m <= IRRBBMonthEdges[i+1] {
			return IRRBBLabels[i+1]
		}
	}

	if m <= IRRBBMonthEdges[0] {
		return IRRBBLabels[1]
	}

	return IRRBBLabels[len(IRRBBLabels)-1]
}

// ComputeAllBuckets computes all 8 bucket maps from a schedule.
// Exact port of calculator.py get_bucket_all.
// Special rules:
//   - LCR interest: only counted for <=30D bucket
//   - NSFR interest: always 0
//   - ILAAP interest: accrues per bucket, the same way IRRBB does
func ComputeAllBuckets(schedule []ScheduleRow, reportingDate time.Time) (
	irrbbPrincipal, irrbbInterest,
	lcrPrincipal, lcrInterest,
	nsfrPrincipal, nsfrInterest,
	ilaapPrincipal, ilaapInterest map[string]float64,
) {
	irrbbPrincipal = EmptyBucketMap(IRRBBLabels)
	irrbbInterest = EmptyBucketMap(IRRBBLabels)
	lcrPrincipal = EmptyBucketMap(LCRLabels)
	lcrInterest = EmptyBucketMap(LCRLabels)
	nsfrPrincipal = EmptyBucketMap(NSFRLabels)
	nsfrInterest = EmptyBucketMap(NSFRLabels)
	ilaapPrincipal = EmptyBucketMap(ILAAPLabels)
	ilaapInterest = EmptyBucketMap(ILAAPLabels)

	if len(schedule) == 0 {
		return
	}

	for _, row := range schedule {
		days := daysBetween(reportingDate, row.PaymentDate)
		months := monthsBetween(reportingDate, row.PaymentDate)

		// IRRBB
		irrbbBucket := getIRRBBBucket(days, months)
		irrbbPrincipal[irrbbBucket] += row.Principal
		irrbbInterest[irrbbBucket] += row.Interest

		// LCR
		if days <= 30 {
			lcrPrincipal["CF <= 30D"] += row.Principal
			lcrInterest["CF <= 30D"] += row.Interest
		} else {
			lcrPrincipal["CF > 30D"] += row.Principal
		}

		// NSFR (interest always 0)
		if months < 6 {
			nsfrPrincipal["CF < 6M"] += row.Principal
		} else if months <= 12 {
			nsfrPrincipal["CF 6M to 12M"] += row.Principal
		} else {
			nsfrPrincipal["CF > 12M"] += row.Principal
		}

		// ILAAP — interest accrues per bucket, matching calculator.py _flatten,
		// which treats ILAAP the same as IRRBB
		ilaapBucket := getILAAPBucket(reportingDate, row.PaymentDate)
		ilaapPrincipal[ilaapBucket] += row.Principal
		ilaapInterest[ilaapBucket] += row.Interest
	}

	roundMap(irrbbPrincipal)
	roundMap(irrbbInterest)
	roundMap(lcrPrincipal)
	roundMap(lcrInterest)
	roundMap(nsfrPrincipal)
	roundMap(nsfrInterest)
	roundMap(ilaapPrincipal)
	roundMap(ilaapInterest)

	return
}

func roundMap(m map[string]float64) {
	for k, v := range m {
		m[k] = Round2(v)
	}
}
