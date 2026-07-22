package turn

import (
	"github.com/google/uuid"
)

// IDs correlates a single inbound processing attempt for logs and timelines.
//
// CorrelationID groups related work from one accept path (e.g. one Meta message).
// TurnID uniquely identifies one HandleTurn / processIncomingMessage attempt
// (retries get a new TurnID under the same CorrelationID when reprocessing).
type IDs struct {
	CorrelationID string
	TurnID        string
}

// NewIDs allocates a new correlation id and turn id.
func NewIDs() IDs {
	return IDs{
		CorrelationID: uuid.NewString(),
		TurnID:        uuid.NewString(),
	}
}

// NewTurn reuses correlation and allocates a fresh turn id (e.g. internal retry).
func NewTurn(correlationID string) IDs {
	if correlationID == "" {
		return NewIDs()
	}
	return IDs{
		CorrelationID: correlationID,
		TurnID:        uuid.NewString(),
	}
}

// LogArgs returns key/value pairs for structured logf-style loggers.
func (id IDs) LogArgs() []any {
	return []any{
		"correlation_id", id.CorrelationID,
		"turn_id", id.TurnID,
	}
}

// With appends LogArgs to extra fields for a single log call.
func (id IDs) With(extra ...any) []any {
	args := id.LogArgs()
	return append(args, extra...)
}

// Valid reports whether both identifiers are non-empty.
func (id IDs) Valid() bool {
	return id.CorrelationID != "" && id.TurnID != ""
}
