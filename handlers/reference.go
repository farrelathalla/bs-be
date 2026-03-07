package handlers

import (
	"fmt"
	"net/http"

	"bs-be/config"
	"bs-be/models"

	"github.com/gin-gonic/gin"
)

// validRefTables whitelist of allowed reference table names
var validRefTables = map[string]bool{
	"product_types":   true,
	"segments":        true,
	"methods":         true,
	"day_counts":      true,
	"currencies":      true,
	"instrument_types": true,
}

// ListReference returns all items from a reference table
func ListReference(c *gin.Context) {
	table := c.Param("table")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reference table"})
		return
	}

	rows, err := config.DB.Query(
		fmt.Sprintf("SELECT id, name FROM %s ORDER BY id", table),
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reference table"})
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

	c.JSON(http.StatusCreated, item)
}

// UpdateReference updates an item in a reference table
func UpdateReference(c *gin.Context) {
	table := c.Param("table")
	id := c.Param("id")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reference table"})
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
	c.JSON(http.StatusOK, item)
}

// DeleteReference removes an item from a reference table
func DeleteReference(c *gin.Context) {
	table := c.Param("table")
	id := c.Param("id")
	if !validRefTables[table] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid reference table"})
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

	c.JSON(http.StatusOK, gin.H{"message": "Deleted successfully"})
}
