package orchestrator

// Ladder step names match CHATBOT_TARGET_ARCHITECTURE.md §13 (canonical priority).
// Phase 5 records each step for observability; only LegacyFull executes work.
const (
	StepGuards           = "guards"
	StepTransferActive   = "transfer_active"
	StepSessionState     = "session_state"
	StepGlobalIntents    = "global_intents"
	StepWaitResolution   = "wait_resolution"
	StepFlowActive       = "flow_active"
	StepKeywordRules     = "keyword_rules"
	StepFlowTriggers     = "flow_triggers"
	StepGreeting         = "greeting"
	StepAIPipeline       = "ai_pipeline"
	StepFallback         = "fallback"
	StepLegacyFull       = "legacy_full" // Phase 5: entire turn handled by legacy adapter
)

// LadderOrder is the evaluation order. Phase 5 walks this for timeline only;
// real claim happens at StepLegacyFull via the legacy adapter.
var LadderOrder = []string{
	StepGuards,
	StepTransferActive,
	StepSessionState,
	StepGlobalIntents,
	StepWaitResolution,
	StepFlowActive,
	StepKeywordRules,
	StepFlowTriggers,
	StepGreeting,
	StepAIPipeline,
	StepFallback,
	StepLegacyFull,
}

// PlannedRoute is a pure description of which ladder step would own a turn.
// Phase 5 always plans legacy_full; later phases fill real winners.
type PlannedRoute struct {
	// Winner is the step expected to claim the turn.
	Winner string
	// Deferred lists steps not yet implemented (logged for shadow compare).
	Deferred []string
}

// PlanRoute returns the Phase 5 routing plan: everything defers to legacy_full.
// Pure function — no I/O — safe for shadow compare without side effects.
func PlanRoute() PlannedRoute {
	deferred := make([]string, 0, len(LadderOrder)-1)
	for _, s := range LadderOrder {
		if s == StepLegacyFull {
			continue
		}
		deferred = append(deferred, s)
	}
	return PlannedRoute{
		Winner:   StepLegacyFull,
		Deferred: deferred,
	}
}
