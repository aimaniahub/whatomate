package interactive_test

import (
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/chatbot/interactive"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngine_SetAndResolveTitle(t *testing.T) {
	eng := interactive.NewEngine(true, true)
	sess := &models.ChatbotSession{SessionData: models.JSONB{}}
	buttons := []map[string]any{
		{"id": "opt_a", "title": "Option A"},
		{"id": "opt_b", "title": "Option B"},
	}
	eng.SetButtonsWait(sess, "node1", "flow1", "Pick one", buttons)
	require.NotNil(t, sess.WaitContract)

	id, ok, src := eng.ResolveButtonInput(sess, buttons, "", "option a")
	assert.True(t, ok)
	assert.Equal(t, "opt_a", id)
	assert.Equal(t, "title", src)
}

func TestEngine_ClearWait(t *testing.T) {
	eng := interactive.NewEngine(true, true)
	sess := &models.ChatbotSession{}
	eng.SetButtonsWait(sess, "n", "f", "body", []map[string]any{{"id": "x", "title": "X"}})
	require.True(t, eng.HasActiveButtonsWait(sess))
	eng.ClearWait(sess)
	assert.False(t, eng.HasActiveButtonsWait(sess))
}

func TestEngine_TitleMatchOff(t *testing.T) {
	eng := interactive.NewEngine(true, false)
	sess := &models.ChatbotSession{}
	buttons := []map[string]any{{"id": "a", "title": "Alpha"}}
	eng.SetButtonsWait(sess, "n", "f", "b", buttons)
	id, ok, _ := eng.ResolveButtonInput(sess, buttons, "", "Alpha")
	assert.False(t, ok)
	assert.Equal(t, "", id)
	// Native click still works
	id, ok, _ = eng.ResolveButtonInput(sess, buttons, "a", "")
	assert.True(t, ok)
	assert.Equal(t, "a", id)
}

func TestContract_Expired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	c := &interactive.Contract{ExpiresAt: &past}
	assert.True(t, c.Expired(time.Now()))
}
