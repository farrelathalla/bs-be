package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"bs-be/config"
	"bs-be/handlers"
	"bs-be/middleware"

	"github.com/gin-gonic/gin"
)

func runMigrations(db *sql.DB) {
	// Read and execute migration files
	migrationFiles := []string{
		"migrations/001_init.sql",
		"migrations/003_reference_tables.sql",
		"migrations/004_behaviours.sql",
		"migrations/005_scenarios.sql",
	}

	for _, f := range migrationFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			log.Printf("Warning: Could not read migration %s: %v", f, err)
			continue
		}
		_, err = db.Exec(string(content))
		if err != nil {
			log.Printf("Warning: Migration %s error (may already exist): %v", f, err)
		} else {
			log.Printf("Migration %s applied successfully", f)
		}
	}
}

func main() {
	// Initialize database
	config.InitDB()
	defer config.DB.Close()

	// Run migrations
	runMigrations(config.DB)

	// Seed data
	config.SeedAdmin()
	config.SeedSuperAdmin()
	config.SeedReferenceData()
	config.SeedDefaultBehaviour()

	// Ensure upload directory exists
	uploadDir := config.GetUploadDir()
	os.MkdirAll(uploadDir, 0755)

	// Setup router
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Apply CORS
	r.Use(middleware.CORSMiddleware())

	// Set max multipart memory (100MB)
	r.MaxMultipartMemory = 100 << 20

	// Public routes
	r.POST("/api/login", handlers.Login)

	// Health check
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Protected routes
	auth := r.Group("/api")
	auth.Use(middleware.AuthMiddleware())
	{
		// Auth
		auth.GET("/auth/check", handlers.CheckAuth)
		auth.POST("/auth/logout", handlers.Logout)

		// Upload
		auth.POST("/upload", handlers.UploadCSV)
		auth.GET("/upload/status/:id", handlers.GetUploadStatus)

		// History
		auth.GET("/history", handlers.GetHistory)
		auth.DELETE("/history/:id", handlers.DeleteUpload)

		// Results
		auth.GET("/results/:id", handlers.GetResults)
		auth.GET("/results/:id/summary", handlers.GetSummary)
		auth.GET("/results/:id/filter-options", handlers.GetFilterOptions)

		// Pivot
		auth.GET("/pivot/:id", handlers.GetPivot)

		// Export
		auth.GET("/export/:id", handlers.ExportExcel)

		// Behaviours (per upload)
		auth.POST("/uploads/:id/behaviours", handlers.UploadBehaviour)
		auth.GET("/uploads/:id/behaviours", handlers.ListBehaviours)
		auth.GET("/behaviours/:id", handlers.GetBehaviour)
		auth.PUT("/behaviours/:id", handlers.UpdateBehaviour)
		auth.DELETE("/behaviours/:id", handlers.DeleteBehaviour)

		// Scenario Mappings (per upload)
		auth.GET("/uploads/:id/mappings", handlers.ListMappings)
		auth.POST("/uploads/:id/mappings", handlers.CreateMapping)
		auth.PUT("/mappings/:id", handlers.UpdateMapping)
		auth.DELETE("/mappings/:id", handlers.DeleteMapping)
		auth.GET("/uploads/:id/mapping-options", handlers.GetMappingOptions)

		// Reprocess
		auth.POST("/uploads/:id/reprocess", handlers.ReprocessUpload)

		// Reference tables (superadmin only)
		admin := auth.Group("")
		admin.Use(middleware.SuperAdminMiddleware())
		{
			admin.GET("/reference/:table", handlers.ListReference)
			admin.POST("/reference/:table", handlers.CreateReference)
			admin.PUT("/reference/:table/:id", handlers.UpdateReference)
			admin.DELETE("/reference/:table/:id", handlers.DeleteReference)
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8002"
	}

	log.Printf("Server starting on port %s", port)
	if err := r.Run(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
