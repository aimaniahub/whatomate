package interactive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

// Wait types stored in WaitContract.Type.
const (
	TypeButtons      = "buttons"
	TypeList         = "list"
	TypePrompt       = "prompt"
	TypeWhatsAppFlow = "whatsapp_flow"
	TypeQuickReply   = "quick_reply"
)

// Free-text policies while a wait is active.
const (
	// FreeTextMatchOption tries to map typed text to an option (title/alias).
	FreeTextMatchOption = "match_option"
	// FreeTextBreakout allows keyword/AI/flow-trigger handling outside the graph.
	FreeTextBreakout = "free_text_breakout"
	// FreeTextIgnore ignores free text (reprompt only) — reserved.
	FreeTextIgnore = "ignore"
	// FreeTextAIAssist answers via AI then returns to menu — reserved.
	FreeTextAIAssist = "ai_assist"
)

// Invalid policies when input does not match.
const (
	InvalidReprompt  = "reprompt"
	InvalidBreakout  = "breakout"
	InvalidFallback  = "fallback_edge"
)

// Option is one selectable choice on an interactive wait.
type Option struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Aliases []string `json:"aliases,omitempty"`
}

// Contract is the first-class wait state persisted on chatbot_sessions.wait_contract.
type Contract struct {
	Type       string    `json:"type"`
	NodeID     string    `json:"node_id"`
	FlowID     string    `json:"flow_id,omitempty"`
	Options    []Option  `json:"options,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// FreeTextPolicy: match_option | free_text_breakout | ignore | ai_assist
	FreeTextPolicy string `json:"free_text_policy,omitempty"`
	// InvalidPolicy: reprompt | breakout | fallback_edge
	InvalidPolicy string `json:"invalid_policy,omitempty"`
	// RenderFingerprint hashes last sent menu body+options to detect duplicate sends.
	RenderFingerprint string `json:"render_fingerprint,omitempty"`
	// ConsumedInputHash blocks duplicate click processing.
	ConsumedInputHash string `json:"consumed_input_hash,omitempty"`
}

// DefaultTTL for interactive waits when not specified.
const DefaultTTL = 30 * time.Minute

// FromButtonMaps builds a buttons wait contract from node button maps.
func FromButtonMaps(nodeID, flowID, body string, buttons []map[string]any, ttl time.Duration) *Contract {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	exp := time.Now().Add(ttl)
	opts := make([]Option, 0, len(buttons))
	for _, b := range buttons {
		id, _ := b["id"].(string)
		title, _ := b["title"].(string)
		if id == "" && title == "" {
			continue
		}
		if id == "" {
			id = title
		}
		opts = append(opts, Option{ID: id, Title: title})
	}
	c := &Contract{
		Type:           TypeButtons,
		NodeID:         nodeID,
		FlowID:         flowID,
		Options:        opts,
		ExpiresAt:      &exp,
		FreeTextPolicy: FreeTextMatchOption + "," + FreeTextBreakout, // match first, then breakout
		InvalidPolicy:  InvalidReprompt,
	}
	c.RenderFingerprint = Fingerprint(body, opts)
	return c
}

// Fingerprint hashes body + option ids/titles for dedupe.
func Fingerprint(body string, opts []Option) string {
	h := sha256.New()
	_, _ = h.Write([]byte(body))
	for _, o := range opts {
		_, _ = h.Write([]byte(o.ID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(o.Title))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ToJSONB serializes the contract for session.WaitContract.
func (c *Contract) ToJSONB() models.JSONB {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var m models.JSONB
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// FromJSONB loads a contract from session.WaitContract.
func FromJSONB(j models.JSONB) *Contract {
	if j == nil || len(j) == 0 {
		return nil
	}
	b, err := json.Marshal(j)
	if err != nil {
		return nil
	}
	var c Contract
	if err := json.Unmarshal(b, &c); err != nil {
		return nil
	}
	if c.Type == "" && c.NodeID == "" {
		return nil
	}
	return &c
}

// Expired reports whether the contract is past ExpiresAt.
func (c *Contract) Expired(now time.Time) bool {
	if c == nil || c.ExpiresAt == nil {
		return false
	}
	return !c.ExpiresAt.After(now)
}

// AllowsMatchOption is true when free-text may resolve to an option.
func (c *Contract) AllowsMatchOption() bool {
	if c == nil {
		return true // default when matching against live node config
	}
	p := c.FreeTextPolicy
	if p == "" {
		return true
	}
	return strings.Contains(p, FreeTextMatchOption) || p == FreeTextMatchOption
}

// AllowsBreakout is true when free-text may leave the graph via keyword/AI.
func (c *Contract) AllowsBreakout() bool {
	if c == nil {
		return true // legacy default
	}
	p := c.FreeTextPolicy
	if p == "" {
		return true
	}
	return strings.Contains(p, FreeTextBreakout) || p == FreeTextBreakout || p == FreeTextAIAssist
}

// MarkConsumed records a resolved input hash.
func (c *Contract) MarkConsumed(inputKey string) {
	if c == nil {
		return
	}
	sum := sha256.Sum256([]byte(inputKey))
	c.ConsumedInputHash = hex.EncodeToString(sum[:])[:16]
}

// AlreadyConsumed reports duplicate resolution of the same input.
func (c *Contract) AlreadyConsumed(inputKey string) bool {
	if c == nil || c.ConsumedInputHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(inputKey))
	return c.ConsumedInputHash == hex.EncodeToString(sum[:])[:16]
}
