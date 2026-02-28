package calculator

import (
	"math"
	"time"
)

// Bucket labels - mirrors Python bucket.py
var IRRBBLabels = []string{
	"≤ 1 bulan",
	"1-3 bulan",
	"3-6 bulan",
	"6-9 bulan",
	"9-12 bulan",
	"1-1.5Y",
	"1.5-2Y",
	"2-3Y",
	"3-4Y",
	"4-5Y",
	"5-6Y",
	"6-7Y",
	"7-8Y",
	"8-9Y",
	"9-10Y",
	"10-15Y",
	"15-20Y",
	"> 20Y",
}

var IRRBBMonthEdges = []float64{
	1, 3, 6, 9, 12,
	18, 24, 36, 48, 60,
	72, 84, 96, 108, 120,
	180, 240, math.Inf(1),
}

var LCRLabels = []string{"≤30D", ">30D"}
var NSFRLabels = []string{"<6M", "6-12M", ">12M"}

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

func getIRRBBBucket(days, months int) string {
	if days <= 30 {
		return "≤ 1 bulan"
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

// ComputeAllBuckets computes all 6 bucket maps from a schedule.
// Exact port of calculator.py get_bucket_all.
// Special rules:
//   - LCR interest: only counted for ≤30D bucket
//   - NSFR interest: always 0
func ComputeAllBuckets(schedule []ScheduleRow, reportingDate time.Time) (
	irrbbPrincipal, irrbbInterest,
	lcrPrincipal, lcrInterest,
	nsfrPrincipal, nsfrInterest map[string]float64,
) {
	irrbbPrincipal = EmptyBucketMap(IRRBBLabels)
	irrbbInterest = EmptyBucketMap(IRRBBLabels)
	lcrPrincipal = EmptyBucketMap(LCRLabels)
	lcrInterest = EmptyBucketMap(LCRLabels)
	nsfrPrincipal = EmptyBucketMap(NSFRLabels)
	nsfrInterest = EmptyBucketMap(NSFRLabels)

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
			lcrPrincipal["≤30D"] += row.Principal
			lcrInterest["≤30D"] += row.Interest
		} else {
			lcrPrincipal[">30D"] += row.Principal
		}

		// NSFR (interest always 0)
		if months < 6 {
			nsfrPrincipal["<6M"] += row.Principal
		} else if months <= 12 {
			nsfrPrincipal["6-12M"] += row.Principal
		} else {
			nsfrPrincipal[">12M"] += row.Principal
		}
	}

	roundMap(irrbbPrincipal)
	roundMap(irrbbInterest)
	roundMap(lcrPrincipal)
	roundMap(lcrInterest)
	roundMap(nsfrPrincipal)
	roundMap(nsfrInterest)

	return
}

func roundMap(m map[string]float64) {
	for k, v := range m {
		m[k] = Round2(v)
	}
}
