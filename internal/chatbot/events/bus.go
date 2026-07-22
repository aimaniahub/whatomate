package events

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Names match target architecture §15.
const (
	MessageReceived     = "MessageReceived"
	SessionCreated      = "SessionCreated"
	SessionStateChanged = "SessionStateChanged"
	FlowStarted         = "FlowStarted"
	FlowCompleted       = "FlowCompleted"
	KeywordMatched      = "KeywordMatched"
	AIReturned          = "AIReturned"
	TransferStarted     = "TransferStarted"
	TurnCompleted       = "TurnCompleted"
	WaitStarted         = "WaitStarted"
	WaitResolved        = "WaitResolved"
	SessionExpired      = "SessionExpired"
)

// Event is a domain event for outbox / in-process subscribers (Phase 16).
type Event struct {
	Name      string
	At        time.Time
	OrgID     uuid.UUID
	SessionID uuid.UUID
	ContactID uuid.UUID
	Payload   map[string]any
}

// Handler consumes events (must be idempotent).
type Handler func(Event)

// Bus is a simple in-process event bus. Outbox DB persistence can wrap Emit later.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
	all      []Handler
}

// NewBus creates an empty bus.
func NewBus() *Bus {
	return &Bus{handlers: make(map[string][]Handler)}
}

// On registers a handler for an event name.
func (b *Bus) On(name string, h Handler) {
	if b == nil || h == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[name] = append(b.handlers[name], h)
}

// OnAny registers a handler for all events.
func (b *Bus) OnAny(h Handler) {
	if b == nil || h == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.all = append(b.all, h)
}

// Emit dispatches an event synchronously (Phase 16 light).
func (b *Bus) Emit(e Event) {
	if b == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	hs := append([]Handler{}, b.handlers[e.Name]...)
	all := append([]Handler{}, b.all...)
	b.mu.RUnlock()
	for _, h := range hs {
		h(e)
	}
	for _, h := range all {
		h(e)
	}
}
