package models

import "time"

// Loan represents a single loan record from CSV input
type Loan struct {
	ReportingDate           time.Time
	AccountID               string
	CCY                     string
	Outstanding             float64
	InterestRate            float64 // decimal e.g. 0.09 for 9%
	StartDate               time.Time
	EndDate                 time.Time
	InstallmentFrequency    *int    // nil = bullet
	ProductType             string
	Segment                 string
	Daerah                  string
	KodePos                 string
	InsuredOrUninsured      string
	TransactionalOrNon      string
	Method                  string  // annuity | flat
	InterestPaymentFrequency *int
	DayCount                string  // 30/360 | ACT/365 | ACT/360
}

// TenorDays returns remaining days from reporting date to end date
func (l *Loan) TenorDays() int {
	return int(l.EndDate.Sub(l.ReportingDate).Hours() / 24)
}

// TenorMonths returns remaining months from reporting date to end date
func (l *Loan) TenorMonths() int {
	return (l.EndDate.Year()-l.ReportingDate.Year())*12 +
		int(l.EndDate.Month()-l.ReportingDate.Month())
}
