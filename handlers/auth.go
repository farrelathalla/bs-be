package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"bs-be/config"
	"bs-be/middleware"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	UserID   string `json:"user_id"`
}

func Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password are required"})
		return
	}

	var userID, passwordHash string
	err := config.DB.QueryRow(
		"SELECT id, password_hash FROM users WHERE username = $1",
		req.Username,
	).Scan(&userID, &passwordHash)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	if !middleware.CheckPasswordHash(req.Password, passwordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// Generate token
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	// Store session
	_, err = config.DB.Exec(
		"INSERT INTO sessions (token, user_id, username, expires_at) VALUES ($1, $2, $3, $4)",
		token, userID, req.Username, time.Now().Add(24*time.Hour),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token:    token,
		Username: req.Username,
		UserID:   userID,
	})
}

func Logout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
		return
	}

	parts := splitAuth(authHeader)
	if len(parts) == 2 {
		config.DB.Exec("DELETE FROM sessions WHERE token = $1", parts[1])
	}

	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
}

func CheckAuth(c *gin.Context) {
	userID, _ := c.Get("user_id")
	username, _ := c.Get("username")
	c.JSON(http.StatusOK, gin.H{
		"user_id":  userID,
		"username": username,
	})
}

func splitAuth(header string) []string {
	result := make([]string, 0, 2)
	idx := 0
	for i, ch := range header {
		if ch == ' ' && idx == 0 {
			result = append(result, header[:i])
			idx = i + 1
		}
	}
	if idx > 0 {
		result = append(result, header[idx:])
	}
	return result
}
