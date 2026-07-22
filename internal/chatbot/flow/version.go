package flow

import (
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Version is an immutable published graph snapshot (Phase 15).
// Stored in chatbot_flow_versions; runtime may pin session.flow_version.
type Version struct {
	ID        uuid.UUID    `gorm:"type:uuid;primaryKey" json:"id"`
	FlowID    uuid.UUID    `gorm:"type:uuid;index;not null;uniqueIndex:idx_flow_version" json:"flow_id"`
	Version   int          `gorm:"not null;uniqueIndex:idx_flow_version" json:"version"`
	Graph     models.JSONB `gorm:"type:jsonb;not null" json:"graph"`
	PublishedAt time.Time  `json:"published_at"`
	PublishedBy *uuid.UUID `gorm:"type:uuid" json:"published_by,omitempty"`
	Notes     string       `gorm:"type:text" json:"notes,omitempty"`
}

func (Version) TableName() string { return "chatbot_flow_versions" }

// NextVersion returns max(version)+1 for a flow (0 if none).
func NextVersion(versions []Version) int {
	max := 0
	for _, v := range versions {
		if v.Version > max {
			max = v.Version
		}
	}
	return max + 1
}

// SelectGraph returns the pinned version graph or the live flow.Graph when version is 0/missing.
func SelectGraph(live models.JSONB, pinned int, versions []Version) models.JSONB {
	if pinned <= 0 {
		return live
	}
	for _, v := range versions {
		if v.Version == pinned {
			return v.Graph
		}
	}
	return live
}
