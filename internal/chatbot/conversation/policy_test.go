package conversation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/chatbot/conversation"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestShouldGreet_NewSessionWithDefault(t *testing.T) {
	sess := &models.ChatbotSession{SessionData: models.JSONB{}}
	plan := conversation.ShouldGreet(true, sess, "Welcome!")
	assert.True(t, plan.ShouldGreet)

	// isSimpleGreeting=true → no pre-answer
	plan = conversation.ShouldGreet(true, sess, "Welcome!").WithPreAnswer(true)
	assert.False(t, plan.AllowPreAnswer)
	// isSimpleGreeting=false → allow keyword/AI before menu
	plan = conversation.ShouldGreet(true, sess, "Welcome!").WithPreAnswer(false)
	assert.True(t, plan.AllowPreAnswer)
}

func TestShouldGreet_NotNewOrEmptyOrAlreadySent(t *testing.T) {
	sess := &models.ChatbotSession{SessionData: models.JSONB{}}
	assert.False(t, conversation.ShouldGreet(false, sess, "Hi").ShouldGreet)
	assert.False(t, conversation.ShouldGreet(true, sess, "").ShouldGreet)
	assert.False(t, conversation.ShouldGreet(true, sess, "   ").ShouldGreet)

	conversation.MarkGreetingSent(sess)
	assert.True(t, conversation.GreetingAlreadySent(sess))
	assert.False(t, conversation.ShouldGreet(true, sess, "Hi").ShouldGreet)
}

func TestIsRestartIntent(t *testing.T) {
	assert.True(t, conversation.IsRestartIntent("restart", nil))
	assert.True(t, conversation.IsRestartIntent("Please START OVER now", nil))
	assert.True(t, conversation.IsRestartIntent("reset chat", nil))
	assert.False(t, conversation.IsRestartIntent("hi", nil))
	assert.False(t, conversation.IsRestartIntent("start", nil))
	assert.False(t, conversation.IsRestartIntent("restarted successfully", nil)) // "restart" not whole word
	assert.True(t, conversation.IsRestartIntent("please reboot now", []string{"reboot"}))
}

func TestCanRestartInFreeChat(t *testing.T) {
	flow := uuid.New()
	assert.True(t, conversation.CanRestartInFreeChat(&models.ChatbotSession{
		Status: models.SessionStatusActive,
	}))
	assert.False(t, conversation.CanRestartInFreeChat(&models.ChatbotSession{
		Status:        models.SessionStatusActive,
		CurrentFlowID: &flow,
	}))
	assert.False(t, conversation.CanRestartInFreeChat(&models.ChatbotSession{
		Status: models.SessionStatusCompleted,
	}))
}
