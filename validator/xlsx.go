package validator

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"bs-be/models"

	"github.com/xuri/excelize/v2"
)

// ValidateAndParseXLSX validates an XLSX file and returns parsed loans or validation errors.
// Interest rates in XLSX are expected as decimals (0.09 = 9%), NOT percentages.
func ValidateAndParseXLSX(content []byte, maxErrors int) ([]models.Loan, []models.ValidationError) {
	var errors []models.ValidationError
	var loans []models.Loan

	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		errors = append(errors, models.ValidationError{Row: 0, Column: "", Message: "Failed to open XLSX file: " + err.Error()})
		return nil, errors
	}
	defer f.Close()

	// Use first sheet
	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		errors = append(errors, models.ValidationError{Row: 0, Column: "", Message: "No sheets found in XLSX file"})
		return nil, errors
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		errors = append(errors, models.ValidationError{Row: 0, Column: "", Message: "Failed to read sheet: " + err.Error()})
		return nil, errors
	}

	if len(rows) < 2 {
		errors = append(errors, models.ValidationError{Row: 0, Column: "", Message: "XLSX file has no data rows"})
		return nil, errors
	}

	// Normalize headers
	header := rows[0]
	for i := range header {
		header[i] = normalizeHeader(header[i])
	}

	colIndex := make(map[string]int)
	for i, h := range header {
		colIndex[h] = i
	}

	// Check required columns
	for _, reqCol := range RequiredColumns {
		if _, ok := colIndex[reqCol]; !ok {
			errors = append(errors, models.ValidationError{
				Row:    1,
				Column: reqCol,
				Message: fmt.Sprintf("Missing required column: '%s'", reqCol),
			})
		}
	}

	if len(errors) > 0 {
		return nil, errors
	}

	// We also need to read raw cell values for dates, since GetRows returns formatted strings.
	// For more accurate parsing, we'll read individual cells when needed.

	for rowIdx := 1; rowIdx < len(rows); rowIdx++ {
		rowNum := rowIdx + 1 // 1-based, header is row 1
		record := rows[rowIdx]

		// Pad record to match header length
		for len(record) < len(header) {
			record = append(record, "")
		}

		// Try to read date cells directly from Excel for better accuracy
		getField := func(name string) string {
			idx, ok := colIndex[name]
			if !ok || idx >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[idx])
		}

		// For date columns, try to read the raw cell value (Excel serial date)
		getCellDate := func(name string) (time.Time, error) {
			idx, ok := colIndex[name]
			if !ok {
				return time.Time{}, fmt.Errorf("column not found")
			}
			cellName, _ := excelize.CoordinatesToCellName(idx+1, rowIdx+1)
			// Try to get the cell value as-is first
			rawVal, _ := f.GetCellValue(sheetName, cellName)
			rawVal = strings.TrimSpace(rawVal)
			if rawVal == "" {
				return time.Time{}, fmt.Errorf("empty date")
			}
			// Try parsing as a date string
			t, err := parseDate(rawVal)
			if err == nil {
				return t, nil
			}
			// Try as Excel serial date number
			serial, serr := strconv.ParseFloat(rawVal, 64)
			if serr == nil && serial > 1 {
				t, err = excelize.ExcelDateToTime(serial, false)
				if err == nil {
					return t, nil
				}
			}
			return time.Time{}, fmt.Errorf("cannot parse date: %s", rawVal)
		}

		// Parse Reporting Date
		reportingDate, err := getCellDate("Reporting Date")
		if err != nil {
			errors = append(errors, models.ValidationError{
				Row: rowNum, Column: "Reporting Date",
				Message: fmt.Sprintf("Invalid date: %v", err),
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

		// Interest Rate — XLSX stores as decimal, no /100 conversion
		interestRate, err := parseNumber(getField("Interest Rate"))
		if err != nil {
			errors = append(errors, models.ValidationError{
				Row: rowNum, Column: "Interest Rate",
				Message: fmt.Sprintf("'%s' is not a valid number", getField("Interest Rate")),
			})
		}
		// No /100 — XLSX already stores as decimal

		// Start Date (optional)
		var startDate time.Time
		startDateStr := getField("Start Date")
		if startDateStr != "" {
			sd, err := getCellDate("Start Date")
			if err != nil {
				errors = append(errors, models.ValidationError{
					Row: rowNum, Column: "Start Date",
					Message: fmt.Sprintf("Invalid date: %v", err),
				})
			} else {
				startDate = sd
			}
		}

		// End Date (NULLABLE)
		var endDate *time.Time
		endDateStr := getField("End Date")
		if endDateStr != "" && strings.ToUpper(endDateStr) != "NULL" && strings.ToUpper(endDateStr) != "NA" && strings.ToUpper(endDateStr) != "N/A" {
			ed, err := getCellDate("End Date")
			if err != nil {
				errors = append(errors, models.ValidationError{
					Row: rowNum, Column: "End Date",
					Message: fmt.Sprintf("Invalid date: %v", err),
				})
			} else {
				endDate = &ed
			}
		}

		// Installment Frequency (nullable)
		var installmentFreq *int
		instFreqStr := getField("Installment Frequency")
		if instFreqStr != "" && strings.ToUpper(instFreqStr) != "NULL" && strings.ToUpper(instFreqStr) != "NA" {
			fv, ferr := strconv.ParseFloat(instFreqStr, 64)
			if ferr == nil {
				iv := int(fv)
				installmentFreq = &iv
			}
		}

		// Interest Payment Frequency (nullable)
		var interestPaymentFreq *int
		intFreqStr := getField("Interest Payment Frequency")
		if intFreqStr != "" && strings.ToUpper(intFreqStr) != "NULL" && strings.ToUpper(intFreqStr) != "NONE" {
			fv, ferr := strconv.ParseFloat(intFreqStr, 64)
			if ferr == nil {
				iv := int(fv)
				interestPaymentFreq = &iv
			}
		}

		// Method — accepts ID (1=annuity, 2=flat) or string
		methodStr := strings.TrimSpace(getField("Method"))
		if methodStr == "" {
			methodStr = "annuity"
		} else {
			// Handle float from Excel (e.g. "1.0" → "1")
			if fv, err := strconv.ParseFloat(methodStr, 64); err == nil {
				methodStr = strconv.Itoa(int(fv))
			}
			if mapped, ok := methodIDMap[methodStr]; ok {
				methodStr = mapped
			} else {
				methodStr = strings.ToLower(methodStr)
			}
		}
		if methodStr != "annuity" && methodStr != "flat" {
			errors = append(errors, models.ValidationError{
				Row: rowNum, Column: "Method",
				Message: fmt.Sprintf("'%s' is not valid, expected 1 (Annuity), 2 (Flat), 'annuity', or 'flat'", getField("Method")),
			})
		}

		// Day Count
		dayCount := strings.TrimSpace(getField("Day Count"))
		if dayCount == "" {
			dayCount = "30/360"
		} else {
			// Handle float from Excel (e.g. "1.0" → "1")
			if fv, err := strconv.ParseFloat(dayCount, 64); err == nil {
				dayCount = strconv.Itoa(int(fv))
			}
			if mapped, ok := dayCountIDMap[dayCount]; ok {
				dayCount = mapped
			}
		}
		validDayCounts := map[string]bool{"30/360": true, "ACT/365": true, "ACT/360": true}
		if !validDayCounts[dayCount] {
			errors = append(errors, models.ValidationError{
				Row: rowNum, Column: "Day Count",
				Message: fmt.Sprintf("'%s' is not valid", getField("Day Count")),
			})
		}

		// Default Behaviour
		defaultBehaviour := true
		dbStr := strings.ToUpper(getField("Default Behaviour"))
		if dbStr == "FALSE" || dbStr == "0" || dbStr == "NO" {
			defaultBehaviour = false
		}

		// Instrument Type
		instrumentType := getField("Instrument Type")
		// Normalize float from Excel (e.g. "1.0" → "1")
		if fv, err := strconv.ParseFloat(instrumentType, 64); err == nil {
			instrumentType = strconv.Itoa(int(fv))
		}

		// Market Value
		marketValue, _ := parseNumber(getField("Market Value"))

		// Account Number
		accountNumber := getField("Account Number")

		// Asset_Liability (default 1)
		assetLiability := 1
		alStr := getField("Asset_Liability")
		if alStr != "" && strings.ToUpper(alStr) != "NULL" {
			alVal, alErr := strconv.ParseFloat(alStr, 64)
			if alErr == nil {
				assetLiability = int(alVal)
			}
			if assetLiability != 1 && assetLiability != 2 {
				assetLiability = 1
			}
		}

		// Margin
		margin, _ := parseNumber(getField("Margin"))

		// Revolving_flag
		revolvingFlag := getField("Revolving_flag")

		if len(errors) > maxErrors {
			errors = errors[:maxErrors]
			break
		}
		if len(errors) > 0 {
			continue
		}

		loans = append(loans, models.Loan{
			ReportingDate:            reportingDate,
			AccountID:                accountID,
			AccountNumber:            accountNumber,
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
			DefaultBehaviour:         defaultBehaviour,
			InstrumentType:           instrumentType,
			MarketValue:              marketValue,
			AssetLiability:           assetLiability,
			Margin:                   margin,
			RevolvingFlag:            revolvingFlag,
		})
	}

	if len(errors) > 0 {
		return nil, errors
	}

	return loans, nil
}
