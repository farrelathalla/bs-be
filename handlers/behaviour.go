package handlers

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"bs-be/config"
	"bs-be/models"

	"github.com/gin-gonic/gin"
)

// UploadBehaviour parses a behaviour CSV and saves it with a name
func UploadBehaviour(c *gin.Context) {
	uploadID := c.Param("id")
	name := c.PostForm("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Behaviour name is required"})
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

	// Parse CSV
	buckets, parseErrors := parseBehaviourCSV(content)
	if len(parseErrors) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV parsing errors", "details": parseErrors})
		return
	}

	// Check upload exists
	var exists int
	config.DB.QueryRow("SELECT COUNT(*) FROM uploads WHERE id = $1", uploadID).Scan(&exists)
	if exists == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upload not found"})
		return
	}

	// Insert behaviour
	tx, err := config.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback()

	var behaviourID int64
	err = tx.QueryRow(
		"INSERT INTO behaviours (upload_id, name, is_default) VALUES ($1, $2, FALSE) RETURNING id",
		uploadID, name,
	).Scan(&behaviourID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create behaviour: " + err.Error()})
		return
	}

	for _, b := range buckets {
		_, err := tx.Exec(
			"INSERT INTO behaviour_buckets (behaviour_id, bucket_type, bucket_name, percentage) VALUES ($1, $2, $3, $4)",
			behaviourID, b.BucketType, b.BucketName, b.Percentage,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to insert bucket %s/%s: %v", b.BucketType, b.BucketName, err)})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        behaviourID,
		"upload_id": uploadID,
		"name":      name,
		"buckets":   len(buckets),
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

		buckets, parseErrors := parseBehaviourCSV(content)
		if len(parseErrors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CSV parsing errors", "details": parseErrors})
			return
		}

		// Delete old buckets and insert new
		tx.Exec("DELETE FROM behaviour_buckets WHERE behaviour_id = $1", id)
		for _, b := range buckets {
			_, err := tx.Exec(
				"INSERT INTO behaviour_buckets (behaviour_id, bucket_type, bucket_name, percentage) VALUES ($1, $2, $3, $4)",
				id, b.BucketType, b.BucketName, b.Percentage,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert bucket"})
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

// parseBehaviourCSV parses a behaviour CSV with columns: Bucket Type, Bucket Name, Percentage
func parseBehaviourCSV(content []byte) ([]models.BehaviourBucket, []string) {
	var errors []string

	text := string(content)
	// Remove BOM
	if utf8.RuneCountInString(text) > 0 {
		r, _ := utf8.DecodeRuneInString(text)
		if r == '\uFEFF' {
			text = text[3:]
		}
	}

	// Detect delimiter
	delimiter := ','
	firstLine := text
	if idx := strings.Index(text, "\n"); idx >= 0 {
		firstLine = text[:idx]
	}
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		delimiter = ';'
	}
	if strings.Count(firstLine, "\t") > strings.Count(firstLine, string(delimiter)) {
		delimiter = '\t'
	}

	csvReader := csv.NewReader(strings.NewReader(text))
	csvReader.Comma = delimiter
	csvReader.LazyQuotes = true
	csvReader.TrimLeadingSpace = true

	header, err := csvReader.Read()
	if err != nil {
		errors = append(errors, "Failed to read header: "+err.Error())
		return nil, errors
	}

	// Normalize headers
	colIndex := make(map[string]int)
	for i, h := range header {
		lower := strings.ToLower(strings.TrimSpace(h))
		switch {
		case strings.Contains(lower, "bucket type") || lower == "buckettype" || lower == "bucket_type":
			colIndex["bucket_type"] = i
		case strings.Contains(lower, "bucket name") || lower == "bucketname" || lower == "bucket_name":
			colIndex["bucket_name"] = i
		case strings.Contains(lower, "percentage") || lower == "pct" || lower == "percent":
			colIndex["percentage"] = i
		}
	}

	if _, ok := colIndex["bucket_type"]; !ok {
		errors = append(errors, "Missing 'Bucket Type' column")
	}
	if _, ok := colIndex["bucket_name"]; !ok {
		errors = append(errors, "Missing 'Bucket Name' column")
	}
	if _, ok := colIndex["percentage"]; !ok {
		errors = append(errors, "Missing 'Percentage' column")
	}
	if len(errors) > 0 {
		return nil, errors
	}

	var buckets []models.BehaviourBucket
	rowNum := 1
	for {
		rowNum++
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			errors = append(errors, fmt.Sprintf("Row %d: parse error: %v", rowNum, err))
			continue
		}

		bucketType := strings.TrimSpace(record[colIndex["bucket_type"]])
		bucketName := strings.TrimSpace(record[colIndex["bucket_name"]])
		pctStr := strings.TrimSpace(record[colIndex["percentage"]])

		if bucketType == "" || bucketName == "" {
			errors = append(errors, fmt.Sprintf("Row %d: bucket_type and bucket_name are required", rowNum))
			continue
		}

		// Parse percentage - support both 0.5 and 50% formats
		pctStr = strings.TrimSuffix(pctStr, "%")
		pct, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Row %d: invalid percentage '%s'", rowNum, pctStr))
			continue
		}

		// If value > 1, assume it's a percentage and convert
		if pct > 1.0 {
			pct = pct / 100.0
		}

		buckets = append(buckets, models.BehaviourBucket{
			BucketType: strings.ToUpper(bucketType),
			BucketName: bucketName,
			Percentage: pct,
		})
	}

	return buckets, errors
}
