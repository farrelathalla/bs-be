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
// Every coded column is checked against the master data tables.
func ValidateAndParseXLSX(content []byte, maxErrors int) ([]models.Loan, []models.ValidationError) {
	return ValidateAndParseXLSXEx(content, maxErrors, LoadMasterData())
}

// ValidateAndParseXLSXEx lets the caller supply the master data snapshot.
func ValidateAndParseXLSXEx(content []byte, maxErrors int, md *MasterData) ([]models.Loan, []models.ValidationError) {
	if md == nil {
		md = LoadMasterData()
	}
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
	errors = append(errors, checkRequiredHeaders(colIndex)...)

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

		loan, rowErrors := validateRow(getField, getCellDate, md, rowNum, true)
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
