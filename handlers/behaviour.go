package handlers

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"bs-be/config"
	"bs-be/models"

	"github.com/gin-gonic/gin"
)

// UploadBehaviour parses a scenario/behaviour CSV and saves it
func UploadBehaviour(c *gin.Context) {
	uploadID := c.Param("id")
	name := c.PostForm("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Scenario name is required"})
		return
	}

	file, _, err := c.Request.FormFile("file")
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

	// Parse CSV (2-section format)
	bucketConfigs, cashflowAssumptions, parseErrors := parseScenarioCSV(content)
	if len(parseErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV parsing errors", "details": parseErrors})
		return
	}

	// Validate no overlapping rules between Bucket and Cashflow Assumption
	overlapErrors := validateScenarioCSV(bucketConfigs, cashflowAssumptions)
	if len(overlapErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Overlapping rules detected", "details": overlapErrors})
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

	// Re-upload CSV if provided
	file, _, fileErr := c.Request.FormFile("file")
	if fileErr == nil {
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
			return
		}

		// Parse CSV
		bucketConfigs, cashflowAssumptions, parseErrors := parseScenarioCSV(content)
		if len(parseErrors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CSV parsing errors", "details": parseErrors})
			return
		}

		// Validate no overlapping rules
		overlapErrors := validateScenarioCSV(bucketConfigs, cashflowAssumptions)
		if len(overlapErrors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Overlapping rules detected", "details": overlapErrors})
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
// Empty strings also act as wildcards (match everything).
func valuesOverlap(a, b string) bool {
	aNorm := strings.ToLower(strings.TrimSpace(a))
	bNorm := strings.ToLower(strings.TrimSpace(b))
	if aNorm == "" || bNorm == "" {
		return true
	}
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

// validateScenarioCSV checks for overlapping rules within bucket configs,
// within cashflow assumptions, and across both sections.
// Returns a list of human-readable error strings (empty = valid).
func validateScenarioCSV(buckets []bucketConfigInternal, cfs []cashflowAssumptionInternal) []string {
	var errs []string

	// ── 1. Bucket section: overlap on (BucketType, ProductType, CCY, Segment, Transactional) ──
	bucketKeyCols := []string{"BucketType", "ProductType", "CCY", "Segment", "Transactional"}
	for i := 0; i < len(buckets); i++ {
		keysI := []string{buckets[i].BucketType, buckets[i].ProductType, buckets[i].CCY, buckets[i].Segment, buckets[i].Transactional}
		for j := i + 1; j < len(buckets); j++ {
			keysJ := []string{buckets[j].BucketType, buckets[j].ProductType, buckets[j].CCY, buckets[j].Segment, buckets[j].Transactional}
			overlap := true
			for k := range keysI {
				if !valuesOverlap(keysI[k], keysJ[k]) {
					overlap = false
					break
				}
			}
			if overlap {
				desc := formatOverlapPair(keysI, keysJ, bucketKeyCols)
				errs = append(errs, fmt.Sprintf("[Bucket] Overlapping rule at rows %d and %d: %s", i+2, j+2, desc))
			}
		}
	}

	// ── 2. Cashflow Assumption section: overlap on (ProductType, CCY, Segment, Transactional, BucketType) ──
	cfKeyCols := []string{"ProductType", "CCY", "Segment", "Transactional", "BucketType"}
	for i := 0; i < len(cfs); i++ {
		keysI := []string{cfs[i].ProductType, cfs[i].CCY, cfs[i].Segment, cfs[i].Transactional, cfs[i].BucketType}
		for j := i + 1; j < len(cfs); j++ {
			keysJ := []string{cfs[j].ProductType, cfs[j].CCY, cfs[j].Segment, cfs[j].Transactional, cfs[j].BucketType}
			overlap := true
			for k := range keysI {
				if !valuesOverlap(keysI[k], keysJ[k]) {
					overlap = false
					break
				}
			}
			if overlap {
				desc := formatOverlapPair(keysI, keysJ, cfKeyCols)
				errs = append(errs, fmt.Sprintf("[Cashflow Assumption] Overlapping rule at rows %d and %d: %s", i+2, j+2, desc))
			}
		}
	}

	// ── 3. Cross-section: Bucket × Cashflow Assumption on (BucketType, ProductType, CCY, Segment, Transactional) ──
	crossKeyCols := []string{"BucketType", "ProductType", "CCY", "Segment", "Transactional"}
	for i, b := range buckets {
		keysB := []string{b.BucketType, b.ProductType, b.CCY, b.Segment, b.Transactional}
		for j, cf := range cfs {
			keysCF := []string{cf.BucketType, cf.ProductType, cf.CCY, cf.Segment, cf.Transactional}
			overlap := true
			for k := range keysB {
				if !valuesOverlap(keysB[k], keysCF[k]) {
					overlap = false
					break
				}
			}
			if overlap {
				desc := formatOverlapPair(keysB, keysCF, crossKeyCols)
				errs = append(errs, fmt.Sprintf("[Cross-Sheet] Bucket row %d overlaps with Cashflow Assumption row %d: %s", i+2, j+2, desc))
			}
		}
	}

	return errs
}

