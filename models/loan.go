package models

import "time"

// Loan represents a single loan record from CSV input
type Loan struct {
	ReportingDate            time.Time
	AccountID                string
	CCY                      string
	Outstanding              float64
	InterestRate             float64  // decimal e.g. 0.09 for 9%
	StartDate                time.Time
	EndDate                  *time.Time // nullable — nil means no maturity
	InstallmentFrequency     *int       // nil = bullet
	ProductType              string
	Segment                  string
	Daerah                   string
	KodePos                  string
	InsuredOrUninsured       string
	TransactionalOrNon       string
	Method                   string // annuity | flat
	InterestPaymentFrequency *int
	DayCount                 string // 30/360 | ACT/365 | ACT/360
	DefaultBehaviour         bool   // always apply default behaviour
	InstrumentType           string
	MarketValue              float64
}

// TenorDays returns remaining days from reporting date to end date
// Returns 0 if EndDate is nil
func (l *Loan) TenorDays() int {
	if l.EndDate == nil {
		return 0
	}
	return int(l.EndDate.Sub(l.ReportingDate).Hours() / 24)
}

// TenorMonths returns remaining months from reporting date to end date
// Returns 0 if EndDate is nil
func (l *Loan) TenorMonths() int {
	if l.EndDate == nil {
		return 0
	}
	return (l.EndDate.Year()-l.ReportingDate.Year())*12 +
		int(l.EndDate.Month()-l.ReportingDate.Month())
}

// HasEndDate returns true if the loan has a non-nil end date
func (l *Loan) HasEndDate() bool {
	return l.EndDate != nil
}
