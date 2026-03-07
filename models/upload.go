package models

import "time"

type Upload struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Filename     string    `json:"filename"`
	UploadedAt   time.Time `json:"uploaded_at"`
	TotalRows    int       `json:"total_rows"`
	Status       string    `json:"status"` // processing | completed | failed
	ErrorMessage string    `json:"error_message,omitempty"`
	FilePath     string    `json:"-"`
}

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
}

// CashflowResult holds computed bucket values for a single loan
type CashflowResult struct {
	ID            int64              `json:"id"`
	UploadID      string             `json:"upload_id"`
	LoanInputID   int64              `json:"loan_input_id"`
	RemainingDays int                `json:"remaining_days"`
	IRRBBPrincipal map[string]float64 `json:"irrbb_principal"`
	IRRBBInterest  map[string]float64 `json:"irrbb_interest"`
	LCRPrincipal   map[string]float64 `json:"lcr_principal"`
	LCRInterest    map[string]float64 `json:"lcr_interest"`
	NSFRPrincipal  map[string]float64 `json:"nsfr_principal"`
	NSFRInterest   map[string]float64 `json:"nsfr_interest"`
}

// ResultRow is a combined loan input + cashflow result for API response
type ResultRow struct {
	RowNumber              int                `json:"row_number"`
	ReportingDate          string             `json:"reporting_date"`
	AccountID              string             `json:"account_id"`
	CCY                    string             `json:"ccy"`
	Outstanding            float64            `json:"outstanding"`
	InterestRate           float64            `json:"interest_rate"`
	StartDate              string             `json:"start_date"`
	EndDate                string             `json:"end_date"`
	InstallmentFrequency   *int               `json:"installment_frequency"`
	ProductType            string             `json:"product_type"`
	Segment                string             `json:"segment"`
	Daerah                 string             `json:"daerah"`
	KodePos                string             `json:"kode_pos"`
	InsuredOrUninsured     string             `json:"insured_or_uninsured"`
	TransactionalOrNon     string             `json:"transactional_or_non"`
	Method                 string             `json:"method"`
	InterestPaymentFrequency *int             `json:"interest_payment_frequency"`
	DayCount               string             `json:"day_count"`
	RemainingDays          int                `json:"remaining_days"`
	ResultType             string             `json:"result_type"`
	IRRBBPrincipal         map[string]float64 `json:"irrbb_principal"`
	IRRBBInterest          map[string]float64 `json:"irrbb_interest"`
	LCRPrincipal           map[string]float64 `json:"lcr_principal"`
	LCRInterest            map[string]float64 `json:"lcr_interest"`
	NSFRPrincipal          map[string]float64 `json:"nsfr_principal"`
	NSFRInterest           map[string]float64 `json:"nsfr_interest"`
}

type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	Limit      int         `json:"limit"`
	TotalPages int         `json:"total_pages"`
}

type PivotGroup struct {
	Keys   map[string]string  `json:"keys"`
	Count  int                `json:"count"`
	Aggregates map[string]float64 `json:"aggregates"`
}

type UploadResponse struct {
	ID       string           `json:"id"`
	Filename string           `json:"filename"`
	Status   string           `json:"status"`
	Errors   []ValidationError `json:"errors,omitempty"`
}

type ValidationError struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Message string `json:"message"`
}
