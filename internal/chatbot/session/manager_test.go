package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/chatbot/session"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMgr(t *testing.T) (*session.Manager, uuid.UUID, uuid.UUID) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	return session.NewManager(db), org.ID, contact.ID
}

func TestGetOrCreate_CreatesThenReuses(t *testing.T) {
	mgr, orgID, contactID := setupMgr(t)
	ctx := context.Background()
	key := session.Key{OrganizationID: orgID, ContactID: contactID, WhatsAppAccount: "main"}

	s1, created, err := mgr.GetOrCreateActive(ctx, key, "15551212", 30)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 1, s1.Version)
	require.NotNil(t, s1.ExpiresAt)

	s2, created2, err := mgr.GetOrCreateActive(ctx, key, "15551212", 30)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, s1.ID, s2.ID)
	assert.GreaterOrEqual(t, s2.Version, 2) // touch bumped version
}

func TestGetOrCreate_SupersedesDuplicates(t *testing.T) {
	mgr, orgID, contactID := setupMgr(t)
	ctx := context.Background()
	now := time.Now()

	// Two active rows (legacy race simulation)
	older := models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "main",
		PhoneNumber:     "1",
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       now.Add(-time.Hour),
		LastActivityAt:  now.Add(-time.Minute * 10),
		Version:         1,
	}
	newer := models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "main",
		PhoneNumber:     "1",
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       now.Add(-time.Minute),
		LastActivityAt:  now.Add(-time.Second),
		Version:         1,
	}
	require.NoError(t, mgr.DB.Create(&older).Error)
	require.NoError(t, mgr.DB.Create(&newer).Error)

	key := session.Key{OrganizationID: orgID, ContactID: contactID, WhatsAppAccount: "main"}
	s, created, err := mgr.GetOrCreateActive(ctx, key, "1", 30)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, newer.ID, s.ID)

	var old models.ChatbotSession
	require.NoError(t, mgr.DB.First(&old, older.ID).Error)
	assert.Equal(t, models.SessionStatusSuperseded, old.Status)
}

func TestGetOrCreate_ExpiredNotResumed(t *testing.T) {
	mgr, orgID, contactID := setupMgr(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	exp := past

	old := models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "main",
		PhoneNumber:     "1",
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       past,
		LastActivityAt:  past,
		ExpiresAt:       &exp,
		Version:         1,
	}
	require.NoError(t, mgr.DB.Create(&old).Error)

	key := session.Key{OrganizationID: orgID, ContactID: contactID, WhatsAppAccount: "main"}
	s, created, err := mgr.GetOrCreateActive(ctx, key, "1", 30)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, old.ID, s.ID)

	var expired models.ChatbotSession
	require.NoError(t, mgr.DB.First(&expired, old.ID).Error)
	assert.Equal(t, models.SessionStatusExpired, expired.Status)
}

func TestSave_VersionConflict(t *testing.T) {
	mgr, orgID, contactID := setupMgr(t)
	ctx := context.Background()
	key := session.Key{OrganizationID: orgID, ContactID: contactID, WhatsAppAccount: "main"}
	s, _, err := mgr.GetOrCreateActive(ctx, key, "1", 30)
	require.NoError(t, err)

	// Concurrent writer bumps version
	s2 := *s
	s2.CurrentStep = "other"
	require.NoError(t, mgr.Save(ctx, &s2, 30))

	s.CurrentStep = "mine"
	err = mgr.Save(ctx, s, 30)
	assert.ErrorIs(t, err, session.ErrVersionConflict)
}

func TestComplete_ExitFlow(t *testing.T) {
	mgr, orgID, contactID := setupMgr(t)
	ctx := context.Background()
	key := session.Key{OrganizationID: orgID, ContactID: contactID, WhatsAppAccount: "main"}
	s, _, err := mgr.GetOrCreateActive(ctx, key, "1", 30)
	require.NoError(t, err)
	flowID := uuid.New()
	s.CurrentFlowID = &flowID
	s.CurrentStep = "n1"
	require.NoError(t, mgr.Save(ctx, s, 30))

	require.NoError(t, mgr.ExitFlow(ctx, s))
	assert.Equal(t, models.SessionStatusCompleted, s.Status)
	assert.Nil(t, s.CurrentFlowID)
	assert.Equal(t, "", s.CurrentStep)
	assert.Equal(t, session.ReasonExitFlow, s.CompletionReason)
}

func TestExpireStale(t *testing.T) {
	mgr, orgID, contactID := setupMgr(t)
	ctx := context.Background()
	past := time.Now().Add(-2 * time.Hour)
	exp := past
	row := models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "main",
		PhoneNumber:     "1",
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       past,
		LastActivityAt:  past,
		ExpiresAt:       &exp,
		Version:         1,
	}
	require.NoError(t, mgr.DB.Create(&row).Error)

	n, err := mgr.ExpireStale(ctx, 30, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var got models.ChatbotSession
	require.NoError(t, mgr.DB.First(&got, row.ID).Error)
	assert.Equal(t, models.SessionStatusExpired, got.Status)
}
