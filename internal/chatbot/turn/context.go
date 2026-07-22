package turn

import (
	"time"

	"github.com/google/uuid"
)

// Context is the normalized inbound turn input for the Conversation Orchestrator.
// Phase 5: carries identity + raw message for the legacy adapter.
// Later phases fill SessionID, Settings, etc. before ladder steps need them.
type Context struct {
	IDs IDs

	// Channel / account
	PhoneNumberID   string
	OrganizationID  uuid.UUID
	WhatsAppAccount string
	ProfileName     string

	// Sender
	FromPhone string
	WAMID     string
	MsgType   string

	// Pre-parsed convenience fields (may be empty until full parse)
	Text     string
	ButtonID string

	// RawMessage is JSON of the inbound payload (handlers IncomingTextMessage shape).
	// Legacy adapter unmarshals this to avoid orchestrator→handlers import cycles.
	RawMessage []byte

	// StartedAt is when HandleTurn began (for duration metrics).
	StartedAt time.Time
}

// TimelineEntry records one orchestrator decision for logs / future turn_traces.
type TimelineEntry struct {
	Stage      string `json:"stage"`
	Decision   string `json:"decision"`
	Handler    string `json:"handler,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Result is the output of Orchestrator.HandleTurn.
type Result struct {
	// Claimed is true when a handler owned the turn (legacy always claims in Phase 5).
	Claimed bool
	// Handler is the winning handler name (e.g. "legacy_full").
	Handler string
	// Path is "orchestrator" or "legacy_direct".
	Path string
	// Timeline is the ordered ladder / routing decisions.
	Timeline []TimelineEntry
	// Error is a non-fatal note; hard failures return as error from HandleTurn.
	Notes string
}

// Append adds a timeline entry.
func (r *Result) Append(stage, decision, handler, detail string) {
	if r == nil {
		return
	}
	r.Timeline = append(r.Timeline, TimelineEntry{
		Stage:    stage,
		Decision: decision,
		Handler:  handler,
		Detail:   detail,
	})
}
