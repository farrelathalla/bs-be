package config

import (
	"log"

	"golang.org/x/crypto/bcrypt"
)

// SeedAdmin creates the admin user if it doesn't exist
func SeedAdmin() {
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'admin'").Scan(&count)
	if count == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), 10)
		if err != nil {
			log.Printf("Failed to hash password: %v", err)
			return
		}
		_, err = DB.Exec(
			"INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)",
			"admin", string(hash), "user",
		)
		if err != nil {
			log.Printf("Failed to seed admin user: %v", err)
		} else {
			log.Println("Admin user seeded successfully (username: admin, password: admin123)")
		}
	}
}

// SeedSuperAdmin creates the superadmin user if it doesn't exist
func SeedSuperAdmin() {
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'superadmin'").Scan(&count)
	if count > 0 {
		log.Println("Superadmin user already exists, skipping seed")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), 10)
	if err != nil {
		log.Printf("Failed to hash password: %v", err)
		return
	}

	_, err = DB.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)",
		"superadmin", string(hash), "superadmin",
	)
	if err != nil {
		log.Printf("Failed to seed superadmin user: %v", err)
		return
	}

	log.Println("Superadmin user seeded successfully (username: superadmin, password: admin123)")
}

// SeedReferenceData populates the lookup tables with initial values
func SeedReferenceData() {
	refs := map[string][][2]string{
		"product_types": {
			{"1", "Loan"},
			{"2", "Deposit"},
			{"3", "Saving"},
			{"4", "Giro"},
		},
		"segments": {
			{"1", "Retail"},
			{"2", "Korporasi"},
		},
		"methods": {
			{"1", "Annuity"},
			{"2", "Flat"},
		},
		"day_counts": {
			{"1", "30/360"},
			{"2", "ACT/360"},
			{"3", "ACT/365"},
		},
		"currencies": {
			{"IDR", "Indonesia Rupiah"},
			{"USD", "US Dollar"},
		},
		"instrument_types": {
			{"1", "Fixed"},
			{"2", "Floating"},
			{"3", "Nonmaturing"},
		},
		"transactional_types": {
			{"1", "Transactional"},
			{"2", "Non Transactional"},
		},
		"installment_frequencies": {
			{"1", "Monthly"},
			{"2", "Bi-Monthly"},
			{"3", "Quarterly"},
			{"6", "Semi-Annual"},
			{"12", "Annual"},
		},
		"insured_types": {
			{"1", "Insured"},
			{"2", "Uninsured"},
		},
		"asset_liabilities": {
			{"1", "Asset"},
			{"2", "Liability"},
		},
		"revolving_flags": {
			{"1", "Revolving"},
			{"2", "Non Revolving"},
		},
	}

	for table, items := range refs {
		for _, item := range items {
			_, err := DB.Exec(
				"INSERT INTO "+table+" (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING",
				item[0], item[1],
			)
			if err != nil {
				log.Printf("Failed to seed %s (%s): %v", table, item[0], err)
			}
		}
	}

	log.Println("Reference data seeded successfully")
}

// SeedDefaultBehaviour creates the global "Default Behaviour" with bucket percentages
func SeedDefaultBehaviour() {
	// Check if default behaviour already exists
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM behaviours WHERE is_default = TRUE AND upload_id IS NULL").Scan(&count)
	if count > 0 {
		log.Println("Default behaviour already exists, skipping seed")
		return
	}

	// Insert default behaviour
	var behaviourID int64
	err := DB.QueryRow(
		"INSERT INTO behaviours (upload_id, name, is_default) VALUES (NULL, $1, TRUE) RETURNING id",
		"Default Behaviour",
	).Scan(&behaviourID)
	if err != nil {
		log.Printf("Failed to seed default behaviour: %v", err)
		return
	}

	// Default behaviour bucket percentages
	// Based on the image: all buckets at 0% except specific ones at 100%
	type bucketRow struct {
		bucketType string
		bucketName string
		percentage float64
	}

	buckets := []bucketRow{
		// LCR
		{"LCR", "CF <= 30D", 1.0},
		{"LCR", "CF > 30D", 0.0},
		// NSFR
		{"NSFR", "CF < 6M", 0.0},
		{"NSFR", "CF 6M to 12M", 0.0},
		{"NSFR", "CF > 12M", 0.0},
		// IRRBB
		{"IRRBB", "≤ 1 M", 0.0},
		{"IRRBB", "1M ≤ 3M", 0.0},
		{"IRRBB", "3M ≤ 6M", 0.0},
		{"IRRBB", "6M ≤ 9M", 0.0},
		{"IRRBB", "9M ≤ 1Y", 0.0},
		{"IRRBB", "1Y ≤ 1.5Y", 0.0},
		{"IRRBB", "1.5Y ≤ 2Y", 0.0},
		{"IRRBB", "2Y ≤ 3Y", 0.0},
		{"IRRBB", "3Y ≤ 4Y", 0.0},
		{"IRRBB", "4Y ≤ 5Y", 0.0},
		{"IRRBB", "5Y ≤ 6Y", 0.0},
		{"IRRBB", "6Y ≤ 7Y", 0.0},
		{"IRRBB", "7Y ≤ 8Y", 0.0},
		{"IRRBB", "8Y ≤ 9Y", 0.0},
		{"IRRBB", "9Y ≤ 10Y", 0.0},
		{"IRRBB", "10Y ≤ 15Y", 0.0},
		{"IRRBB", "15Y ≤ 20Y", 0.0},
		{"IRRBB", "> 20Y", 0.0},
		// ILAAP
		{"ILAAP", "No Maturity", 1.0},
		{"ILAAP", "D-1", 0.0},
		{"ILAAP", "D-2", 0.0},
		{"ILAAP", "D-3", 0.0},
		{"ILAAP", "D-4", 0.0},
		{"ILAAP", "D-5", 0.0},
		{"ILAAP", "D-6", 0.0},
		{"ILAAP", "D-7", 0.0},
		{"ILAAP", "D-8", 0.0},
		{"ILAAP", "D-9", 0.0},
		{"ILAAP", "D-10", 0.0},
		{"ILAAP", "D-11", 0.0},
		{"ILAAP", "D-12", 0.0},
		{"ILAAP", "D-13", 0.0},
		{"ILAAP", "D-14", 0.0},
		{"ILAAP", "D-15", 0.0},
		{"ILAAP", "D-16", 0.0},
		{"ILAAP", "D-17", 0.0},
		{"ILAAP", "D-18", 0.0},
		{"ILAAP", "D-19", 0.0},
		{"ILAAP", "D-20", 0.0},
		{"ILAAP", "D-21", 0.0},
		{"ILAAP", "D-22", 0.0},
		{"ILAAP", "D-23", 0.0},
		{"ILAAP", "D-24", 0.0},
		{"ILAAP", "D-25", 0.0},
		{"ILAAP", "D-26", 0.0},
		{"ILAAP", "D-27", 0.0},
		{"ILAAP", "D-28", 0.0},
		{"ILAAP", "D-29", 0.0},
		{"ILAAP", "D-30", 0.0},
		{"ILAAP", "W4 <= W5", 0.0},
		{"ILAAP", "W5 <= 2M", 0.0},
		{"ILAAP", "2M <= 3M", 0.0},
		{"ILAAP", "3M <= 4M", 0.0},
		{"ILAAP", "4M <= 5M", 0.0},
		{"ILAAP", "5M <= 6M", 0.0},
		{"ILAAP", "6M <= 9M", 0.0},
		{"ILAAP", "9M <= 12M", 0.0},
		{"ILAAP", "12M <= 2Y", 0.0},
		{"ILAAP", "2Y <= 5Y", 0.0},
		{"ILAAP", ">5Y", 0.0},
	}

	for _, b := range buckets {
		_, err := DB.Exec(
			"INSERT INTO behaviour_buckets (behaviour_id, bucket_type, bucket_name, percentage) VALUES ($1, $2, $3, $4)",
			behaviourID, b.bucketType, b.bucketName, b.percentage,
		)
		if err != nil {
			log.Printf("Failed to seed bucket %s/%s: %v", b.bucketType, b.bucketName, err)
		}
	}

	log.Println("Default behaviour seeded successfully")
}
