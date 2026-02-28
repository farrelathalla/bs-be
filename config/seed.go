package config

import (
	"log"

	"golang.org/x/crypto/bcrypt"
)

// SeedAdmin creates the admin user if it doesn't exist
func SeedAdmin() {
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'admin'").Scan(&count)
	if count > 0 {
		log.Println("Admin user already exists, skipping seed")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), 10)
	if err != nil {
		log.Printf("Failed to hash password: %v", err)
		return
	}

	_, err = DB.Exec(
		"INSERT INTO users (username, password_hash) VALUES ($1, $2)",
		"admin", string(hash),
	)
	if err != nil {
		log.Printf("Failed to seed admin user: %v", err)
		return
	}

	log.Println("Admin user seeded successfully (username: admin, password: admin123)")
}
