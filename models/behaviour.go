package models

import "time"

// Behaviour represents a named behaviour definition (e.g. "Default Behaviour", "Covid Behaviour")
type Behaviour struct {
	ID        int64             `json:"id"`
	UploadID  *string           `json:"upload_id,omitempty"` // nil for global/default
	Name      string            `json:"name"`
	IsDefault bool              `json:"is_default"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Buckets   []BehaviourBucket `json:"buckets,omitempty"`
}

// BehaviourBucket represents a single bucket percentage within a behaviour
type BehaviourBucket struct {
	ID          int64   `json:"id,omitempty"`
	BehaviourID int64   `json:"behaviour_id"`
	BucketType  string  `json:"bucket_type"`  // LCR, NSFR, IRRBB, ILAAP
	BucketName  string  `json:"bucket_name"`
	Percentage  float64 `json:"percentage"`
}

// ScenarioMapping maps loan criteria (ProductType+CCY+Segment+Transactional) to a Behaviour
type ScenarioMapping struct {
	ID            int64  `json:"id"`
	UploadID      string `json:"upload_id"`
	ProductType   string `json:"product_type"`
	CCY           string `json:"ccy"`
	Segment       string `json:"segment"`
	Transactional string `json:"transactional"`
	BehaviourID   int64  `json:"behaviour_id"`
	BehaviourName string `json:"behaviour_name,omitempty"` // joined from behaviours table
}
