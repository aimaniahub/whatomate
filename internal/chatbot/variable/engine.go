package variable

import (
	"fmt"
	"strings"

	"github.com/shridarpatil/whatomate/internal/models"
)

// Scopes (Phase 12). Flat session_data remains the store; keys may be prefixed.
const (
	ScopeSystem       = "system"
	ScopeContact      = "contact"
	ScopeConversation = "conversation"
	ScopeSession      = "session"
	ScopeFlow         = "flow"
	ScopeTemp         = "temp"
)

// Engine provides scoped get/set over session SessionData (and optional maps).
type Engine struct{}

// NewEngine creates a variable engine.
func NewEngine() *Engine { return &Engine{} }

// Get resolves a key from session data using scope search:
// temp → flow → session → conversation → contact → system → bare key.
func (e *Engine) Get(data models.JSONB, key string) (any, bool) {
	if data == nil || key == "" {
		return nil, false
	}
	if v, ok := data[key]; ok {
		return v, true
	}
	for _, scope := range []string{ScopeTemp, ScopeFlow, ScopeSession, ScopeConversation, ScopeContact, ScopeSystem} {
		sk := scope + "." + key
		if v, ok := data[sk]; ok {
			return v, true
		}
	}
	return nil, false
}

// Set writes key into the given scope (default session).
func (e *Engine) Set(data models.JSONB, scope, key string, value any) models.JSONB {
	if data == nil {
		data = models.JSONB{}
	}
	if key == "" {
		return data
	}
	if scope == "" {
		scope = ScopeSession
	}
	if scope == ScopeSession || !strings.Contains(key, ".") {
		// Prefer scoped key for non-session; bare for session compatibility.
		if scope == ScopeSession {
			data[key] = value
			return data
		}
	}
	data[scope+"."+key] = value
	// Also write bare key for template {{key}} compatibility on flow/session.
	if scope == ScopeFlow || scope == ScopeSession {
		data[key] = value
	}
	return data
}

// ClearScope removes keys with the given scope prefix (and optional bare flow keys tracked separately — best effort).
func (e *Engine) ClearScope(data models.JSONB, scope string) models.JSONB {
	if data == nil || scope == "" {
		return data
	}
	prefix := scope + "."
	for k := range data {
		if strings.HasPrefix(k, prefix) {
			delete(data, k)
		}
	}
	return data
}

// ClearFlowScope clears flow-scoped variables when a flow exits.
func (e *Engine) ClearFlowScope(data models.JSONB) models.JSONB {
	return e.ClearScope(data, ScopeFlow)
}

// SeedSystem writes built-in system variables.
func (e *Engine) SeedSystem(data models.JSONB, phone, contactName, account string) models.JSONB {
	if data == nil {
		data = models.JSONB{}
	}
	data["phone_number"] = phone
	data["whatsapp_phone"] = phone
	data["contact_name"] = contactName
	data["whatsapp_name"] = contactName
	data["whatsapp_account"] = account
	return data
}

// String returns string form of a value.
func String(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
