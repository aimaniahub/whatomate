package prompt

import (
	"regexp"
	"time"

	"github.com/shridarpatil/whatomate/internal/chatbot/interactive"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Config for a prompt wait (Phase 11).
type Config struct {
	NodeID          string
	FlowID          string
	Body            string
	ValidationRegex string
	StoreAs         string
	MaxRetries      int
}

// SetWait stores a prompt WaitContract on the session.
func SetWait(sess *models.ChatbotSession, cfg Config, ttl time.Duration) {
	if sess == nil {
		return
	}
	if ttl <= 0 {
		ttl = interactive.DefaultTTL
	}
	exp := time.Now().Add(ttl)
	c := &interactive.Contract{
		Type:           interactive.TypePrompt,
		NodeID:         cfg.NodeID,
		FlowID:         cfg.FlowID,
		ExpiresAt:      &exp,
		FreeTextPolicy: interactive.FreeTextIgnore, // free text is the input
		InvalidPolicy:  interactive.InvalidReprompt,
	}
	sess.WaitContract = c.ToJSONB()
}

// Validate checks input against optional regex. Empty regex always passes.
func Validate(input, validationRegex string) bool {
	if validationRegex == "" {
		return true
	}
	re, err := regexp.Compile(validationRegex)
	if err != nil {
		return true // skip invalid regex rather than block user
	}
	return re.MatchString(input)
}
