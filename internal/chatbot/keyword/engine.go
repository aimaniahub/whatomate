package keyword

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Match is a successful keyword rule match.
type Match struct {
	RuleID       uuid.UUID
	RuleName     string
	ResponseType models.ResponseType
	Body         string
	Buttons      []map[string]any
	// FlowID is set when response_type=flow and response_content has flow_id.
	FlowID *uuid.UUID
	Priority int
}

// Engine matches inbound text against keyword rules (Phase 8).
type Engine struct{}

// NewEngine creates a keyword engine.
func NewEngine() *Engine { return &Engine{} }

// MatchRules returns the first matching enabled rule (rules must be priority-sorted).
// Enforces ActiveFrom / ActiveUntil when set.
func (e *Engine) MatchRules(rules []models.KeywordRule, messageText string, now time.Time) (*Match, bool) {
	if e == nil {
		e = &Engine{}
	}
	messageLower := strings.ToLower(messageText)

	for _, rule := range rules {
		if !rule.IsEnabled {
			continue
		}
		if rule.ActiveFrom != nil && now.Before(*rule.ActiveFrom) {
			continue
		}
		if rule.ActiveUntil != nil && now.After(*rule.ActiveUntil) {
			continue
		}

		for _, keyword := range rule.Keywords {
			if !keywordMatches(rule, messageText, messageLower, keyword) {
				continue
			}
			m := &Match{
				RuleID:       rule.ID,
				RuleName:     rule.Name,
				ResponseType: rule.ResponseType,
				Body:         responseBody(rule.ResponseContent),
				Priority:     rule.Priority,
			}
			if rule.ResponseType == models.ResponseTypeTransfer {
				return m, true
			}
			// Extract flow_id for flow responses
			if rule.ResponseType == models.ResponseTypeFlow && rule.ResponseContent != nil {
				if fid, ok := rule.ResponseContent["flow_id"].(string); ok && fid != "" {
					if parsed, err := uuid.Parse(fid); err == nil {
						m.FlowID = &parsed
					}
				}
			}
			if buttons, ok := rule.ResponseContent["buttons"].([]any); ok && len(buttons) > 0 {
				m.Buttons = make([]map[string]any, 0, len(buttons))
				for _, btn := range buttons {
					if btnMap, ok := btn.(map[string]any); ok {
						m.Buttons = append(m.Buttons, btnMap)
					}
				}
			}
			switch rule.ResponseType {
			case models.ResponseTypeText, models.ResponseTypeTemplate, models.ResponseTypeMedia,
				models.ResponseTypeScript, models.ResponseTypeFlow, "":
				if m.Body != "" || len(m.Buttons) > 0 || m.FlowID != nil {
					if m.Body == "" && len(m.Buttons) > 0 {
						m.Body = " "
					}
					return m, true
				}
			default:
				if m.Body != "" {
					return m, true
				}
			}
		}
	}
	return nil, false
}

func keywordMatches(rule models.KeywordRule, messageText, messageLower, keyword string) bool {
	keywordLower := strings.ToLower(keyword)
	switch rule.MatchType {
	case models.MatchTypeExact:
		if rule.CaseSensitive {
			return messageText == keyword
		}
		return messageLower == keywordLower
	case models.MatchTypeContains:
		if rule.CaseSensitive {
			return strings.Contains(messageText, keyword)
		}
		return strings.Contains(messageLower, keywordLower)
	case models.MatchTypeStartsWith:
		if rule.CaseSensitive {
			return strings.HasPrefix(messageText, keyword)
		}
		return strings.HasPrefix(messageLower, keywordLower)
	case models.MatchTypeRegex:
		re, err := regexp.Compile(keyword)
		if err != nil {
			return false
		}
		return re.MatchString(messageText)
	default:
		return strings.Contains(messageLower, keywordLower)
	}
}

func responseBody(content models.JSONB) string {
	if content == nil {
		return ""
	}
	if body, ok := content["body"].(string); ok && strings.TrimSpace(body) != "" {
		return body
	}
	if text, ok := content["text"].(string); ok {
		return text
	}
	return ""
}
