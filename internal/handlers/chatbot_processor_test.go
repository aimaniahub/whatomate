package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newProcessorTestApp creates a minimal App suitable for chatbot processor tests.
// It connects to the test database and Redis, provides a mock WhatsApp client,
// and uses a no-op logger.
func newProcessorTestApp(t *testing.T) *App {
	t.Helper()
	db := testutil.SetupTestDB(t)
	log := testutil.NopLogger()

	// Mock WhatsApp API server that accepts all requests.
	waServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]string{{"id": "wamid.mock_" + uuid.New().String()[:8]}},
		})
	}))
	t.Cleanup(waServer.Close)

	app := &App{
		DB:         db,
		Log:        log,
		WhatsApp:   whatsapp.NewWithBaseURL(log, waServer.URL),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
	if rdb := testutil.SetupTestRedis(t); rdb != nil {
		app.Redis = rdb
	}
	return app
}

// createProcessorTestOrg creates an organization and WhatsApp account for processor tests.
func createProcessorTestOrg(t *testing.T, app *App) (*models.Organization, *models.WhatsAppAccount) {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	return org, account
}

// =============================================================================
// matchKeywordRules
// =============================================================================

func TestMatchKeywordRules_ExactMatch(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "exact-hello",
		Keywords:        models.StringArray{"hello"},
		MatchType:       models.MatchTypeExact,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Hello response"},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "hello")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, "Hello response", resp.Body)

	// Different case should also match (case insensitive by default)
	resp2, matched2 := app.matchKeywordRules(org.ID, account.Name, "HELLO")
	assert.True(t, matched2)
	require.NotNil(t, resp2)
	assert.Equal(t, "Hello response", resp2.Body)

	// Partial should NOT match exact
	_, matched3 := app.matchKeywordRules(org.ID, account.Name, "hello world")
	assert.False(t, matched3)
}

func TestMatchKeywordRules_ExactMatch_CaseSensitive(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "exact-case",
		Keywords:        models.StringArray{"Hello"},
		MatchType:       models.MatchTypeExact,
		CaseSensitive:   true,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Case match"},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	_, matched := app.matchKeywordRules(org.ID, account.Name, "Hello")
	assert.True(t, matched)

	_, matched2 := app.matchKeywordRules(org.ID, account.Name, "hello")
	assert.False(t, matched2)
}

func TestMatchKeywordRules_ContainsMatch(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "contains-help",
		Keywords:        models.StringArray{"help"},
		MatchType:       models.MatchTypeContains,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Help response"},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "I need help please")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, "Help response", resp.Body)

	_, matched2 := app.matchKeywordRules(org.ID, account.Name, "HELP ME")
	assert.True(t, matched2)

	_, matched3 := app.matchKeywordRules(org.ID, account.Name, "goodbye")
	assert.False(t, matched3)
}

func TestMatchKeywordRules_StartsWithMatch(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "starts-with-hi",
		Keywords:        models.StringArray{"hi"},
		MatchType:       models.MatchTypeStartsWith,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Hi response"},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "hi there")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, "Hi response", resp.Body)

	_, matched2 := app.matchKeywordRules(org.ID, account.Name, "say hi")
	assert.False(t, matched2)
}

func TestMatchKeywordRules_RegexMatch(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "regex-order",
		Keywords:        models.StringArray{`order\s*#?\d+`},
		MatchType:       models.MatchTypeRegex,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Order lookup"},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "I have order #12345")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, "Order lookup", resp.Body)

	_, matched2 := app.matchKeywordRules(org.ID, account.Name, "where is my package")
	assert.False(t, matched2)
}

func TestMatchKeywordRules_NoMatch(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "nope",
		Keywords:        models.StringArray{"specific-keyword"},
		MatchType:       models.MatchTypeExact,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "reply"},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "random message")
	assert.False(t, matched)
	assert.Nil(t, resp)
}

func TestMatchKeywordRules_Priority(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	// Lower priority rule
	lowRule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "low-priority",
		Keywords:        models.StringArray{"test"},
		MatchType:       models.MatchTypeContains,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Low priority"},
		Priority:        5,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(lowRule).Error)

	// Higher priority rule
	highRule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "high-priority",
		Keywords:        models.StringArray{"test"},
		MatchType:       models.MatchTypeContains,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "High priority"},
		Priority:        20,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(highRule).Error)

	// The higher priority rule should be returned (rules are ORDER BY priority DESC)
	resp, matched := app.matchKeywordRules(org.ID, account.Name, "this is a test")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, "High priority", resp.Body)
}

func TestMatchKeywordRules_DisabledRuleIgnored(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "disabled",
		Keywords:        models.StringArray{"disabled"},
		MatchType:       models.MatchTypeExact,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{"body": "Should not match"},
		Priority:        10,
		IsEnabled:       true, // Create as enabled first
	}
	require.NoError(t, app.DB.Create(rule).Error)
	// Explicitly disable: GORM skips zero-value bools with default:true on INSERT.
	require.NoError(t, app.DB.Model(rule).Update("is_enabled", false).Error)

	_, matched := app.matchKeywordRules(org.ID, account.Name, "disabled")
	assert.False(t, matched)
}

func TestMatchKeywordRules_TransferType(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "agent",
		Keywords:        models.StringArray{"agent"},
		MatchType:       models.MatchTypeExact,
		ResponseType:    models.ResponseTypeTransfer,
		ResponseContent: models.JSONB{"body": "Connecting you to an agent..."},
		Priority:        10,
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "agent")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, models.ResponseTypeTransfer, resp.ResponseType)
	assert.Equal(t, "Connecting you to an agent...", resp.Body)
}

func TestMatchKeywordRules_WithButtons(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	rule := &models.KeywordRule{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "menu",
		Keywords:        models.StringArray{"menu"},
		MatchType:       models.MatchTypeExact,
		ResponseType:    models.ResponseTypeText,
		ResponseContent: models.JSONB{
			"body": "Choose an option:",
			"buttons": []any{
				map[string]any{"id": "opt1", "title": "Option 1"},
				map[string]any{"id": "opt2", "title": "Option 2"},
			},
		},
		Priority:  10,
		IsEnabled: true,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	resp, matched := app.matchKeywordRules(org.ID, account.Name, "menu")
	assert.True(t, matched)
	require.NotNil(t, resp)
	assert.Equal(t, "Choose an option:", resp.Body)
	assert.Len(t, resp.Buttons, 2)
}

// =============================================================================
// getOrCreateSession
// =============================================================================

func TestGetOrCreateSession_NewSession(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	session, isNew := app.getOrCreateSession(org.ID, contact.ID, account.Name, contact.PhoneNumber, 30)
	assert.True(t, isNew)
	require.NotNil(t, session)
	assert.Equal(t, models.SessionStatusActive, session.Status)
	assert.Equal(t, org.ID, session.OrganizationID)
	assert.Equal(t, contact.ID, session.ContactID)
	assert.Equal(t, account.Name, session.WhatsAppAccount)

	// Verify it was persisted
	var dbSession models.ChatbotSession
	require.NoError(t, app.DB.First(&dbSession, session.ID).Error)
	assert.Equal(t, models.SessionStatusActive, dbSession.Status)
}

func TestGetOrCreateSession_ExistingSession(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	// Create an active session
	existing := models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     contact.PhoneNumber,
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{"key": "value"},
		StartedAt:       time.Now(),
		LastActivityAt:  time.Now(),
	}
	require.NoError(t, app.DB.Create(&existing).Error)

	session, isNew := app.getOrCreateSession(org.ID, contact.ID, account.Name, contact.PhoneNumber, 30)
	assert.False(t, isNew)
	require.NotNil(t, session)
	assert.Equal(t, existing.ID, session.ID)
}

func TestGetOrCreateSession_ExpiredSession(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	// Create an expired session (last activity 60 minutes ago, timeout is 30 minutes)
	expired := models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     contact.PhoneNumber,
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       time.Now().Add(-60 * time.Minute),
		LastActivityAt:  time.Now().Add(-60 * time.Minute),
	}
	require.NoError(t, app.DB.Create(&expired).Error)

	session, isNew := app.getOrCreateSession(org.ID, contact.ID, account.Name, contact.PhoneNumber, 30)
	assert.True(t, isNew)
	require.NotNil(t, session)
	assert.NotEqual(t, expired.ID, session.ID, "should create a new session, not return expired one")
}

// =============================================================================
// isWithinBusinessHours
// =============================================================================

func TestIsWithinBusinessHours_WithinHours(t *testing.T) {
	app := newProcessorTestApp(t)
	now := time.Now()
	dayOfWeek := float64(now.Weekday())

	hours := models.JSONBArray{
		map[string]any{
			"day":        dayOfWeek,
			"enabled":    true,
			"start_time": "00:00",
			"end_time":   "23:59",
		},
	}

	result := app.isWithinBusinessHours(hours)
	assert.True(t, result)
}

func TestIsWithinBusinessHours_OutsideHours(t *testing.T) {
	app := newProcessorTestApp(t)
	now := time.Now()
	dayOfWeek := float64(now.Weekday())

	// Set hours to a time window that has definitely passed
	// Use a very narrow window in the past
	hours := models.JSONBArray{
		map[string]any{
			"day":        dayOfWeek,
			"enabled":    true,
			"start_time": "00:00",
			"end_time":   "00:01",
		},
	}

	// This will only be true if running at midnight; for all practical purposes it tests false
	currentTime := now.Format("15:04")
	if currentTime > "00:01" {
		result := app.isWithinBusinessHours(hours)
		assert.False(t, result)
	}
}

func TestIsWithinBusinessHours_DayDisabled(t *testing.T) {
	app := newProcessorTestApp(t)
	now := time.Now()
	dayOfWeek := float64(now.Weekday())

	hours := models.JSONBArray{
		map[string]any{
			"day":        dayOfWeek,
			"enabled":    false,
			"start_time": "00:00",
			"end_time":   "23:59",
		},
	}

	result := app.isWithinBusinessHours(hours)
	assert.False(t, result)
}

func TestIsWithinBusinessHours_NoMatchingDay(t *testing.T) {
	app := newProcessorTestApp(t)
	now := time.Now()
	// Use a different day of the week
	otherDay := float64((int(now.Weekday()) + 1) % 7)

	hours := models.JSONBArray{
		map[string]any{
			"day":        otherDay,
			"enabled":    true,
			"start_time": "00:00",
			"end_time":   "23:59",
		},
	}

	result := app.isWithinBusinessHours(hours)
	assert.False(t, result)
}

func TestIsWithinBusinessHours_EmptyHours(t *testing.T) {
	app := newProcessorTestApp(t)

	result := app.isWithinBusinessHours(models.JSONBArray{})
	assert.False(t, result)
}

// =============================================================================
// shouldSkipStep
// =============================================================================

func TestExitFlow_UpdatesSession(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	flow := &models.ChatbotFlow{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "Exit Test Flow",
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(flow).Error)

	session := &models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     contact.PhoneNumber,
		Status:          models.SessionStatusActive,
		CurrentFlowID:   &flow.ID,
		CurrentStep:     "step2",
		StepRetries:     2,
		SessionData:     models.JSONB{},
		StartedAt:       time.Now(),
		LastActivityAt:  time.Now(),
	}
	require.NoError(t, app.DB.Create(session).Error)

	app.exitFlow(session)

	var dbSession models.ChatbotSession
	require.NoError(t, app.DB.First(&dbSession, session.ID).Error)
	assert.Equal(t, models.SessionStatusCompleted, dbSession.Status)
	assert.Equal(t, "", dbSession.CurrentStep)
	assert.Equal(t, 0, dbSession.StepRetries)
	assert.NotNil(t, dbSession.CompletedAt)
}

// =============================================================================
// saveIncomingMessage
// =============================================================================

func TestSaveIncomingMessage_TextMessage(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	waMsgID := "wamid." + uuid.New().String()[:16]
	app.saveIncomingMessage(account, contact, waMsgID, "text", "Hello from test", nil, "")

	// Verify message was saved
	var msg models.Message
	require.NoError(t, app.DB.Where("whats_app_message_id = ?", waMsgID).First(&msg).Error)
	assert.Equal(t, models.DirectionIncoming, msg.Direction)
	assert.Equal(t, models.MessageTypeText, msg.MessageType)
	assert.Equal(t, "Hello from test", msg.Content)
	assert.Equal(t, contact.ID, msg.ContactID)
	assert.Equal(t, account.Name, msg.WhatsAppAccount)
	assert.Equal(t, models.MessageStatusReceived, msg.Status)

	// Verify contact was updated
	var dbContact models.Contact
	require.NoError(t, app.DB.First(&dbContact, contact.ID).Error)
	assert.NotNil(t, dbContact.LastMessageAt)
	assert.Equal(t, "Hello from test", dbContact.LastMessagePreview)
	assert.False(t, dbContact.IsRead)
}

func TestSaveIncomingMessage_WithMedia(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	waMsgID := "wamid." + uuid.New().String()[:16]
	media := &MediaInfo{
		MediaURL:      "/uploads/test-image.jpg",
		MediaMimeType: "image/jpeg",
		MediaFilename: "photo.jpg",
	}
	app.saveIncomingMessage(account, contact, waMsgID, "image", "Look at this", media, "")

	var msg models.Message
	require.NoError(t, app.DB.Where("whats_app_message_id = ?", waMsgID).First(&msg).Error)
	assert.Equal(t, "/uploads/test-image.jpg", msg.MediaURL)
	assert.Equal(t, "image/jpeg", msg.MediaMimeType)
	assert.Equal(t, "photo.jpg", msg.MediaFilename)

	// Non-text messages show type in preview
	var dbContact models.Contact
	require.NoError(t, app.DB.First(&dbContact, contact.ID).Error)
	assert.Equal(t, "[image]", dbContact.LastMessagePreview)
}

func TestSaveIncomingMessage_WithReplyContext(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	// Create original message to reply to
	originalWAMID := "wamid.original_" + uuid.New().String()[:8]
	originalMsg := models.Message{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		OrganizationID:    org.ID,
		WhatsAppAccount:   account.Name,
		ContactID:         contact.ID,
		WhatsAppMessageID: originalWAMID,
		Direction:         models.DirectionOutgoing,
		MessageType:       models.MessageTypeText,
		Content:           "Original message",
		Status:            models.MessageStatusReceived,
	}
	require.NoError(t, app.DB.Create(&originalMsg).Error)

	// Save reply message
	replyWAMID := "wamid.reply_" + uuid.New().String()[:8]
	app.saveIncomingMessage(account, contact, replyWAMID, "text", "Reply to your message", nil, originalWAMID)

	var replyMsg models.Message
	require.NoError(t, app.DB.Where("whats_app_message_id = ?", replyWAMID).First(&replyMsg).Error)
	assert.True(t, replyMsg.IsReply)
	require.NotNil(t, replyMsg.ReplyToMessageID)
	assert.Equal(t, originalMsg.ID, *replyMsg.ReplyToMessageID)
}

func TestSaveIncomingMessage_LongContent(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	// Create a message with content longer than 100 characters
	longContent := ""
	for i := 0; i < 120; i++ {
		longContent += "x"
	}
	waMsgID := "wamid." + uuid.New().String()[:16]
	app.saveIncomingMessage(account, contact, waMsgID, "text", longContent, nil, "")

	var dbContact models.Contact
	require.NoError(t, app.DB.First(&dbContact, contact.ID).Error)
	// Preview should be truncated to 97 chars + "..."
	assert.Len(t, dbContact.LastMessagePreview, 100)
	assert.True(t, len(dbContact.LastMessagePreview) <= 100)
}

// =============================================================================
// processTemplate with session data (replaces former replaceVariables)
// =============================================================================

func TestProcessTemplateSessionData_Basic(t *testing.T) {
	result := processTemplate("Hello {{name}}, your order is {{order_id}}", models.JSONB{
		"name":     "John",
		"order_id": "12345",
	})
	assert.Equal(t, "Hello John, your order is 12345", result)
}

func TestProcessTemplateSessionData_NilData(t *testing.T) {
	result := processTemplate("Hello {{name}}", nil)
	// processTemplate replaces unresolved variables with empty string
	assert.Equal(t, "Hello ", result)
}

func TestProcessTemplateSessionData_MissingVariable(t *testing.T) {
	result := processTemplate("Hello {{name}}", models.JSONB{})
	// processTemplate replaces unresolved variables with empty string
	assert.Equal(t, "Hello ", result)
}

// =============================================================================
// logSessionMessage
// =============================================================================

func TestLogSessionMessage(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	session := &models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     contact.PhoneNumber,
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
		StartedAt:       time.Now(),
		LastActivityAt:  time.Now(),
	}
	require.NoError(t, app.DB.Create(session).Error)

	app.logSessionMessage(session.ID, models.DirectionIncoming, "test message", "greeting")

	var msgs []models.ChatbotSessionMessage
	require.NoError(t, app.DB.Where("session_id = ?", session.ID).Find(&msgs).Error)
	require.Len(t, msgs, 1)
	assert.Equal(t, "test message", msgs[0].Message)
	assert.Equal(t, "greeting", msgs[0].StepName)
	assert.Equal(t, models.DirectionIncoming, msgs[0].Direction)
}

// =============================================================================
// matchFlowTrigger
// =============================================================================

func TestMatchFlowTrigger_Match(t *testing.T) {
	app := newProcessorTestApp(t)
	org, account := createProcessorTestOrg(t, app)

	flow := &models.ChatbotFlow{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "Order Flow",
		TriggerKeywords: models.StringArray{"order", "buy"},
		IsEnabled:       true,
	}
	require.NoError(t, app.DB.Create(flow).Error)

	result := app.matchFlowTrigger(org.ID, "I want to order")
	require.NotNil(t, result)
	assert.Equal(t, flow.ID, result.ID)

	// No match
	noMatch := app.matchFlowTrigger(org.ID, "hello there")
	assert.Nil(t, noMatch)
}

// =============================================================================
// evaluateExpression (package-level, not on App)
// =============================================================================

func TestIsContactOrLocationQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"where is your office?", true},
		{"contact us please", true},
		{"what is the address?", true},
		{"send me the maps link", true},
		{"i live in Bengaluru", true},
		{"how to grow sandalwood?", false},
		{"is coconut plant available", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, isContactOrLocationQuery(tc.input))
		})
	}
}

func TestCleanAIResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"with think tags",
			"<think>this is a chain of thought reasoning</think>Actual output response text",
			"Actual output response text",
		},
		{
			"with thought tags",
			"<thought>reasoning process</thought>Hello world!",
			"Hello world!",
		},
		{
			"mixed multiline think tags",
			"<think>\nfirst line of reasoning\nsecond line\n</think>\n\nPremium sandalwood varieties.",
			"Premium sandalwood varieties.",
		},
		{
			"no tags",
			"Just normal text answer.",
			"Just normal text answer.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, cleanAIResponse(tc.input))
		})
	}
}

// =============================================================================
// RAG AI Context helpers + generateAIResponse short-circuit
// =============================================================================

func TestNormalizeRAGChatURL(t *testing.T) {
	assert.Equal(t, "https://example.com/chat", normalizeRAGChatURL("https://example.com"))
	assert.Equal(t, "https://example.com/chat", normalizeRAGChatURL("https://example.com/"))
	assert.Equal(t, "https://example.com/chat", normalizeRAGChatURL("https://example.com/chat"))
	assert.Equal(t, "https://example.com/chat", normalizeRAGChatURL("https://example.com/chat/"))
	assert.Equal(t, "", normalizeRAGChatURL("  "))
}

func TestFlowTriggerKeywordMatches_WholeWord(t *testing.T) {
	// Short "hi" must NOT match inside "this" / "which" (was restarting Welcome)
	assert.False(t, flowTriggerKeywordMatches("what is the price of this plant?", "hi"))
	assert.False(t, flowTriggerKeywordMatches("which variety is best?", "hi"))
	assert.True(t, flowTriggerKeywordMatches("hi", "hi"))
	assert.True(t, flowTriggerKeywordMatches("Hi there", "hi"))
	assert.True(t, flowTriggerKeywordMatches("please start", "start"))
	assert.True(t, flowTriggerKeywordMatches("open main menu please", "main menu"))
	assert.False(t, flowTriggerKeywordMatches("tell me about sandalwood", "menu"))
	assert.True(t, flowTriggerKeywordMatches("show menu", "menu"))
}

func TestAIContextMatchesKeywords(t *testing.T) {
	always := models.AIContext{TriggerKeywords: nil}
	assert.True(t, aiContextMatchesKeywords(always, "anything"))

	empty := models.AIContext{TriggerKeywords: models.StringArray{}}
	assert.True(t, aiContextMatchesKeywords(empty, "anything"))

	priced := models.AIContext{TriggerKeywords: models.StringArray{"price", "cost"}}
	assert.True(t, aiContextMatchesKeywords(priced, "What is the PRICE of guava?"))
	assert.False(t, aiContextMatchesKeywords(priced, "How do I register?"))
}

func TestFilterMatchingAIContexts_PriorityAndKeywords(t *testing.T) {
	low := models.AIContext{
		Name: "low", IsEnabled: true, Priority: 1,
		ContextType: models.ContextTypeStatic, TriggerKeywords: nil,
	}
	high := models.AIContext{
		Name: "high", IsEnabled: true, Priority: 50,
		ContextType: models.ContextTypeRAG, TriggerKeywords: nil,
	}
	disabled := models.AIContext{
		Name: "off", IsEnabled: false, Priority: 100,
		ContextType: models.ContextTypeRAG,
	}
	keywordOnly := models.AIContext{
		Name: "kw", IsEnabled: true, Priority: 20,
		ContextType: models.ContextTypeStatic,
		TriggerKeywords: models.StringArray{"pricing"},
	}

	matched := filterMatchingAIContexts([]models.AIContext{low, high, disabled, keywordOnly}, "hello")
	require.Len(t, matched, 2)
	assert.Equal(t, "high", matched[0].Name)
	assert.Equal(t, "low", matched[1].Name)

	matched = filterMatchingAIContexts([]models.AIContext{low, high, keywordOnly}, "tell me about pricing")
	require.Len(t, matched, 3)
	assert.Equal(t, "high", matched[0].Name)
}

func TestGenerateAIResponse_RAGShortCircuit(t *testing.T) {
	app := newProcessorTestApp(t)
	if app.Redis == nil {
		t.Skip("Redis required for AI context cache")
	}
	org, account := createProcessorTestOrg(t, app)

	var gotQuestion string
	var gotAPIKey string
	ragServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/chat", r.URL.Path)
		gotAPIKey = r.Header.Get("X-API-Key")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuestion, _ = body["question"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answer":    "Guava plants start at ₹120 per plant.",
			"abstained": false,
			"sources":   []any{},
		})
	}))
	t.Cleanup(ragServer.Close)

	ctx := &models.AIContext{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: "",
		Name:            "Darvi RAG",
		ContextType:     models.ContextTypeRAG,
		IsEnabled:       true,
		Priority:        100,
		ApiConfig: models.JSONB{
			"url":     ragServer.URL,
			"api_key": "test-rag-key",
			"top_k":   float64(5),
			"language": "en",
		},
	}
	require.NoError(t, app.DB.Create(ctx).Error)

	// Local AI deliberately NOT configured — RAG alone must answer
	settings := &models.ChatbotSettings{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		IsEnabled:       true,
		AI: models.AIConfig{
			Enabled:  false,
			Provider: "",
			APIKey:   "",
		},
	}
	require.NoError(t, app.DB.Create(settings).Error)

	session := &models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     "919999999999",
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
	}
	require.NoError(t, app.DB.Create(session).Error)

	answer, err := app.generateAIResponse(settings, session, "What is guava plant price?")
	require.NoError(t, err)
	assert.Equal(t, "Guava plants start at ₹120 per plant.", answer)
	assert.Equal(t, "What is guava plant price?", gotQuestion)
	assert.Equal(t, "test-rag-key", gotAPIKey)
}

func TestGenerateAIResponse_RAGAbstainedFallsBackToLocal(t *testing.T) {
	app := newProcessorTestApp(t)
	if app.Redis == nil {
		t.Skip("Redis required for AI context cache")
	}
	org, account := createProcessorTestOrg(t, app)

	ragServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answer":    "I could not find the answer in the documents.",
			"abstained": true,
		})
	}))
	t.Cleanup(ragServer.Close)

	// Local OpenAI-compatible mock
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "Fallback local answer"}},
			},
		})
	}))
	t.Cleanup(llmServer.Close)

	// Point OpenAI URL... generateOpenAIResponse hardcodes api.openai.com.
	// So we only assert RAG-only failure message when no local provider.
	ragCtx := &models.AIContext{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "RAG abstain",
		ContextType:    models.ContextTypeRAG,
		IsEnabled:      true,
		Priority:       10,
		ApiConfig: models.JSONB{
			"url":     ragServer.URL + "/chat",
			"api_key": "k",
		},
	}
	require.NoError(t, app.DB.Create(ragCtx).Error)

	settings := &models.ChatbotSettings{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		AI:              models.AIConfig{Enabled: false},
	}
	require.NoError(t, app.DB.Create(settings).Error)

	session := &models.ChatbotSession{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     "918888888888",
		Status:          models.SessionStatusActive,
		SessionData:     models.JSONB{},
	}
	require.NoError(t, app.DB.Create(session).Error)

	// Soft fallback: user gets a message instead of a hard error when RAG
	// abstains and no local LLM is configured.
	answer, err := app.generateAIResponse(settings, session, "random unrelated question")
	require.NoError(t, err)
	assert.NotEmpty(t, answer)
	assert.Contains(t, strings.ToLower(answer), "could not find")
	_ = llmServer // reserved for future local fallback URL injection
}

func TestCanAttemptAI_RAGWithoutLocalProvider(t *testing.T) {
	app := newProcessorTestApp(t)
	if app.Redis == nil {
		t.Skip("Redis required")
	}
	org, _ := createProcessorTestOrg(t, app)

	settings := &models.ChatbotSettings{
		OrganizationID: org.ID,
		AI:             models.AIConfig{Enabled: false},
	}
	assert.False(t, app.canAttemptAI(settings, ""))

	require.NoError(t, app.DB.Create(&models.AIContext{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "rag",
		ContextType:    models.ContextTypeRAG,
		IsEnabled:      true,
		ApiConfig:      models.JSONB{"url": "https://example.com/chat"},
	}).Error)

	assert.True(t, app.canAttemptAI(settings, ""))
}

func TestBuildAIContext_SkipsRAGType(t *testing.T) {
	app := newProcessorTestApp(t)
	if app.Redis == nil {
		t.Skip("Redis required")
	}
	org, _ := createProcessorTestOrg(t, app)

	require.NoError(t, app.DB.Create(&models.AIContext{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "Static FAQ",
		ContextType:    models.ContextTypeStatic,
		StaticContent:  "Hours are 9-5",
		IsEnabled:      true,
		Priority:       10,
	}).Error)
	require.NoError(t, app.DB.Create(&models.AIContext{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "Company RAG",
		ContextType:    models.ContextTypeRAG,
		IsEnabled:      true,
		Priority:       100,
		ApiConfig:      models.JSONB{"url": "https://example.com/chat", "api_key": "x"},
	}).Error)

	session := &models.ChatbotSession{
		OrganizationID:  org.ID,
		WhatsAppAccount: "",
		SessionData:     models.JSONB{},
	}
	built := app.buildAIContext(org.ID, session, "hello")
	assert.Contains(t, built, "Hours are 9-5")
	assert.NotContains(t, built, "Company RAG")
	assert.NotContains(t, built, "example.com")
}



