package planner_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/chatbot/planner"
	"github.com/stretchr/testify/assert"
)

func TestPlan_SuppressesDuplicateFingerprint(t *testing.T) {
	p := planner.NewPlan()
	assert.True(t, p.ShouldSend(planner.KindInteractive, "Pick one", "a", "b"))
	assert.False(t, p.ShouldSend(planner.KindInteractive, "Pick one", "a", "b"))
	assert.True(t, p.ShouldSend(planner.KindText, "Hello"))
	assert.Equal(t, 2, p.Len())
}

func TestEngine_DisabledReturnsNilPlan(t *testing.T) {
	e := planner.NewEngine(false)
	assert.Nil(t, e.BeginTurn())
	assert.True(t, (*planner.Plan)(nil).ShouldSend(planner.KindText, "x"))
}
