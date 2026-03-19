package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"bs-be/config"

	"github.com/gin-gonic/gin"
)

type presetRequest struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type presetResponse struct {
	ID        int64           `json:"id"`
	UserID    string          `json:"user_id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// ListPresets returns all presets for the authenticated user
func ListPresets(c *gin.Context) {
	userID, _ := c.Get("user_id")

	rows, err := config.DB.Query(
		`SELECT id, user_id, name, config, created_at, updated_at
		 FROM user_presets WHERE user_id = $1 ORDER BY created_at`,
		userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list presets"})
		return
	}
	defer rows.Close()

	presets := make([]presetResponse, 0)
	for rows.Next() {
		var p presetResponse
		var configBytes []byte
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &configBytes, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		p.Config = json.RawMessage(configBytes)
		presets = append(presets, p)
	}

	c.JSON(http.StatusOK, presets)
}

// CreatePreset creates a new preset for the authenticated user
func CreatePreset(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var req presetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	configBytes, _ := json.Marshal(req.Config)

	var id int64
	err := config.DB.QueryRow(
		`INSERT INTO user_presets (user_id, name, config)
		 VALUES ($1, $2, $3) RETURNING id`,
		userID, req.Name, string(configBytes),
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create preset: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name})
}

// UpdatePreset updates an existing preset
func UpdatePreset(c *gin.Context) {
	userID, _ := c.Get("user_id")
	presetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preset ID"})
		return
	}

	var req presetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	configBytes, _ := json.Marshal(req.Config)

	result, err := config.DB.Exec(
		`UPDATE user_presets SET name = $1, config = $2, updated_at = NOW()
		 WHERE id = $3 AND user_id = $4`,
		req.Name, string(configBytes), presetID, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update preset"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Preset not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Updated successfully"})
}

// DeletePreset deletes a preset
func DeletePreset(c *gin.Context) {
	userID, _ := c.Get("user_id")
	presetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preset ID"})
		return
	}

	result, err := config.DB.Exec(
		`DELETE FROM user_presets WHERE id = $1 AND user_id = $2`,
		presetID, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete preset"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Preset not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Deleted successfully"})
}
