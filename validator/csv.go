package validator

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"bs-be/models"
)

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
// Every coded column is checked against the master data tables.
func ValidateAndParseCSV(reader io.Reader, maxErrors int) ([]models.Loan, []models.ValidationError) {
	return validateAndParseCSVInternal(reader, maxErrors, false, LoadMasterData())
}

// ValidateAndParseCSVEx lets the caller supply the master data snapshot and
// control interest rate handling: XLSX stores decimals (0.09), CSV stores
// percentages (9).
func ValidateAndParseCSVEx(reader io.Reader, maxErrors int, isXLSX bool, md *MasterData) ([]models.Loan, []models.ValidationError) {
	if md == nil {
		md = LoadMasterData()
	}
	return validateAndParseCSVInternal(reader, maxErrors, isXLSX, md)
}

func validateAndParseCSVInternal(reader io.Reader, maxErrors int, isXLSX bool, md *MasterData) ([]models.Loan, []models.ValidationError) {
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
	// TrimLeadingSpace also trims tabs, so with a tab delimiter it swallows
	// empty fields ("a\t\tb" would read as two columns, not three) and every
	// row with a blank cell fails the field-count check.
	csvReader.TrimLeadingSpace = delimiter != '\t'

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
	errors = append(errors, checkRequiredHeaders(colIndex)...)
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

		loan, rowErrors := parseRow(record, colIndex, rowNum, isXLSX, md)
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

func parseRow(
	record []string,
	colIndex map[string]int,
	rowNum int,
	isXLSX bool,
	md *MasterData,
) (*models.Loan, []models.ValidationError) {
	get := func(name string) string {
		idx, ok := colIndex[name]
		if !ok || idx >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[idx])
	}

	getDate := func(name string) (time.Time, error) {
		return parseDate(get(name))
	}

	return validateRow(get, getDate, md, rowNum, isXLSX)
}

// checkRequiredHeaders reports every required column missing from the header
// row, naming the spellings that would have been accepted.
func checkRequiredHeaders(colIndex map[string]int) []models.ValidationError {
	var errs []models.ValidationError
	for _, spec := range InputColumns {
		if !spec.Required {
			continue
		}
		if _, ok := colIndex[spec.Name]; ok {
			continue
		}
		msg := fmt.Sprintf("Missing required column '%s'.", spec.Name)
		if len(spec.Aliases) > 0 {
			msg += fmt.Sprintf(" These header spellings are also accepted: %s.",
				strings.Join(spec.Aliases, ", "))
		}
		errs = append(errs, models.ValidationError{Row: 1, Column: spec.Name, Message: msg})
	}
	return errs
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
	// Need at least 5 delimiters for columns
	if tabCount >= 5 {
		return '\t'
	}
	if semiCount >= 5 {
		return ';'
	}
	if commaCount >= 5 {
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
