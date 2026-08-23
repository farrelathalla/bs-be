package validator

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"bs-be/models"
)

// fieldGetter returns the trimmed raw cell for a canonical column name.
type fieldGetter func(name string) string

// dateGetter parses a date cell. CSV reads the string; XLSX also understands
// Excel serial numbers, so each parser supplies its own.
type dateGetter func(name string) (time.Time, error)

// legacy*Names are used only when the corresponding master data table is empty
// (never seeded, or wiped by a superadmin) so uploads still resolve.
var legacyMethodNames = map[string]string{"1": "Annuity", "2": "Flat"}
var legacyDayCountNames = map[string]string{"1": "30/360", "2": "ACT/360", "3": "ACT/365"}

// maxInterestRate caps the annual rate at 100%. Rates above that are almost
// always a unit mistake (9 entered in an XLSX that expects 0.09).
const maxInterestRate = 1.0

// validateRow turns one raw record into a Loan, collecting every problem it
// finds instead of stopping at the first. Both the CSV and the XLSX parser go
// through here so the two formats accept exactly the same values.
func validateRow(
	get fieldGetter,
	getDate dateGetter,
	md *MasterData,
	rowNum int,
	isXLSX bool,
) (*models.Loan, []models.ValidationError) {
	var errs []models.ValidationError

	add := func(col, format string, args ...interface{}) {
		errs = append(errs, models.ValidationError{
			Row: rowNum, Column: col, Message: fmt.Sprintf(format, args...),
		})
	}

	// code validates a cell against the master data table declared for the
	// column in InputColumns and returns the canonical ID ("" when blank).
	code := func(col string) string {
		spec, ok := ColumnSpecFor(col)
		if !ok || spec.RefTable == "" {
			return normalizeCode(get(col))
		}
		id, verr := md.checkCode(spec.RefTable, col, get(col), rowNum, spec.Required && !spec.Nullable)
		if verr != nil {
			errs = append(errs, *verr)
			return ""
		}
		return id
	}

	// masterName resolves an ID to its display name, falling back to the
	// built-in mapping when the master table is empty.
	masterName := func(table, id string, legacy map[string]string) string {
		if md.IsEmpty(table) {
			if n, ok := legacy[id]; ok {
				return n
			}
			return id
		}
		return md.Name(table, id)
	}

	// ── Reporting Date ───────────────────────────────────────
	reportingDate, err := getDate("Reporting Date")
	if err != nil {
		add("Reporting Date",
			"'%s' is not a valid date. Use DD/MM/YYYY (e.g. 31/12/2025).",
			get("Reporting Date"))
	}

	// ── Account ID ───────────────────────────────────────────
	accountID := get("Account ID")
	if accountID == "" {
		add("Account ID", "Account ID is required but the cell is empty.")
	}

	// ── CCY ──────────────────────────────────────────────────
	ccy := code("CCY")

	// ── Outstanding ──────────────────────────────────────────
	outstandingRaw := get("Outstanding")
	outstanding, err := parseNumber(outstandingRaw)
	if err != nil {
		add("Outstanding", "'%s' is not a valid number.", outstandingRaw)
	} else if outstanding < 0 {
		add("Outstanding",
			"'%s' is negative. Outstanding must be zero or positive — set Asset_Liability = 2 (Liability) to report a liability instead.",
			outstandingRaw)
	}

	// ── Interest Rate ────────────────────────────────────────
	rateRaw := get("Interest Rate")
	rateValue, err := parseNumber(rateRaw)
	var interestRate float64
	if err != nil {
		add("Interest Rate", "'%s' is not a valid number.", rateRaw)
	} else {
		if isXLSX {
			interestRate = rateValue // XLSX stores a decimal (0.09 = 9%)
		} else {
			interestRate = rateValue / 100.0 // CSV stores a percentage (9 = 9%)
		}
		switch {
		case interestRate < 0:
			add("Interest Rate", "'%s' is negative. The interest rate must be zero or positive.", rateRaw)
		case interestRate > maxInterestRate && isXLSX:
			add("Interest Rate",
				"'%s' works out to %.2f%% per year. Excel files must hold the rate as a decimal — write 0.09 for 9%%.",
				rateRaw, interestRate*100)
		case interestRate > maxInterestRate:
			add("Interest Rate",
				"'%s' works out to %.2f%% per year. CSV files must hold the rate as a percentage — write 9 for 9%%.",
				rateRaw, interestRate*100)
		}
	}

	// ── Margin ───────────────────────────────────────────────
	margin := 0.0
	if raw := get("Margin"); !isBlankCode(raw) {
		m, merr := parseNumber(raw)
		if merr != nil {
			add("Margin", "'%s' is not a valid number.", raw)
		} else {
			margin = m
		}
	}

	// ── Start Date (optional) ────────────────────────────────
	var startDate time.Time
	if raw := get("Start Date"); !isBlankCode(raw) {
		sd, serr := getDate("Start Date")
		if serr != nil {
			add("Start Date", "'%s' is not a valid date. Use DD/MM/YYYY, or leave the cell empty.", raw)
		} else {
			startDate = sd
		}
	}

	// ── End Date (nullable — blank means no maturity) ─────────
	var endDate *time.Time
	if raw := get("End Date"); !isBlankCode(raw) {
		ed, eerr := getDate("End Date")
		if eerr != nil {
			add("End Date", "'%s' is not a valid date. Use DD/MM/YYYY, or leave it empty for a non-maturing account.", raw)
		} else {
			endDate = &ed
		}
	}
	if endDate != nil && !startDate.IsZero() && endDate.Before(startDate) {
		add("End Date", "End Date (%s) is before Start Date (%s).",
			endDate.Format("02/01/2006"), startDate.Format("02/01/2006"))
	}

	// ── Installment Frequency (nullable) ─────────────────────
	installmentFreq := optionalFrequency(
		get("Installment Frequency"), "Installment Frequency", md, rowNum, &errs, true)

	// ── Interest Payment Frequency (nullable) ────────────────
	interestPaymentFreq := optionalFrequency(
		get("Interest Payment Frequency"), "Interest Payment Frequency", md, rowNum, &errs, false)

	// ── Method ───────────────────────────────────────────────
	method := ""
	if methodID := code("Method"); methodID != "" {
		name := strings.ToLower(masterName("methods", methodID, legacyMethodNames))
		if !containsFold(SupportedMethods, name) {
			add("Method",
				"'%s' maps to '%s', which the calculator cannot run. Supported methods: %s.",
				get("Method"), masterName("methods", methodID, legacyMethodNames),
				strings.Join(SupportedMethods, ", "))
		} else {
			method = name
		}
	}

	// ── Day Count ────────────────────────────────────────────
	dayCount := ""
	if dcID := code("Day Count"); dcID != "" {
		name := masterName("day_counts", dcID, legacyDayCountNames)
		if !containsFold(SupportedDayCounts, name) {
			add("Day Count",
				"'%s' maps to '%s', which the calculator does not implement. Supported conventions: %s.",
				get("Day Count"), name, strings.Join(SupportedDayCounts, ", "))
		} else {
			dayCount = canonicalFold(SupportedDayCounts, name)
		}
	}

	// ── Optional coded columns ───────────────────────────────
	productType := code("ProductType")
	segment := code("Segment")
	instrumentType := code("Instrument Type")
	transactional := code("Transactional/Non Transactional")
	insured := code("Insured/Uninsured")
	revolvingFlag := code("Revolving_flag")

	// ── Asset / Liability ────────────────────────────────────
	assetLiability := 1
	if alID := code("Asset_Liability"); alID != "" {
		v, aerr := strconv.Atoi(alID)
		if aerr != nil || (v != 1 && v != 2) {
			add("Asset_Liability",
				"'%s' cannot be used. Only 1 (Asset) and 2 (Liability) change how the cashflows are signed.",
				get("Asset_Liability"))
		} else {
			assetLiability = v
		}
	}

	// ── Market Value ─────────────────────────────────────────
	marketValue := 0.0
	if raw := get("Market Value"); !isBlankCode(raw) {
		mv, mverr := parseNumber(raw)
		if mverr != nil {
			add("Market Value", "'%s' is not a valid number.", raw)
		} else {
			marketValue = mv
		}
	}

	// ── Default Behaviour ────────────────────────────────────
	defaultBehaviour := true
	if raw := strings.ToUpper(get("Default Behaviour")); !isBlankCode(raw) {
		switch raw {
		case "TRUE", "1", "YES", "Y":
			defaultBehaviour = true
		case "FALSE", "0", "NO", "N":
			defaultBehaviour = false
		default:
			add("Default Behaviour", "'%s' is not valid. Use TRUE or FALSE, or leave the cell empty.", get("Default Behaviour"))
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	return &models.Loan{
		ReportingDate:            reportingDate,
		AccountID:                accountID,
		AccountNumber:            get("Account Number"),
		CCY:                      ccy,
		Outstanding:              outstanding,
		InterestRate:             interestRate,
		StartDate:                startDate,
		EndDate:                  endDate,
		InstallmentFrequency:     installmentFreq,
		ProductType:              productType,
		Segment:                  segment,
		Daerah:                   get("Daerah"),
		KodePos:                  get("KodePos"),
		InsuredOrUninsured:       insured,
		TransactionalOrNon:       transactional,
		Method:                   method,
		InterestPaymentFrequency: interestPaymentFreq,
		DayCount:                 dayCount,
		DefaultBehaviour:         defaultBehaviour,
		InstrumentType:           instrumentType,
		MarketValue:              marketValue,
		AssetLiability:           assetLiability,
		Margin:                   margin,
		RevolvingFlag:            revolvingFlag,
	}, nil
}

// optionalFrequency validates a month-frequency cell against the
// installment_frequencies master data. Blank / NULL means "no schedule".
// allowYesNo keeps the legacy "Yes"/"No" spelling working for the installment
// column, where Yes means monthly.
func optionalFrequency(
	raw, column string, md *MasterData, rowNum int,
	errs *[]models.ValidationError, allowYesNo bool,
) *int {
	if isBlankCode(raw) {
		return nil
	}

	value := raw
	if allowYesNo {
		switch strings.ToUpper(strings.TrimSpace(raw)) {
		case "YES", "Y":
			value = "1"
		case "NO", "N":
			return nil
		}
	}

	id, verr := md.checkCode("installment_frequencies", column, value, rowNum, false)
	if verr != nil {
		*errs = append(*errs, *verr)
		return nil
	}
	if id == "" {
		return nil
	}

	months, err := strconv.Atoi(id)
	if err != nil {
		*errs = append(*errs, models.ValidationError{
			Row: rowNum, Column: column,
			Message: fmt.Sprintf(
				"'%s' maps to a frequency code that is not a whole number of months. Fix the code under Admin → Master Data.",
				raw),
		})
		return nil
	}
	if months <= 0 {
		*errs = append(*errs, models.ValidationError{
			Row: rowNum, Column: column,
			Message: fmt.Sprintf("'%s' resolves to %d months. The frequency must be at least 1 month.", raw, months),
		})
		return nil
	}
	return &months
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// canonicalFold returns the entry of list matching v, preserving list's casing.
func canonicalFold(list []string, v string) string {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return s
		}
	}
	return v
}
