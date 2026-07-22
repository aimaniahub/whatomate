package keyword_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/chatbot/keyword"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchRules_ExactAndSchedule(t *testing.T) {
	eng := keyword.NewEngine()
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)
	rules := []models.KeywordRule{
		{
			BaseModel: models.BaseModel{ID: uuid.New()}, Name: "future", IsEnabled: true,
			Keywords: models.StringArray{"hello"}, MatchType: models.MatchTypeExact,
			ResponseType: models.ResponseTypeText, ResponseContent: models.JSONB{"body": "hi"},
			ActiveFrom: &future,
		},
		{
			BaseModel: models.BaseModel{ID: uuid.New()}, Name: "ok", IsEnabled: true,
			Keywords: models.StringArray{"hello"}, MatchType: models.MatchTypeExact,
			ResponseType: models.ResponseTypeText, ResponseContent: models.JSONB{"body": "welcome"},
			ActiveFrom: &past,
		},
	}
	m, ok := eng.MatchRules(rules, "hello", time.Now())
	require.True(t, ok)
	assert.Equal(t, "welcome", m.Body)
	assert.Equal(t, "ok", m.RuleName)
}

func TestMatchRules_FlowID(t *testing.T) {
	eng := keyword.NewEngine()
	fid := uuid.New()
	rules := []models.KeywordRule{{
		BaseModel: models.BaseModel{ID: uuid.New()}, Name: "f", IsEnabled: true,
		Keywords: models.StringArray{"register"}, MatchType: models.MatchTypeContains,
		ResponseType: models.ResponseTypeFlow,
		ResponseContent: models.JSONB{"flow_id": fid.String(), "body": "starting"},
	}}
	m, ok := eng.MatchRules(rules, "please register me", time.Now())
	require.True(t, ok)
	require.NotNil(t, m.FlowID)
	assert.Equal(t, fid, *m.FlowID)
}
