package ai_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/chatbot/ai"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePlan(t *testing.T) {
	p := ai.ResolvePlan(models.FreeTextAIRAGOnly, true, true)
	assert.True(t, p.TryRAG)
	assert.False(t, p.TryLLM)

	p = ai.ResolvePlan(models.FreeTextAIRAGThenLocal, true, true)
	assert.True(t, p.TryRAG)
	assert.True(t, p.TryLLM)
}

func TestKnowledgeHit(t *testing.T) {
	ctxs := []models.AIContext{{
		Name: "hours", IsEnabled: true, ContextType: models.ContextTypeStatic,
		Priority: 10, TriggerKeywords: models.StringArray{"hours", "open"},
		StaticContent: "We are open 9-5 Mon-Fri.",
	}}
	c, ok := ai.KnowledgeHit(ctxs, "what are your hours?")
	require.True(t, ok)
	assert.Contains(t, c.Text, "9-5")
}

func TestPickBest(t *testing.T) {
	c, ok := ai.PickBest(
		ai.Candidate{Abstained: true, Text: "x"},
		ai.Candidate{Text: "hello", Source: "rag"},
	)
	require.True(t, ok)
	assert.Equal(t, "hello", c.Text)
}
