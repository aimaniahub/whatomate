package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shridarpatil/whatomate/internal/chatbot/orchestrator"
	"github.com/shridarpatil/whatomate/internal/chatbot/turn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubLegacy struct {
	called int
	err    error
}

func (s *stubLegacy) HandleLegacyTurn(ctx context.Context, tc *turn.Context) error {
	s.called++
	return s.err
}

func TestPlanRoute_Phase5AlwaysLegacy(t *testing.T) {
	p := orchestrator.PlanRoute()
	assert.Equal(t, orchestrator.StepLegacyFull, p.Winner)
	assert.Contains(t, p.Deferred, orchestrator.StepGuards)
	assert.Contains(t, p.Deferred, orchestrator.StepKeywordRules)
	assert.NotContains(t, p.Deferred, orchestrator.StepLegacyFull)
	assert.Equal(t, len(orchestrator.LadderOrder)-1, len(p.Deferred))
}

func TestHandleTurn_CallsLegacy(t *testing.T) {
	legacy := &stubLegacy{}
	orch := orchestrator.New(legacy, nil)
	tc := &turn.Context{
		IDs:           turn.NewIDs(),
		PhoneNumberID: "pn",
		RawMessage:    []byte(`{}`),
	}
	res, err := orch.HandleTurn(context.Background(), tc)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Claimed)
	assert.Equal(t, "legacy_full", res.Handler)
	assert.Equal(t, "orchestrator", res.Path)
	assert.Equal(t, 1, legacy.called)
	assert.GreaterOrEqual(t, len(res.Timeline), len(orchestrator.LadderOrder))
}

func TestHandleTurn_LegacyError(t *testing.T) {
	legacy := &stubLegacy{err: errors.New("boom")}
	orch := orchestrator.New(legacy, nil)
	res, err := orch.HandleTurn(context.Background(), &turn.Context{IDs: turn.NewIDs()})
	assert.Error(t, err)
	assert.True(t, res.Claimed)
}

func TestHandleTurn_NilLegacy(t *testing.T) {
	orch := orchestrator.New(nil, nil)
	_, err := orch.HandleTurn(context.Background(), &turn.Context{IDs: turn.NewIDs()})
	assert.Error(t, err)
}

func TestLadderOrder_StableSnapshot(t *testing.T) {
	// Snapshot test: changing order requires deliberate review (implementation spec).
	want := []string{
		orchestrator.StepGuards,
		orchestrator.StepTransferActive,
		orchestrator.StepSessionState,
		orchestrator.StepGlobalIntents,
		orchestrator.StepWaitResolution,
		orchestrator.StepFlowActive,
		orchestrator.StepKeywordRules,
		orchestrator.StepFlowTriggers,
		orchestrator.StepGreeting,
		orchestrator.StepAIPipeline,
		orchestrator.StepFallback,
		orchestrator.StepLegacyFull,
	}
	assert.Equal(t, want, orchestrator.LadderOrder)
}
