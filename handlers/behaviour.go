package handlers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"bs-be/config"
	"bs-be/models"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

// UploadBehaviour parses a scenario/behaviour CSV or XLSX and saves it
func UploadBehaviour(c *gin.Context) {
	uploadID := c.Param("id")
	name := c.PostForm("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Scenario name is required"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
		return
	}

	// Detect file type and parse accordingly
	var bucketConfigs []bucketConfigInternal
	var cashflowAssumptions []cashflowAssumptionInternal
	var parseErrors []string

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == ".xlsx" || ext == ".xls" {
		bucketConfigs, cashflowAssumptions, parseErrors = parseScenarioXLSX(content)
	} else {
		bucketConfigs, cashflowAssumptions, parseErrors = parseScenarioCSV(content)
	}
	if len(parseErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File parsing errors", "details": parseErrors})
		return
	}

	// Validate no overlapping rules within either section
	overlapErrors := validateScenarioCSV(bucketConfigs, cashflowAssumptions)
	if len(overlapErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Overlapping rules detected", "details": capDetails(overlapErrors)})
		return
	}

	// Check upload exists
	var exists int
	config.DB.QueryRow("SELECT COUNT(*) FROM uploads WHERE id = $1", uploadID).Scan(&exists)
	if exists == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upload not found"})
		return
	}

	// Insert scenario behaviour
	tx, err := config.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	var behaviourID int64
	err = tx.QueryRow(
		"INSERT INTO behaviours (upload_id, name, is_default, is_scenario) VALUES ($1, $2, FALSE, TRUE) RETURNING id",
		uploadID, name,
	).Scan(&behaviourID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create behaviour: " + err.Error()})
		return
	}

	// Insert bucket configs
	for _, b := range bucketConfigs {
		_, err := tx.Exec(
			`INSERT INTO scenario_bucket_configs (
				behaviour_id, bucket_type, bucket_name, percentage,
				product_type, ccy, segment, transactional, value_type
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			behaviourID, b.BucketType, b.BucketName, b.Percentage,
			b.ProductType, b.CCY, b.Segment, b.Transactional, b.ValueType,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to insert bucket config: %v", err)})
			return
		}
	}

	// Insert cashflow assumptions
	for _, ca := range cashflowAssumptions {
		_, err := tx.Exec(
			`INSERT INTO scenario_cashflow_assumptions (
				behaviour_id, product_type, ccy, segment, transactional, bucket_type, percentage
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			behaviourID, ca.ProductType, ca.CCY, ca.Segment, ca.Transactional, ca.BucketType, ca.Percentage,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to insert cashflow assumption: %v", err)})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":         behaviourID,
		"upload_id":  uploadID,
		"name":       name,
		"is_scenario": true,
		"buckets":    len(bucketConfigs),
		"assumptions": len(cashflowAssumptions),
	})
}

// ListBehaviours returns all behaviours for an upload (including global default)
func ListBehaviours(c *gin.Context) {
	uploadID := c.Param("id")

	rows, err := config.DB.Query(
		`SELECT id, upload_id, name, is_default, created_at, updated_at
		 FROM behaviours
		 WHERE upload_id = $1 OR (is_default = TRUE AND upload_id IS NULL)
		 ORDER BY is_default DESC, created_at ASC`,
		uploadID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query behaviours"})
		return
	}
	defer rows.Close()

	behaviours := make([]models.Behaviour, 0)
	for rows.Next() {
		var b models.Behaviour
		var uid *string
		if err := rows.Scan(&b.ID, &uid, &b.Name, &b.IsDefault, &b.CreatedAt, &b.UpdatedAt); err != nil {
			continue
		}
		b.UploadID = uid
		behaviours = append(behaviours, b)
	}

	c.JSON(http.StatusOK, behaviours)
}

// GetBehaviour returns a single behaviour with its buckets
func GetBehaviour(c *gin.Context) {
	id := c.Param("id")

	var b models.Behaviour
	var uid *string
	err := config.DB.QueryRow(
		"SELECT id, upload_id, name, is_default, created_at, updated_at FROM behaviours WHERE id = $1",
		id,
	).Scan(&b.ID, &uid, &b.Name, &b.IsDefault, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Behaviour not found"})
		return
	}
	b.UploadID = uid

	// Get buckets
	rows, err := config.DB.Query(
		"SELECT id, behaviour_id, bucket_type, bucket_name, percentage FROM behaviour_buckets WHERE behaviour_id = $1 ORDER BY bucket_type, bucket_name",
		id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query buckets"})
		return
	}
	defer rows.Close()

	b.Buckets = make([]models.BehaviourBucket, 0)
	for rows.Next() {
		var bucket models.BehaviourBucket
		if rows.Scan(&bucket.ID, &bucket.BehaviourID, &bucket.BucketType, &bucket.BucketName, &bucket.Percentage) == nil {
			b.Buckets = append(b.Buckets, bucket)
		}
	}

	c.JSON(http.StatusOK, b)
}

// UpdateBehaviour updates the name and optionally re-uploads CSV
func UpdateBehaviour(c *gin.Context) {
	id := c.Param("id")

	// Check it's not the default
	var isDefault bool
	err := config.DB.QueryRow("SELECT is_default FROM behaviours WHERE id = $1", id).Scan(&isDefault)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Behaviour not found"})
		return
	}
	if isDefault {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot modify default behaviour"})
		return
	}

	name := c.PostForm("name")

	tx, err := config.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	// Update name if provided
	if name != "" {
		tx.Exec("UPDATE behaviours SET name = $1, updated_at = NOW() WHERE id = $2", name, id)
	}

	// Re-upload file if provided
	file, fileHeader, fileErr := c.Request.FormFile("file")
	if fileErr == nil {
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
			return
		}

		// Detect file type and parse
		var bucketConfigs []bucketConfigInternal
		var cashflowAssumptions []cashflowAssumptionInternal
		var parseErrors []string

		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		if ext == ".xlsx" || ext == ".xls" {
			bucketConfigs, cashflowAssumptions, parseErrors = parseScenarioXLSX(content)
		} else {
			bucketConfigs, cashflowAssumptions, parseErrors = parseScenarioCSV(content)
		}
		if len(parseErrors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "File parsing errors", "details": parseErrors})
			return
		}

		// Validate no overlapping rules within either section
		overlapErrors := validateScenarioCSV(bucketConfigs, cashflowAssumptions)
		if len(overlapErrors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Overlapping rules detected", "details": capDetails(overlapErrors)})
			return
		}

		// Delete old data and insert new
		tx.Exec("DELETE FROM scenario_bucket_configs WHERE behaviour_id = $1", id)
		tx.Exec("DELETE FROM scenario_cashflow_assumptions WHERE behaviour_id = $1", id)

		for _, b := range bucketConfigs {
			_, err := tx.Exec(
				`INSERT INTO scenario_bucket_configs (
					behaviour_id, bucket_type, bucket_name, percentage,
					product_type, ccy, segment, transactional, value_type
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				id, b.BucketType, b.BucketName, b.Percentage,
				b.ProductType, b.CCY, b.Segment, b.Transactional, b.ValueType,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert bucket config"})
				return
			}
		}

		for _, ca := range cashflowAssumptions {
			_, err := tx.Exec(
				`INSERT INTO scenario_cashflow_assumptions (
					behaviour_id, product_type, ccy, segment, transactional, bucket_type, percentage
				) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				id, ca.ProductType, ca.CCY, ca.Segment, ca.Transactional, ca.BucketType, ca.Percentage,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert cashflow assumption"})
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Behaviour updated"})
}

// DeleteBehaviour removes a behaviour and its buckets
func DeleteBehaviour(c *gin.Context) {
	id := c.Param("id")

	var isDefault bool
	err := config.DB.QueryRow("SELECT is_default FROM behaviours WHERE id = $1", id).Scan(&isDefault)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Behaviour not found"})
		return
	}
	if isDefault {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete default behaviour"})
		return
	}

	config.DB.Exec("DELETE FROM behaviours WHERE id = $1", id)
	c.JSON(http.StatusOK, gin.H{"message": "Behaviour deleted"})
}

// bucketConfigInternal is a helper for parsing Section 1
type bucketConfigInternal struct {
	BucketType    string
	BucketName    string
	Percentage    float64
	ProductType   string
	CCY           string
	Segment       string
	Transactional string
	ValueType     string
}

// cashflowAssumptionInternal is a helper for parsing Section 2
type cashflowAssumptionInternal struct {
	ProductType   string
	CCY           string
	Segment       string
	Transactional string
	BucketType    string
	Percentage    float64
}

func parseScenarioCSV(content []byte) ([]bucketConfigInternal, []cashflowAssumptionInternal, []string) {
	var errors []string

	text := string(content)
	// Remove BOM
	if strings.HasPrefix(text, "\xef\xbb\xbf") {
		text = text[3:]
	}

	// Split into two sections by empty lines
	sections := strings.Split(text, "\n\n")
	if len(sections) == 1 {
		// Try carriage return split
		sections = strings.Split(text, "\r\n\r\n")
	}

	var bucketConfigs []bucketConfigInternal
	var cashflowAssumptions []cashflowAssumptionInternal

	// Detect delimiter from first line of first section
	delimiter := ','
	if len(sections) > 0 {
		firstLine := strings.Split(sections[0], "\n")[0]
		if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
			delimiter = ';'
		}
	}

	// Section 1: Bucket Configs
	if len(sections) > 0 {
		reader := csv.NewReader(strings.NewReader(sections[0]))
		reader.Comma = delimiter
		reader.TrimLeadingSpace = true
		header, err := reader.Read()
		if err == nil {
			colMap := make(map[string]int)
			for i, h := range header {
				h = strings.ToLower(strings.TrimSpace(h))
				colMap[h] = i
			}

			for {
				record, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					continue
				}

				b := bucketConfigInternal{ValueType: "Outstanding"}
				if idx, ok := colMap["bucket type"]; ok && idx < len(record) {
					b.BucketType = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["bucket name"]; ok && idx < len(record) {
					b.BucketName = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["percentage"]; ok && idx < len(record) {
					pStr := strings.TrimSuffix(strings.TrimSpace(record[idx]), "%")
					p, _ := strconv.ParseFloat(pStr, 64)
					if p > 1.0 {
						p = p / 100.0
					}
					b.Percentage = p
				}
				if idx, ok := colMap["producttype"]; ok && idx < len(record) {
					b.ProductType = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["ccy"]; ok && idx < len(record) {
					b.CCY = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["segment"]; ok && idx < len(record) {
					b.Segment = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["transactional/non transactional"]; ok && idx < len(record) {
					b.Transactional = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["value type"]; ok && idx < len(record) {
					b.ValueType = strings.TrimSpace(record[idx])
				}

				if b.BucketType != "" && b.BucketName != "" {
					bucketConfigs = append(bucketConfigs, b)
				}
			}
		}
	}

	// Section 2: Cashflow Assumptions
	if len(sections) > 1 {
		reader := csv.NewReader(strings.NewReader(sections[1]))
		reader.Comma = delimiter
		reader.TrimLeadingSpace = true
		header, err := reader.Read()
		if err == nil {
			colMap := make(map[string]int)
			for i, h := range header {
				h = strings.ToLower(strings.TrimSpace(h))
				colMap[h] = i
			}

			for {
				record, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					continue
				}

				ca := cashflowAssumptionInternal{Percentage: 1.0}
				if idx, ok := colMap["producttype"]; ok && idx < len(record) {
					ca.ProductType = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["ccy"]; ok && idx < len(record) {
					ca.CCY = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["segment"]; ok && idx < len(record) {
					ca.Segment = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["transactional/non transactional"]; ok && idx < len(record) {
					ca.Transactional = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["bucket type"]; ok && idx < len(record) {
					ca.BucketType = strings.TrimSpace(record[idx])
				}
				if idx, ok := colMap["cashflow assumption"]; ok && idx < len(record) {
					pStr := strings.TrimSuffix(strings.TrimSpace(record[idx]), "%")
					p, _ := strconv.ParseFloat(pStr, 64)
					// Multipliers like 2.0 should stay as 2.0, only percentages like 90% should be converted if > 1 and ends in %
					// If it doesn't end in %, we assume it's a multiplier.
					if strings.HasSuffix(strings.TrimSpace(record[idx]), "%") && p > 1.0 {
						p = p / 100.0
					}
					ca.Percentage = p
				}

				if ca.BucketType != "" {
					cashflowAssumptions = append(cashflowAssumptions, ca)
				}
			}
		}
	}

	return bucketConfigs, cashflowAssumptions, errors
}

// ──────────────────────────────────────────────────────────────
//  Overlap Validator (mirrors installment_software/validator.py)
// ──────────────────────────────────────────────────────────────

// valuesOverlap returns true if two values overlap.
// "All" (case-insensitive) is a wildcard that matches anything.
// Matches Python validator.py: only "All" is a wildcard, NOT empty strings.
func valuesOverlap(a, b string) bool {
	aNorm := strings.ToLower(strings.TrimSpace(a))
	bNorm := strings.ToLower(strings.TrimSpace(b))
	return aNorm == "all" || bNorm == "all" || aNorm == bNorm
}

// formatOverlapPair shows which keys overlap, highlighting where "All" matched a specific value
func formatOverlapPair(keysA, keysB []string, colNames []string) string {
	parts := make([]string, 0, len(colNames))
	for i, col := range colNames {
		va, vb := keysA[i], keysB[i]
		vaNorm := strings.ToLower(strings.TrimSpace(va))
		vbNorm := strings.ToLower(strings.TrimSpace(vb))
		if vaNorm == vbNorm {
			parts = append(parts, fmt.Sprintf("%s='%s'", col, va))
		} else {
			parts = append(parts, fmt.Sprintf("%s=('%s' ↔ '%s')", col, va, vb))
		}
	}
	return strings.Join(parts, ", ")
}

// maxReportedDetails bounds the error list sent to the client. The frontend
// joins every entry into a single message, so an unbounded list is unreadable.
const maxReportedDetails = 25

// capDetails truncates a details list, noting how many were omitted.
func capDetails(details []string) []string {
	if len(details) <= maxReportedDetails {
		return details
	}
	out := make([]string, 0, maxReportedDetails+1)
	out = append(out, details[:maxReportedDetails]...)
	return append(out, fmt.Sprintf("… and %d more", len(details)-maxReportedDetails))
}

// distinctRule is one deduplicated rule: the key column values (original
// casing, for error messages) plus every source row that produced it.
type distinctRule struct {
	keys []string
	rows []int
}

// distinctRules collapses rows that share an identical case-insensitive key.
// A scenario sheet carries one row per Bucket Name, so a single rule such as
// "IRRBB / Loan / IDR / Retail / Transactional" legitimately spans 18 rows.
// Those rows are the same rule, not 18 rules overlapping each other, so they
// must be folded together before any pairwise comparison.
// Mirrors Validator._distinct_keyed in installment_software/validator.py.
func distinctRules(n int, keysAt func(int) []string) []distinctRule {
	pos := make(map[string]int, n)
	out := make([]distinctRule, 0, n)
	for i := 0; i < n; i++ {
		keys := keysAt(i)
		norm := make([]string, len(keys))
		for j, v := range keys {
			norm[j] = strings.ToLower(strings.TrimSpace(v))
		}
		id := strings.Join(norm, "\x00")
		if p, seen := pos[id]; seen {
			out[p].rows = append(out[p].rows, i+2)
			continue
		}
		pos[id] = len(out)
		out = append(out, distinctRule{keys: keys, rows: []int{i + 2}})
	}
	return out
}

// rulesOverlap reports whether two rules can both match the same loan, testing
// only the first n key columns (trailing columns such as Value Type take part
// in deduplication but not in the overlap test).
func rulesOverlap(a, b distinctRule, n int) bool {
	for k := 0; k < n; k++ {
		if !valuesOverlap(a.keys[k], b.keys[k]) {
			return false
		}
	}
	return true
}

// formatRows renders the source rows behind a rule, e.g. "2-19" or "2, 5, 9".
func formatRows(rows []int) string {
	if len(rows) == 1 {
		return fmt.Sprintf("row %d", rows[0])
	}
	contiguous := true
	for i := 1; i < len(rows); i++ {
		if rows[i] != rows[i-1]+1 {
			contiguous = false
			break
		}
	}
	if contiguous {
		return fmt.Sprintf("rows %d-%d", rows[0], rows[len(rows)-1])
	}
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = strconv.Itoa(r)
	}
	return "rows " + strings.Join(parts, ", ")
}

// validateScenarioCSV checks for overlapping rules within the bucket configs
// and within the cashflow assumptions.
//
// There is deliberately NO cross-section check. The two sections are
// complementary rather than competing: a scenario value is
// baseValue × bucketPercentage × cashflowAssumption, and the two factors are
// looked up independently (FindMatchingConfig / FindCashflowAssumption). A
// bucket rule and a cashflow-assumption rule that cover the same criteria are
// therefore the intended configuration, not an ambiguity — flagging them made
// every functional scenario file unuploadable.
//
// Returns a list of human-readable error strings (empty = valid).
func validateScenarioCSV(buckets []bucketConfigInternal, cfs []cashflowAssumptionInternal) []string {
	var errs []string

	// ── 0. Exact duplicate detection (matches Python validator.py) ──
	type dupKey struct {
		BucketType, BucketName, ProductType, CCY, Segment, Transactional string
	}
	dupSeen := make(map[dupKey]int) // key → first row (1-based data row)
	for i, b := range buckets {
		k := dupKey{
			strings.ToLower(strings.TrimSpace(b.BucketType)),
			strings.ToLower(strings.TrimSpace(b.BucketName)),
			strings.ToLower(strings.TrimSpace(b.ProductType)),
			strings.ToLower(strings.TrimSpace(b.CCY)),
			strings.ToLower(strings.TrimSpace(b.Segment)),
			strings.ToLower(strings.TrimSpace(b.Transactional)),
		}
		if firstRow, exists := dupSeen[k]; exists {
			errs = append(errs, fmt.Sprintf("[Bucket] Duplicate row at rows %d and %d", firstRow, i+2))
		} else {
			dupSeen[k] = i + 2
		}
	}

	// ── 1. Bucket section ──
	// Deduplicate on (BucketType, ProductType, CCY, Segment, Transactional,
	// ValueType), then test overlap on the first five of those.
	bucketKeyCols := []string{"BucketType", "ProductType", "CCY", "Segment", "Transactional"}
	bucketRules := distinctRules(len(buckets), func(i int) []string {
		return []string{
			buckets[i].BucketType, buckets[i].ProductType, buckets[i].CCY,
			buckets[i].Segment, buckets[i].Transactional, buckets[i].ValueType,
		}
	})
	for i := 0; i < len(bucketRules); i++ {
		for j := i + 1; j < len(bucketRules); j++ {
			if !rulesOverlap(bucketRules[i], bucketRules[j], len(bucketKeyCols)) {
				continue
			}
			desc := formatOverlapPair(bucketRules[i].keys, bucketRules[j].keys, bucketKeyCols)
			errs = append(errs, fmt.Sprintf(
				"[Bucket] Overlapping rule between %s and %s: %s — Value Type: '%s' vs '%s'",
				formatRows(bucketRules[i].rows), formatRows(bucketRules[j].rows), desc,
				bucketRules[i].keys[5], bucketRules[j].keys[5],
			))
		}
	}

	// ── 2. Cashflow Assumption section ──
	// Deduplicate and test overlap on the same five criteria. This section has
	// no Value Type column, so it plays no part here.
	cfKeyCols := []string{"ProductType", "CCY", "Segment", "Transactional", "BucketType"}
	cfRules := distinctRules(len(cfs), func(i int) []string {
		return []string{
			cfs[i].ProductType, cfs[i].CCY, cfs[i].Segment,
			cfs[i].Transactional, cfs[i].BucketType,
		}
	})
	for i := 0; i < len(cfRules); i++ {
		for j := i + 1; j < len(cfRules); j++ {
			if !rulesOverlap(cfRules[i], cfRules[j], len(cfKeyCols)) {
				continue
			}
			desc := formatOverlapPair(cfRules[i].keys, cfRules[j].keys, cfKeyCols)
			errs = append(errs, fmt.Sprintf(
				"[Cashflow Assumption] Overlapping rule between %s and %s: %s",
				formatRows(cfRules[i].rows), formatRows(cfRules[j].rows), desc,
			))
		}
	}

	return errs
}

// parseScenarioXLSX parses a scenario XLSX file with "Bucket" and "Cashflow Assumption" sheets.
// Mirrors the Python scenario.py Excel reader.
func parseScenarioXLSX(content []byte) ([]bucketConfigInternal, []cashflowAssumptionInternal, []string) {
	var errors []string

	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		errors = append(errors, "Failed to open XLSX file: "+err.Error())
		return nil, nil, errors
	}
	defer f.Close()

	// Find sheets (case-insensitive)
	var bucketSheet, cfSheet string
	for _, name := range f.GetSheetList() {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "bucket" {
			bucketSheet = name
		} else if lower == "cashflow assumption" {
			cfSheet = name
		}
	}

	if bucketSheet == "" {
		errors = append(errors, "Sheet 'Bucket' not found in XLSX file")
	}
	if cfSheet == "" {
		errors = append(errors, "Sheet 'Cashflow Assumption' not found in XLSX file")
	}
	if len(errors) > 0 {
		return nil, nil, errors
	}

	// Parse Bucket sheet
	var bucketConfigs []bucketConfigInternal
	bucketRows, err := f.GetRows(bucketSheet)
	if err == nil && len(bucketRows) > 1 {
		header := bucketRows[0]
		colMap := make(map[string]int)
		for i, h := range header {
			colMap[strings.ToLower(strings.TrimSpace(h))] = i
		}

		for _, row := range bucketRows[1:] {
			getVal := func(key string) string {
				idx, ok := colMap[key]
				if !ok || idx >= len(row) {
					return ""
				}
				return strings.TrimSpace(row[idx])
			}

			b := bucketConfigInternal{ValueType: "Outstanding"}
			b.BucketType = getVal("bucket type")
			b.BucketName = getVal("bucket name")

			// Parse percentage
			pStr := getVal("percentage")
			if pStr != "" {
				pStr = strings.TrimSuffix(pStr, "%")
				p, _ := strconv.ParseFloat(pStr, 64)
				if p > 1.0 {
					p = p / 100.0
				}
				b.Percentage = p
			}

			b.ProductType = getVal("producttype")
			b.CCY = getVal("ccy")
			b.Segment = getVal("segment")
			b.Transactional = getVal("transactional/non transactional")

			vt := getVal("value type")
			if vt != "" {
				b.ValueType = vt
			}

			// Convert numeric IDs to names for matching (Excel may have "2" for Deposit)
			// These stay as strings — the scenario matching uses string comparison

			if b.BucketType != "" && b.BucketName != "" {
				bucketConfigs = append(bucketConfigs, b)
			}
		}
	}

	// Parse Cashflow Assumption sheet
	var cashflowAssumptions []cashflowAssumptionInternal
	cfRows, err := f.GetRows(cfSheet)
	if err == nil && len(cfRows) > 1 {
		header := cfRows[0]
		colMap := make(map[string]int)
		for i, h := range header {
			colMap[strings.ToLower(strings.TrimSpace(h))] = i
		}

		for _, row := range cfRows[1:] {
			getVal := func(key string) string {
				idx, ok := colMap[key]
				if !ok || idx >= len(row) {
					return ""
				}
				return strings.TrimSpace(row[idx])
			}

			ca := cashflowAssumptionInternal{Percentage: 1.0}
			ca.ProductType = getVal("producttype")
			ca.CCY = getVal("ccy")
			ca.Segment = getVal("segment")
			ca.Transactional = getVal("transactional/non transactional")
			ca.BucketType = getVal("bucket type")

			// Parse cashflow assumption percentage
			pStr := getVal("cashflow assumption")
			if pStr != "" {
				pStr = strings.TrimSuffix(pStr, "%")
				p, _ := strconv.ParseFloat(pStr, 64)
				if p > 1.0 {
					p = p / 100.0
				}
				ca.Percentage = p
			}

			if ca.BucketType != "" {
				cashflowAssumptions = append(cashflowAssumptions, ca)
			}
		}
	}

	return bucketConfigs, cashflowAssumptions, errors
}
