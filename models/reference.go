package models

// ReferenceItem is a generic key-value pair for lookup tables
type ReferenceItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
