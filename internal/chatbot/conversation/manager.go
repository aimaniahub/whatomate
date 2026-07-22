package conversation

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/chatbot/session"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Manager is a thin conversation policy façade over the session manager.
// It does not own a separate conversations table (deferred); identity remains
// (org, account, contact) via sessions.
type Manager struct {
	Sessions *session.Manager
	// ExtraRestartKeywords are optional org/product overrides (may be nil).
	ExtraRestartKeywords []string
	Now                  func() time.Time
}

// NewManager binds conversation policies to a session manager (may be nil for pure policy tests).
func NewManager(sessions *session.Manager) *Manager {
	return &Manager{
		Sessions: sessions,
		Now:      time.Now,
	}
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// PlanGreeting returns the greeting plan for this turn.
func (m *Manager) PlanGreeting(isNewSession bool, sess *models.ChatbotSession, defaultResponse string, isSimpleGreeting bool) GreetingPlan {
	return ShouldGreet(isNewSession, sess, defaultResponse).WithPreAnswer(isSimpleGreeting)
}

// TryRestartIfRequested completes the current free-chat session and opens a new one
// when the user message is a restart intent. Returns the (possibly new) session,
// whether a restart occurred, and whether the new session is "new" for greeting.
func (m *Manager) TryRestartIfRequested(
	ctx context.Context,
	sess *models.ChatbotSession,
	key session.Key,
	phoneNumber string,
	timeoutMins int,
	messageText string,
) (out *models.ChatbotSession, restarted bool, isNew bool, err error) {
	if sess == nil {
		return nil, false, false, fmt.Errorf("conversation restart: nil session")
	}
	if !CanRestartInFreeChat(sess) {
		return sess, false, false, nil
	}
	extra := []string(nil)
	if m != nil {
		extra = m.ExtraRestartKeywords
	}
	if !IsRestartIntent(messageText, extra) {
		return sess, false, false, nil
	}
	if m == nil || m.Sessions == nil {
		return sess, false, false, fmt.Errorf("conversation restart: session manager required")
	}

	if err := m.Sessions.Complete(ctx, sess, models.SessionStatusCompleted, "restart"); err != nil {
		return sess, false, false, fmt.Errorf("conversation restart complete: %w", err)
	}

	fresh, created, err := m.Sessions.GetOrCreateActive(ctx, key, phoneNumber, timeoutMins)
	if err != nil {
		return sess, false, false, fmt.Errorf("conversation restart create: %w", err)
	}
	MarkRestarted(fresh, strconv.FormatInt(m.now().Unix(), 10))
	// Persist restart stamp lightly (best-effort).
	_ = m.Sessions.Save(ctx, fresh, timeoutMins)
	return fresh, true, created, nil
}

// PersistGreetingSent marks greeting sent and saves via session manager when available.
func (m *Manager) PersistGreetingSent(ctx context.Context, sess *models.ChatbotSession, timeoutMins int) error {
	MarkGreetingSent(sess)
	if m == nil || m.Sessions == nil || sess == nil {
		return nil
	}
	return m.Sessions.Save(ctx, sess, timeoutMins)
}

// SessionKey builds the session key for conversation operations.
func SessionKey(orgID, contactID uuid.UUID, account string) session.Key {
	return session.Key{
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: account,
	}
}
