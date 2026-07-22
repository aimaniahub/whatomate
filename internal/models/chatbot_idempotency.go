package models

import (
	"time"
)

// InboundIdempotency tracks Meta WhatsApp message IDs (WAMIDs) to enforce
// at-most-once chatbot processing across webhook retries and concurrent deliveries.
//
// Table: inbound_idempotency (Phase 2 of chatbot refactor).
type InboundIdempotency struct {
	// WAMID is the Meta WhatsApp message id (primary key).
	WAMID string `gorm:"column:wamid;size:255;primaryKey" json:"wamid"`

	// Status: processing | completed
	Status string `gorm:"size:20;not null;index" json:"status"`

	CorrelationID string `gorm:"size:36" json:"correlation_id"`
	TurnID        string `gorm:"size:36" json:"turn_id"`

	// LeaseUntil is when a processing lease expires and may be reclaimed.
	LeaseUntil time.Time `gorm:"not null;index" json:"lease_until"`

	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (InboundIdempotency) TableName() string {
	return "inbound_idempotency"
}

// Idempotency status values.
const (
	IdempotencyStatusProcessing = "processing"
	IdempotencyStatusCompleted  = "completed"
)
