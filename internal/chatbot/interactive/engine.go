package interactive

import (
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

// Engine applies wait-contract rules. Stateless; session carries the contract.
type Engine struct {
	// TitleMatch enables free-text → option resolution (flag interactive_title_match_v1).
	TitleMatch bool
	// ContractsEnabled enables persist/clear of WaitContract (flag wait_contract_v1).
	ContractsEnabled bool
	// DefaultWaitTTL used when creating contracts.
	DefaultWaitTTL time.Duration
}

// NewEngine constructs an interactive engine from feature flags.
func NewEngine(contractsEnabled, titleMatch bool) *Engine {
	return &Engine{
		TitleMatch:       titleMatch,
		ContractsEnabled: contractsEnabled,
		DefaultWaitTTL:   DefaultTTL,
	}
}

// ResolveButtonInput tries to produce a buttonID from click or typed title.
// Uses stored contract options when present; otherwise liveButtons.
func (e *Engine) ResolveButtonInput(
	sess *models.ChatbotSession,
	liveButtons []map[string]any,
	buttonID, text string,
) (resolvedID string, matched bool, source string) {
	if buttonID != "" && (e == nil || !e.TitleMatch) {
		// Always accept native button clicks even when title match is off.
		return buttonID, true, "button_id"
	}

	var opts []Option
	if e != nil && e.ContractsEnabled {
		if c := FromJSONB(sess.WaitContract); c != nil && len(c.Options) > 0 {
			if c.Expired(time.Now()) {
				return buttonID, buttonID != "", "expired"
			}
			opts = c.Options
			if buttonID == "" && !c.AllowsMatchOption() && e.TitleMatch {
				return "", false, ""
			}
		}
	}
	if len(opts) == 0 {
		opts = OptionsFromButtonMaps(liveButtons)
	}

	// Native click first
	if buttonID != "" {
		if m := ResolveInput(opts, buttonID, ""); m != nil {
			return m.OptionID, true, m.Source
		}
		return buttonID, true, "button_id"
	}

	if e == nil || !e.TitleMatch {
		return "", false, ""
	}
	m := ResolveInput(opts, "", text)
	if m == nil {
		return "", false, ""
	}
	return m.OptionID, true, m.Source
}

// SetButtonsWait stores a wait contract on the session after sending a menu.
func (e *Engine) SetButtonsWait(sess *models.ChatbotSession, nodeID, flowID, body string, buttons []map[string]any) {
	if e == nil || !e.ContractsEnabled || sess == nil {
		return
	}
	ttl := e.DefaultWaitTTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	// Prefer session expires_at remaining window if shorter.
	if sess.ExpiresAt != nil {
		rem := time.Until(*sess.ExpiresAt)
		if rem > 0 && rem < ttl {
			ttl = rem
		}
	}
	c := FromButtonMaps(nodeID, flowID, body, buttons, ttl)
	sess.WaitContract = c.ToJSONB()
}

// ClearWait removes the wait contract from the session (in-memory).
func (e *Engine) ClearWait(sess *models.ChatbotSession) {
	if sess == nil {
		return
	}
	sess.WaitContract = nil
}

// ClearWaitIfNode clears only when the contract is for the given node.
func (e *Engine) ClearWaitIfNode(sess *models.ChatbotSession, nodeID string) {
	if sess == nil {
		return
	}
	c := FromJSONB(sess.WaitContract)
	if c == nil || c.NodeID == nodeID || nodeID == "" {
		sess.WaitContract = nil
	}
}

// HasActiveButtonsWait reports a non-expired buttons/list wait on the session.
func (e *Engine) HasActiveButtonsWait(sess *models.ChatbotSession) bool {
	if e == nil || !e.ContractsEnabled || sess == nil {
		return false
	}
	c := FromJSONB(sess.WaitContract)
	if c == nil {
		return false
	}
	if c.Type != TypeButtons && c.Type != TypeList {
		return false
	}
	return !c.Expired(time.Now())
}

// ActiveContract returns the session wait contract or nil.
func (e *Engine) ActiveContract(sess *models.ChatbotSession) *Contract {
	if sess == nil {
		return nil
	}
	return FromJSONB(sess.WaitContract)
}
