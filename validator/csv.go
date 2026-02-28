package validator

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"bs-be/models"
)

// RequiredColumns defines the minimum required CSV columns
var RequiredColumns = []string{
	"Reporting Date",
	"Account ID",
	"CCY",
	"Outstanding",
	"Interest Rate",
	"Start Date",
	"End Date",
	"Installment Frequency",
	"ProductType",
	"Segment",
	"Daerah",
	"KodePos",
	"Insured/Uninsured",
	"Transactional/Non Transactional",
	"Method",
}

// OptionalColumns have defaults when missing
var OptionalColumns = []string{
	"Interest Payment Frequency",
	"Day Count",
}

// columnAliases maps alternative header names to canonical names
var columnAliases = map[string]string{
	"accountid":            "Account ID",
	"account_id":           "Account ID",
	"account id":           "Account ID",
	"installment":          "Installment Frequency",
	"installment frequency":"Installment Frequency",
	"installmentfrequency": "Installment Frequency",
	"product type":         "ProductType",
	"producttype":          "ProductType",
	"product_type":         "ProductType",
	"interest payment frequency": "Interest Payment Frequency",
	"interestpaymentfrequency":   "Interest Payment Frequency",
	"day count":            "Day Count",
	"daycount":             "Day Count",
	"day_count":            "Day Count",
	"reporting date":       "Reporting Date",
	"reportingdate":        "Reporting Date",
	"ccy":                  "CCY",
	"currency":             "CCY",
	"outstanding":          "Outstanding",
	"interest rate":        "Interest Rate",
	"interestrate":         "Interest Rate",
	"start date":           "Start Date",
	"startdate":            "Start Date",
	"end date":             "End Date",
	"enddate":              "End Date",
	"segment":              "Segment",
	"daerah":               "Daerah",
	"kodepos":              "KodePos",
	"kode pos":             "KodePos",
	"kode_pos":             "KodePos",
	"insured/uninsured":    "Insured/Uninsured",
	"transactional/non transactional": "Transactional/Non Transactional",
	"method":               "Method",
}

// normalizeHeader maps a raw header to its canonical name
func normalizeHeader(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if canonical, ok := columnAliases[lower]; ok {
		return canonical
	}
	// Return original trimmed value if no alias found
	return strings.TrimSpace(raw)
}

// ValidateAndParseCSV validates a CSV file and returns parsed loans or validation errors.
// It collects ALL errors (up to maxErrors) instead of failing at the first one.
func ValidateAndParseCSV(reader io.Reader, maxErrors int) ([]models.Loan, []models.ValidationError) {
	var errors []models.ValidationError
	var loans []models.Loan

	// Read all content first to handle BOM and detect delimiter
	content, err := io.ReadAll(reader)
	if err != nil {
		errors = append(errors, models.ValidationError{Row: 0, Column: "", Message: "Failed to read file: " + err.Error()})
		return nil, errors
	}

	// Remove BOM if present
	text := string(content)
	if utf8.RuneCountInString(text) > 0 {
		r, _ := utf8.DecodeRuneInString(text)
		if r == '\uFEFF' {
			text = text[3:]
		}
	}

	// Detect delimiter
	delimiter := detectDelimiter(text)

	csvReader := csv.NewReader(strings.NewReader(text))
	csvReader.Comma = delimiter
	csvReader.LazyQuotes = true
	csvReader.TrimLeadingSpace = true

	// Read header
	header, err := csvReader.Read()
	if err != nil {
		errors = append(errors, models.ValidationError{Row: 0, Column: "", Message: "Failed to read header row: " + err.Error()})
		return nil, errors
	}
	// Trim and normalize headers to canonical names
	for i := range header {
		header[i] = normalizeHeader(header[i])
	}

	// Build column index map using canonical names
	colIndex := make(map[string]int)
	for i, h := range header {
		colIndex[h] = i
	}

	// Check required columns
	for _, reqCol := range RequiredColumns {
		if _, ok := colIndex[reqCol]; !ok {
			errors = append(errors, models.ValidationError{
				Row:     1,
				Column:  reqCol,
				Message: fmt.Sprintf("Missing required column: '%s'", reqCol),
			})
		}
	}
	// Optional columns are NOT checked — they get defaults in parseRow

	if len(errors) > 0 {
		return nil, errors
	}

	// Parse data rows
	rowNum := 1 // header is row 1, data starts at row 2
	for {
		rowNum++
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			errors = append(errors, models.ValidationError{
				Row:     rowNum,
				Column:  "",
				Message: fmt.Sprintf("Failed to parse row: %v", err),
			})
			if len(errors) >= maxErrors {
				break
			}
			continue
		}

		loan, rowErrors := parseRow(record, colIndex, rowNum)
		if len(rowErrors) > 0 {
			errors = append(errors, rowErrors...)
			if len(errors) >= maxErrors {
				errors = errors[:maxErrors]
				break
			}
			continue
		}

		loans = append(loans, *loan)
	}

	if len(errors) > 0 {
		return nil, errors
	}

	return loans, nil
}

func parseRow(record []string, colIndex map[string]int, rowNum int) (*models.Loan, []models.ValidationError) {
	var errors []models.ValidationError
	getField := func(name string) string {
		idx, ok := colIndex[name]
		if !ok || idx >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[idx])
	}

	// Parse Reporting Date
	reportingDate, err := parseDate(getField("Reporting Date"))
	if err != nil {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Reporting Date",
			Message: fmt.Sprintf("Invalid date format '%s', expected DD/MM/YYYY", getField("Reporting Date")),
		})
	}

	// Account ID
	accountID := getField("Account ID")
	if accountID == "" {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Account ID",
			Message: "Account ID cannot be empty",
		})
	}

	// CCY
	ccy := getField("CCY")

	// Outstanding
	outstanding, err := parseNumber(getField("Outstanding"))
	if err != nil {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Outstanding",
			Message: fmt.Sprintf("'%s' is not a valid number", getField("Outstanding")),
		})
	}

	// Interest Rate
	interestRateRaw, err := parseNumber(getField("Interest Rate"))
	if err != nil {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Interest Rate",
			Message: fmt.Sprintf("'%s' is not a valid number", getField("Interest Rate")),
		})
	}
	interestRate := interestRateRaw / 100.0 // Convert percentage to decimal

	// Start Date
	startDate, err := parseDate(getField("Start Date"))
	if err != nil {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Start Date",
			Message: fmt.Sprintf("Invalid date format '%s', expected DD/MM/YYYY", getField("Start Date")),
		})
	}

	// End Date
	endDate, err := parseDate(getField("End Date"))
	if err != nil {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "End Date",
			Message: fmt.Sprintf("Invalid date format '%s', expected DD/MM/YYYY", getField("End Date")),
		})
	}
	// Installment Frequency (nullable)
	// Accepts: numeric (e.g. 1, 3, 12), "Yes"/"No", or empty/NULL
	var installmentFreq *int
	instFreqStr := getField("Installment Frequency")
	upperInstFreq := strings.ToUpper(instFreqStr)
	if upperInstFreq == "YES" {
		// "Yes" means installment exists; default to monthly (1)
		v := 1
		installmentFreq = &v
	} else if upperInstFreq == "NO" || upperInstFreq == "NULL" || instFreqStr == "" {
		// No installment
		installmentFreq = nil
	} else {
		v, err := strconv.Atoi(instFreqStr)
		if err != nil {
			// Try parsing as float first
			fv, ferr := strconv.ParseFloat(instFreqStr, 64)
			if ferr != nil || math.IsNaN(fv) {
				errors = append(errors, models.ValidationError{
					Row: rowNum, Column: "Installment Frequency",
					Message: fmt.Sprintf("'%s' is not a valid value (expected number, 'Yes', or 'No')", instFreqStr),
				})
			} else {
				iv := int(fv)
				installmentFreq = &iv
			}
		} else {
			installmentFreq = &v
		}
	}

	// Interest Payment Frequency (nullable)
	var interestPaymentFreq *int
	intFreqStr := getField("Interest Payment Frequency")
	if intFreqStr != "" && strings.ToUpper(intFreqStr) != "NULL" {
		v, err := strconv.Atoi(intFreqStr)
		if err != nil {
			fv, ferr := strconv.ParseFloat(intFreqStr, 64)
			if ferr != nil || math.IsNaN(fv) {
				errors = append(errors, models.ValidationError{
					Row: rowNum, Column: "Interest Payment Frequency",
					Message: fmt.Sprintf("'%s' is not a valid integer", intFreqStr),
				})
			} else {
				iv := int(fv)
				interestPaymentFreq = &iv
			}
		} else {
			interestPaymentFreq = &v
		}
	}

	// Method
	methodStr := strings.ToLower(getField("Method"))
	if methodStr == "" {
		methodStr = "annuity"
	}
	if methodStr != "annuity" && methodStr != "flat" {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Method",
			Message: fmt.Sprintf("'%s' is not valid, expected 'annuity' or 'flat'", getField("Method")),
		})
	}

	// Day Count
	dayCount := getField("Day Count")
	if dayCount == "" {
		dayCount = "30/360"
	}
	validDayCounts := map[string]bool{"30/360": true, "ACT/365": true, "ACT/360": true}
	if !validDayCounts[dayCount] {
		errors = append(errors, models.ValidationError{
			Row: rowNum, Column: "Day Count",
			Message: fmt.Sprintf("'%s' is not valid, expected '30/360', 'ACT/365', or 'ACT/360'", dayCount),
		})
	}

	if len(errors) > 0 {
		return nil, errors
	}

	return &models.Loan{
		ReportingDate:            reportingDate,
		AccountID:                accountID,
		CCY:                      ccy,
		Outstanding:              outstanding,
		InterestRate:             interestRate,
		StartDate:                startDate,
		EndDate:                  endDate,
		InstallmentFrequency:     installmentFreq,
		ProductType:              getField("ProductType"),
		Segment:                  getField("Segment"),
		Daerah:                   getField("Daerah"),
		KodePos:                  getField("KodePos"),
		InsuredOrUninsured:       getField("Insured/Uninsured"),
		TransactionalOrNon:       getField("Transactional/Non Transactional"),
		Method:                   methodStr,
		InterestPaymentFrequency: interestPaymentFreq,
		DayCount:                 dayCount,
	}, nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}

	// Try DD/MM/YYYY
	formats := []string{
		"02/01/2006",
		"2/1/2006",
		"02-01-2006",
		"2-1-2006",
		"2006-01-02",
	}

	for _, fmt := range formats {
		t, err := time.Parse(fmt, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse date: %s", s)
}

func parseNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\"", "")

	if s == "" {
		return 0, fmt.Errorf("empty number")
	}

	// Handle Indonesian format: 1.000.000,50 → 1000000.50
	if matched := strings.Count(s, ".") > 1 || (strings.Contains(s, ".") && strings.Contains(s, ",")); matched {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	} else if strings.Contains(s, ",") && !strings.Contains(s, ".") {
		// Single comma as decimal: 1000,50 → 1000.50
		s = strings.ReplaceAll(s, ",", ".")
	}

	return strconv.ParseFloat(s, 64)
}

func detectDelimiter(text string) rune {
	firstLine := text
	if idx := strings.Index(text, "\n"); idx >= 0 {
		firstLine = text[:idx]
	}

	tabCount := 0
	semiCount := 0
	commaCount := 0
	inQuotes := false

	for _, ch := range firstLine {
		if ch == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes {
			switch ch {
			case '\t':
				tabCount++
			case ';':
				semiCount++
			case ',':
				commaCount++
			}
		}
	}
	// Need at least 14 delimiters for 15+ columns
	if tabCount >= 14 {
		return '\t'
	}
	if semiCount >= 14 {
		return ';'
	}
	if commaCount >= 14 {
		return ','
	}

	if tabCount >= semiCount && tabCount >= commaCount {
		return '\t'
	}
	if semiCount >= commaCount {
		return ';'
	}
	return ','
}
