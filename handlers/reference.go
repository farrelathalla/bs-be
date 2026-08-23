package handlers

import (
	"fmt"
	"net/http"

	"bs-be/config"
	"bs-be/models"
	"bs-be/validator"

	"github.com/gin-gonic/gin"
)

// validRefTables and allRefTableNames are derived from validator.MasterTables,
// the single catalog of reference ("master data") tables.
var validRefTables = buildValidRefTables()
var allRefTableNames = buildRefTableNames()

func buildValidRefTables() map[string]bool {
	m := make(map[string]bool, len(validator.MasterTables))
	for _, t := range validator.MasterTables {
		m[t.Key] = true
	}
	return m
}

func buildRefTableNames() []string {
	out := make([]string, 0, len(validator.MasterTables))
	for _, t := range validator.MasterTables {
		out = append(out, t.Key)
	}
	return out
}

// GetAllReferenceMaps returns all reference tables as a single JSON object
// Accessible by all authenticated users (not just superadmin)
func GetAllReferenceMaps(c *gin.Context) {
	result := make(map[string][]models.ReferenceItem)

	for _, table := range allRefTableNames {
		rows, err := config.DB.Query(
			fmt.Sprintf("SELECT id, name FROM %s ORDER BY LENGTH(id), id", table),
		)
		if err != nil {
			result[table] = []models.ReferenceItem{}
			continue
		}

		items := make([]models.ReferenceItem, 0)
		for rows.Next() {
			var item models.ReferenceItem
			if rows.Scan(&item.ID, &item.Name) == nil {
				items = append(items, item)
			}
		}
		rows.Close()
		result[table] = items
	}

	c.JSON(http.StatusOK, result)
}

// ListReference returns all items from a reference table
func ListReference(c *gin.Context) {
	table := c.Param("table")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown master data table '" + table + "'"})
		return
	}

	rows, err := config.DB.Query(
		fmt.Sprintf("SELECT id, name FROM %s ORDER BY LENGTH(id), id", table),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query: " + err.Error()})
		return
	}
	defer rows.Close()

	items := make([]models.ReferenceItem, 0)
	for rows.Next() {
		var item models.ReferenceItem
		if rows.Scan(&item.ID, &item.Name) == nil {
			items = append(items, item)
		}
	}

	c.JSON(http.StatusOK, items)
}

// CreateReference adds a new item to a reference table
func CreateReference(c *gin.Context) {
	table := c.Param("table")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown master data table '" + table + "'"})
		return
	}

	var item models.ReferenceItem
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if item.ID == "" || item.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Both id and name are required"})
		return
	}

	_, err := config.DB.Exec(
		fmt.Sprintf("INSERT INTO %s (id, name) VALUES ($1, $2)", table),
		item.ID, item.Name,
	)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "ID already exists or insert failed: " + err.Error()})
		return
	}

	validator.InvalidateMasterData()
	c.JSON(http.StatusCreated, item)
}

// UpdateReference updates an item in a reference table
func UpdateReference(c *gin.Context) {
	table := c.Param("table")
	id := c.Param("id")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown master data table '" + table + "'"})
		return
	}

	var item models.ReferenceItem
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if item.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	result, err := config.DB.Exec(
		fmt.Sprintf("UPDATE %s SET name = $1, updated_at = NOW() WHERE id = $2", table),
		item.Name, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update: " + err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
		return
	}

	item.ID = id
	validator.InvalidateMasterData()
	c.JSON(http.StatusOK, item)
}

// refUsageColumns maps a master data table to the loan_inputs column that
// stores its codes, so a value still present in uploaded data cannot be
// deleted out from under it.
var refUsageColumns = map[string]string{
	"product_types":           "product_type",
	"segments":                "segment",
	"methods":                 "method",
	"day_counts":              "day_count",
	"currencies":              "ccy",
	"instrument_types":        "instrument_type",
	"transactional_types":     "transactional_or_non",
	"insured_types":           "insured_or_uninsured",
	"revolving_flags":         "revolving_flag",
	"installment_frequencies": "installment_frequency",
}

// countReferenceUsage returns how many uploaded rows still use a code.
func countReferenceUsage(table, id string) int {
	column, ok := refUsageColumns[table]
	if !ok {
		return 0
	}
	var count int
	err := config.DB.QueryRow(
		fmt.Sprintf("SELECT COUNT(*) FROM loan_inputs WHERE %s::text = $1", column), id,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// DeleteReference removes an item from a reference table
func DeleteReference(c *gin.Context) {
	table := c.Param("table")
	id := c.Param("id")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown master data table '" + table + "'"})
		return
	}

	if used := countReferenceUsage(table, id); used > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf(
			"'%s' is still used by %d uploaded row(s). Delete or re-upload those files first, or rename this entry instead of deleting it.",
			id, used,
		)})
		return
	}

	result, err := config.DB.Exec(
		fmt.Sprintf("DELETE FROM %s WHERE id = $1", table),
		id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete: " + err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
		return
	}

	validator.InvalidateMasterData()
	c.JSON(http.StatusOK, gin.H{"message": "Deleted successfully"})
}
