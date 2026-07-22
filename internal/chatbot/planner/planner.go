package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// MessageKind classifies an outbound unit in a turn plan.
type MessageKind string

const (
	KindText        MessageKind = "text"
	KindInteractive MessageKind = "interactive"
	KindMedia       MessageKind = "media"
	KindOther       MessageKind = "other"
)

// PlannedMessage is one outbound message in a turn.
type PlannedMessage struct {
	Kind        MessageKind
	Body        string
	Fingerprint string // empty = no dedupe
	// Payload is opaque for the sender (e.g. buttons maps) — not used by planner itself.
	Meta map[string]any
}

// Plan accumulates outbound messages for a single inbound turn.
// Thread-safe for concurrent append within one turn is not required; one turn one goroutine.
type Plan struct {
	mu       sync.Mutex
	messages []PlannedMessage
	seenFP   map[string]struct{}
	// MaxMessages caps outbound count per turn (0 = unlimited).
	MaxMessages int
}

// NewPlan creates an empty turn plan.
func NewPlan() *Plan {
	return &Plan{
		seenFP:      make(map[string]struct{}),
		MaxMessages: 8,
	}
}

// FingerprintText builds a stable fingerprint for text/menu content.
func FingerprintText(kind MessageKind, body string, extra ...string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(body)))
	for _, e := range extra {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(e))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Add queues a message. Returns false if suppressed as duplicate fingerprint or over cap.
func (p *Plan) Add(msg PlannedMessage) bool {
	if p == nil {
		return true // no planner → allow send
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.MaxMessages > 0 && len(p.messages) >= p.MaxMessages {
		return false
	}
	if msg.Fingerprint != "" {
		if _, ok := p.seenFP[msg.Fingerprint]; ok {
			return false
		}
		p.seenFP[msg.Fingerprint] = struct{}{}
	}
	p.messages = append(p.messages, msg)
	return true
}

// ShouldSend is a convenience for fire-and-forget send paths: records fingerprint and
// returns whether the caller should actually send.
func (p *Plan) ShouldSend(kind MessageKind, body string, extra ...string) bool {
	if p == nil {
		return true
	}
	fp := FingerprintText(kind, body, extra...)
	return p.Add(PlannedMessage{Kind: kind, Body: body, Fingerprint: fp})
}

// Messages returns a copy of planned messages in order.
func (p *Plan) Messages() []PlannedMessage {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PlannedMessage, len(p.messages))
	copy(out, p.messages)
	return out
}

// Len returns planned message count.
func (p *Plan) Len() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

// Engine is the Phase 7 response planner. Create a Plan per turn via BeginTurn.
type Engine struct {
	Enabled bool
}

// NewEngine constructs a planner; enabled when response_planner_v1 is on.
func NewEngine(enabled bool) *Engine {
	return &Engine{Enabled: enabled}
}

// BeginTurn returns a plan when enabled, or nil (pass-through) when disabled.
func (e *Engine) BeginTurn() *Plan {
	if e == nil || !e.Enabled {
		return nil
	}
	return NewPlan()
}
