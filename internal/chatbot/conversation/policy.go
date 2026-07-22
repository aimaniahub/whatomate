package conversation

import (
	"regexp"
	"strings"

	"github.com/shridarpatil/whatomate/internal/models"
)

// Session data keys written by conversation policies.
const (
	KeyGreetingSent = "_greeting_sent"
	KeyRestartedAt  = "_restarted_at"
)

// DefaultRestartKeywords trigger a clean session restart in free chat.
// Intentionally does NOT include bare "start" / "hi" (those are greetings / flow triggers).
var DefaultRestartKeywords = []string{
	"restart",
	"start over",
	"reset chat",
	"reset bot",
	"new conversation",
}

// GreetingPlan is the decision for the new-session greeting branch.
type GreetingPlan struct {
	// ShouldGreet means send DefaultResponse / greeting menu this turn.
	ShouldGreet bool
	// AllowPreAnswer means the user message is not a simple greeting, so
	// keyword/AI may answer before the menu (matches legacy product behavior).
	AllowPreAnswer bool
}

// ShouldGreet decides whether this turn should run the greeting branch.
//
// Product rules (preserved from legacy processor):
//  1. Only brand-new sessions (isNewSession).
//  2. Settings must define a non-empty DefaultResponse.
//  3. Session must not already have _greeting_sent (double-send guard).
func ShouldGreet(isNewSession bool, sess *models.ChatbotSession, defaultResponse string) GreetingPlan {
	plan := GreetingPlan{}
	if !isNewSession {
		return plan
	}
	if strings.TrimSpace(defaultResponse) == "" {
		return plan
	}
	if GreetingAlreadySent(sess) {
		return plan
	}
	plan.ShouldGreet = true
	return plan
}

// WithPreAnswer sets AllowPreAnswer when the inbound text is not a simple greeting.
// isSimple is provided by the caller (handlers.isSimpleGreeting) to avoid duplicating
// product-specific greeting lists in this package.
func (p GreetingPlan) WithPreAnswer(isSimpleGreeting bool) GreetingPlan {
	if p.ShouldGreet && !isSimpleGreeting {
		p.AllowPreAnswer = true
	}
	return p
}

// GreetingAlreadySent reports session_data[_greeting_sent] == true.
func GreetingAlreadySent(sess *models.ChatbotSession) bool {
	if sess == nil || sess.SessionData == nil {
		return false
	}
	v, ok := sess.SessionData[KeyGreetingSent]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true") || t == "1"
	default:
		return false
	}
}

// MarkGreetingSent sets _greeting_sent on the in-memory session (caller persists).
func MarkGreetingSent(sess *models.ChatbotSession) {
	if sess == nil {
		return
	}
	if sess.SessionData == nil {
		sess.SessionData = models.JSONB{}
	}
	sess.SessionData[KeyGreetingSent] = true
}

// IsRestartIntent reports whether free-text asks to restart the conversation.
// Matching is case-insensitive; multi-word keywords use contains; single tokens
// require equality or whole-phrase equality after trim (not bare substring of longer words).
func IsRestartIntent(message string, extraKeywords []string) bool {
	msg := strings.TrimSpace(strings.ToLower(message))
	if msg == "" {
		return false
	}
	keywords := append([]string{}, DefaultRestartKeywords...)
	for _, k := range extraKeywords {
		k = strings.TrimSpace(k)
		if k != "" {
			keywords = append(keywords, k)
		}
	}
	for _, kw := range keywords {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw == "" {
			continue
		}
		if strings.Contains(kw, " ") {
			if strings.Contains(msg, kw) {
				return true
			}
			continue
		}
		// Single token: exact message or whole-word match (not substring of "restarted").
		if msg == kw {
			return true
		}
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
		if err == nil && re.MatchString(msg) {
			return true
		}
	}
	return false
}

// CanRestartInFreeChat is true when there is no active flow (free-chat ownership).
func CanRestartInFreeChat(sess *models.ChatbotSession) bool {
	if sess == nil {
		return false
	}
	return sess.CurrentFlowID == nil && sess.Status == models.SessionStatusActive
}

// MarkRestarted stamps session_data for audit (usually on the new session).
func MarkRestarted(sess *models.ChatbotSession, atUnix string) {
	if sess == nil {
		return
	}
	if sess.SessionData == nil {
		sess.SessionData = models.JSONB{}
	}
	sess.SessionData[KeyRestartedAt] = atUnix
}
