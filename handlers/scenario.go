package handlers

import (
	"fmt"
	"net/http"

	"bs-be/config"
	"bs-be/models"

	"github.com/gin-gonic/gin"
)

// ListMappings returns all scenario mappings for an upload
func ListMappings(c *gin.Context) {
	uploadID := c.Param("id")

	rows, err := config.DB.Query(
		`SELECT sm.id, sm.upload_id, sm.product_type, sm.ccy, sm.segment, sm.transactional,
		        sm.behaviour_id, COALESCE(b.name, '') as behaviour_name
		 FROM scenario_mappings sm
		 LEFT JOIN behaviours b ON b.id = sm.behaviour_id
		 WHERE sm.upload_id = $1
		 ORDER BY sm.id`,
		uploadID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query mappings: " + err.Error()})
		return
	}
	defer rows.Close()

	mappings := make([]models.ScenarioMapping, 0)
	for rows.Next() {
		var m models.ScenarioMapping
		if rows.Scan(&m.ID, &m.UploadID, &m.ProductType, &m.CCY, &m.Segment,
			&m.Transactional, &m.BehaviourID, &m.BehaviourName) == nil {
			mappings = append(mappings, m)
		}
	}

	c.JSON(http.StatusOK, mappings)
}

// CreateMapping creates a new scenario mapping
func CreateMapping(c *gin.Context) {
	uploadID := c.Param("id")

	var m models.ScenarioMapping
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Validate behaviour exists
	var bExists int
	config.DB.QueryRow("SELECT COUNT(*) FROM behaviours WHERE id = $1", m.BehaviourID).Scan(&bExists)
	if bExists == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Behaviour not found"})
		return
	}

	var id int64
	err := config.DB.QueryRow(
		`INSERT INTO scenario_mappings (upload_id, product_type, ccy, segment, transactional, behaviour_id)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		uploadID, m.ProductType, m.CCY, m.Segment, m.Transactional, m.BehaviourID,
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create mapping: " + err.Error()})
		return
	}

	m.ID = id
	m.UploadID = uploadID
	c.JSON(http.StatusCreated, m)
}

// UpdateMapping updates a scenario mapping
func UpdateMapping(c *gin.Context) {
	id := c.Param("id")

	var m models.ScenarioMapping
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	result, err := config.DB.Exec(
		`UPDATE scenario_mappings
		 SET product_type = $1, ccy = $2, segment = $3, transactional = $4, behaviour_id = $5
		 WHERE id = $6`,
		m.ProductType, m.CCY, m.Segment, m.Transactional, m.BehaviourID, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update: " + err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Mapping not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mapping updated"})
}

// DeleteMapping removes a scenario mapping
func DeleteMapping(c *gin.Context) {
	id := c.Param("id")

	result, err := config.DB.Exec("DELETE FROM scenario_mappings WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Mapping not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mapping deleted"})
}

// GetMappingOptions returns the distinct ProductType, CCY, Segment, Transactional values
// from the upload's loan_inputs, for populating dropdown filters in the UI
func GetMappingOptions(c *gin.Context) {
	uploadID := c.Param("id")

	result := gin.H{}

	colMap := map[string]string{
		"product_types":  "product_type",
		"ccys":           "ccy",
		"segments":       "segment",
		"transactionals": "transactional_or_non",
	}

	for key, col := range colMap {
		rows, err := config.DB.Query(
			fmt.Sprintf("SELECT DISTINCT %s FROM loan_inputs WHERE upload_id = $1 AND %s IS NOT NULL AND %s != '' ORDER BY %s",
				col, col, col, col),
			uploadID,
		)
		if err != nil {
			continue
		}

		var values []string
		for rows.Next() {
			var v string
			if rows.Scan(&v) == nil {
				values = append(values, v)
			}
		}
		rows.Close()

		if values == nil {
			values = []string{}
		}
		result[key] = values
	}

	c.JSON(http.StatusOK, result)
}
