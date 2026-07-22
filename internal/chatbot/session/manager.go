package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// DefaultTimeoutMins is used when timeoutMins <= 0.
const DefaultTimeoutMins = 30

// Completion reasons (audit / analytics).
const (
	ReasonUserComplete   = "completed"
	ReasonExitFlow       = "exit_flow"
	ReasonTransfer       = "transfer"
	ReasonCancel         = "cancel"
	ReasonExpired        = "expired"
	ReasonSuperseded     = "superseded"
	ReasonAdminForce     = "admin"
	ReasonTimeoutLegacy  = "timeout"
)

// ErrVersionConflict is returned when Save detects a concurrent update.
var ErrVersionConflict = errors.New("session: version conflict")

// Key uniquely identifies at most one open session.
type Key struct {
	OrganizationID  uuid.UUID
	WhatsAppAccount string
	ContactID       uuid.UUID
}

// Manager is the session aggregate service.
type Manager struct {
	DB *gorm.DB
	// Now is overridable for tests; defaults to time.Now.
	Now func() time.Time
}

// NewManager creates a Manager. db must be non-nil.
func NewManager(db *gorm.DB) *Manager {
	return &Manager{
		DB:  db,
		Now: time.Now,
	}
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// GetOrCreateActive loads the single open non-expired session for the key or creates one.
// Duplicate open rows are closed as superseded (keeps latest last_activity_at).
// Returns (session, created, error).
func (m *Manager) GetOrCreateActive(
	ctx context.Context,
	key Key,
	phoneNumber string,
	timeoutMins int,
) (*models.ChatbotSession, bool, error) {
	if m == nil || m.DB == nil {
		return nil, false, errors.New("session manager: nil db")
	}
	if timeoutMins <= 0 {
		timeoutMins = DefaultTimeoutMins
	}
	now := m.now()
	timeoutDur := time.Duration(timeoutMins) * time.Minute
	activityFloor := now.Add(-timeoutDur)

	// Load all open (active) sessions for this key, newest activity first.
	var opens []models.ChatbotSession
	err := m.DB.WithContext(ctx).
		Where("organization_id = ? AND contact_id = ? AND whats_app_account = ? AND status = ?",
			key.OrganizationID, key.ContactID, key.WhatsAppAccount, models.SessionStatusActive).
		Order("last_activity_at DESC").
		Find(&opens).Error
	if err != nil {
		return nil, false, fmt.Errorf("session list open: %w", err)
	}

	var chosen *models.ChatbotSession
	for i := range opens {
		s := &opens[i]
		if sessionStillOpen(s, now, activityFloor) {
			if chosen == nil {
				chosen = s
			} else {
				// Duplicate open — supersede older ones.
				_ = m.completeInternal(ctx, s, models.SessionStatusSuperseded, ReasonSuperseded, now)
			}
		} else if s.Status == models.SessionStatusActive {
			// Past expiry but still marked active — mark expired now.
			_ = m.completeInternal(ctx, s, models.SessionStatusExpired, ReasonExpired, now)
		}
	}

	if chosen != nil {
		expires := now.Add(timeoutDur)
		chosen.LastActivityAt = now
		chosen.ExpiresAt = &expires
		if chosen.Version < 1 {
			chosen.Version = 1
		}
		// Touch without full optimistic bump for activity-only (version still increments once).
		if err := m.touchActivity(ctx, chosen, now, expires); err != nil {
			return nil, false, err
		}
		return chosen, false, nil
	}

	// Create new session.
	expires := now.Add(timeoutDur)
	session := &models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  key.OrganizationID,
		ContactID:       key.ContactID,
		WhatsAppAccount: key.WhatsAppAccount,
		PhoneNumber:     phoneNumber,
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       now,
		LastActivityAt:  now,
		Version:         1,
		TurnSeq:         0,
		ExpiresAt:       &expires,
	}
	if err := m.DB.WithContext(ctx).Create(session).Error; err != nil {
		return nil, false, fmt.Errorf("session create: %w", err)
	}
	return session, true, nil
}

// sessionStillOpen reports whether an active row is still within timeout.
// Prefers expires_at when set; otherwise last_activity_at > activityFloor (legacy).
func sessionStillOpen(s *models.ChatbotSession, now, activityFloor time.Time) bool {
	if s == nil || s.Status != models.SessionStatusActive {
		return false
	}
	if s.ExpiresAt != nil {
		return s.ExpiresAt.After(now)
	}
	return s.LastActivityAt.After(activityFloor)
}

func (m *Manager) touchActivity(ctx context.Context, s *models.ChatbotSession, now, expires time.Time) error {
	expected := s.Version
	if expected < 1 {
		expected = 1
	}
	res := m.DB.WithContext(ctx).Model(&models.ChatbotSession{}).
		Where("id = ? AND version = ?", s.ID, expected).
		Updates(map[string]any{
			"last_activity_at": now,
			"expires_at":       expires,
			"version":          expected + 1,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Conflict or missing — reload best-effort and still return session for this turn.
		var fresh models.ChatbotSession
		if err := m.DB.WithContext(ctx).Where("id = ?", s.ID).First(&fresh).Error; err == nil {
			*s = fresh
			s.LastActivityAt = now
			s.ExpiresAt = &expires
		}
		return nil
	}
	s.Version = expected + 1
	s.LastActivityAt = now
	s.ExpiresAt = &expires
	return nil
}

// Save persists session state with optimistic concurrency on Version.
// Increments Version. TurnSeq is left to the caller (see BumpTurn).
// Updates last_activity_at and extends expires_at by timeoutMins when timeoutMins > 0.
func (m *Manager) Save(ctx context.Context, s *models.ChatbotSession, timeoutMins int) error {
	if m == nil || m.DB == nil || s == nil {
		return errors.New("session save: nil")
	}
	now := m.now()
	s.LastActivityAt = now
	if timeoutMins > 0 {
		exp := now.Add(time.Duration(timeoutMins) * time.Minute)
		s.ExpiresAt = &exp
	}
	if s.Status == models.SessionStatusCompleted ||
		s.Status == models.SessionStatusExpired ||
		s.Status == models.SessionStatusCancelled ||
		s.Status == models.SessionStatusSuperseded ||
		s.Status == models.SessionStatusTimeout {
		if s.CompletedAt == nil {
			s.CompletedAt = &now
		}
	}

	expected := s.Version
	if expected < 1 {
		expected = 1
	}
	next := expected + 1
	s.Version = next

	res := m.DB.WithContext(ctx).
		Where("id = ? AND version = ?", s.ID, expected).
		Save(s)
	if res.Error != nil {
		s.Version = expected // restore on failure
		return res.Error
	}
	if res.RowsAffected == 0 {
		s.Version = expected
		return ErrVersionConflict
	}
	return nil
}

// BumpTurn increments turn_seq in memory (call Save afterward).
func BumpTurn(s *models.ChatbotSession) {
	if s == nil {
		return
	}
	s.TurnSeq++
}

// Complete marks the session closed with the given status and reason.
func (m *Manager) Complete(ctx context.Context, s *models.ChatbotSession, status models.SessionStatus, reason string) error {
	if s == nil {
		return errors.New("session complete: nil session")
	}
	now := m.now()
	return m.completeInternal(ctx, s, status, reason, now)
}

func (m *Manager) completeInternal(
	ctx context.Context,
	s *models.ChatbotSession,
	status models.SessionStatus,
	reason string,
	now time.Time,
) error {
	if status == "" {
		status = models.SessionStatusCompleted
	}
	expected := s.Version
	if expected < 1 {
		expected = 1
	}
	next := expected + 1

	updates := map[string]any{
		"status":            status,
		"completed_at":      now,
		"completion_reason": reason,
		"current_flow_id":   nil,
		"current_step":      "",
		"step_retries":      0,
		"wait_contract":     nil,
		"version":           next,
		"updated_at":        now,
	}
	res := m.DB.WithContext(ctx).Model(&models.ChatbotSession{}).
		Where("id = ? AND version = ?", s.ID, expected).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	// If version conflict, force status update without version guard (terminal is ok).
	if res.RowsAffected == 0 {
		_ = m.DB.WithContext(ctx).Model(&models.ChatbotSession{}).
			Where("id = ?", s.ID).
			Updates(map[string]any{
				"status":            status,
				"completed_at":      now,
				"completion_reason": reason,
				"current_flow_id":   nil,
				"current_step":      "",
				"step_retries":      0,
				"wait_contract":     nil,
				"updated_at":        now,
			}).Error
	}

	s.Status = status
	s.CompletedAt = &now
	s.CompletionReason = reason
	s.CurrentFlowID = nil
	s.CurrentStep = ""
	s.StepRetries = 0
	s.WaitContract = nil
	if res.RowsAffected > 0 {
		s.Version = next
	}
	return nil
}

// ExitFlow completes the session and clears flow cursor (cancel / unloadable graph).
func (m *Manager) ExitFlow(ctx context.Context, s *models.ChatbotSession) error {
	return m.Complete(ctx, s, models.SessionStatusCompleted, ReasonExitFlow)
}

// ExpireStale marks open sessions past expires_at (or legacy activity floor) as expired.
// timeoutMins is the default timeout applied when expires_at is null.
// Returns the number of rows expired.
func (m *Manager) ExpireStale(ctx context.Context, timeoutMins int, limit int) (int, error) {
	if m == nil || m.DB == nil {
		return 0, errors.New("session expire: nil db")
	}
	if timeoutMins <= 0 {
		timeoutMins = DefaultTimeoutMins
	}
	if limit <= 0 {
		limit = 500
	}
	now := m.now()
	activityFloor := now.Add(-time.Duration(timeoutMins) * time.Minute)

	// Prefer explicit expires_at; also catch legacy null expires_at via last_activity_at.
	var ids []uuid.UUID
	err := m.DB.WithContext(ctx).Model(&models.ChatbotSession{}).
		Select("id").
		Where("status = ?", models.SessionStatusActive).
		Where("(expires_at IS NOT NULL AND expires_at <= ?) OR (expires_at IS NULL AND last_activity_at <= ?)",
			now, activityFloor).
		Order("last_activity_at ASC").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	res := m.DB.WithContext(ctx).Model(&models.ChatbotSession{}).
		Where("id IN ? AND status = ?", ids, models.SessionStatusActive).
		Updates(map[string]any{
			"status":            models.SessionStatusExpired,
			"completed_at":      now,
			"completion_reason": ReasonExpired,
			"current_flow_id":   nil,
			"current_step":      "",
			"step_retries":      0,
			"wait_contract":     nil,
			"updated_at":        now,
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}

// CloseDuplicateOpens finds keys with multiple active sessions and supersedes all but the newest.
// Used for one-shot cleanup / sweeper assist. Returns number of superseded rows.
func (m *Manager) CloseDuplicateOpens(ctx context.Context, limit int) (int, error) {
	if m == nil || m.DB == nil {
		return 0, errors.New("session dedupe: nil db")
	}
	if limit <= 0 {
		limit = 200
	}
	now := m.now()

	// Postgres: find contact keys with count > 1.
	type dupKey struct {
		OrganizationID  uuid.UUID
		ContactID       uuid.UUID
		WhatsAppAccount string
		Cnt             int
	}
	var dups []dupKey
	err := m.DB.WithContext(ctx).Raw(`
		SELECT organization_id, contact_id, whats_app_account, COUNT(*) AS cnt
		FROM chatbot_sessions
		WHERE status = ? AND deleted_at IS NULL
		GROUP BY organization_id, contact_id, whats_app_account
		HAVING COUNT(*) > 1
		LIMIT ?
	`, models.SessionStatusActive, limit).Scan(&dups).Error
	if err != nil {
		return 0, err
	}

	superseded := 0
	for _, d := range dups {
		var opens []models.ChatbotSession
		if err := m.DB.WithContext(ctx).
			Where("organization_id = ? AND contact_id = ? AND whats_app_account = ? AND status = ?",
				d.OrganizationID, d.ContactID, d.WhatsAppAccount, models.SessionStatusActive).
			Order("last_activity_at DESC").
			Find(&opens).Error; err != nil {
			continue
		}
		for i := 1; i < len(opens); i++ {
			if err := m.completeInternal(ctx, &opens[i], models.SessionStatusSuperseded, ReasonSuperseded, now); err == nil {
				superseded++
			}
		}
	}
	return superseded, nil
}
