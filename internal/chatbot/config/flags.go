package config

// Flag names match CHATBOT_IMPLEMENTATION_SPECIFICATION.md §2.2.
// Values are hierarchical: global default → org override → account override.
const (
	FlagIdempotencyV1           = "chatbot.idempotency_v1"
	FlagSessionLockV1           = "chatbot.session_lock_v1"
	FlagOrchestratorV2          = "chatbot.orchestrator_v2"
	FlagWaitContractV1          = "chatbot.wait_contract_v1"
	FlagInteractiveTitleMatchV1 = "chatbot.interactive_title_match_v1"
	FlagPriorityLadderV1        = "chatbot.priority_ladder_v1"
	FlagAIPipelineV1            = "chatbot.ai_pipeline_v1"
	FlagResponsePlannerV1       = "chatbot.response_planner_v1"
	FlagFlowVersionsV1          = "chatbot.flow_versions_v1"
	FlagShadowCompareV1         = "chatbot.shadow_compare_v1"
)

// AllFlagNames is the canonical list of chatbot feature flags (for docs/tests).
var AllFlagNames = []string{
	FlagIdempotencyV1,
	FlagSessionLockV1,
	FlagOrchestratorV2,
	FlagWaitContractV1,
	FlagInteractiveTitleMatchV1,
	FlagPriorityLadderV1,
	FlagAIPipelineV1,
	FlagResponsePlannerV1,
	FlagFlowVersionsV1,
	FlagShadowCompareV1,
}

// Flags holds global (or scoped) boolean switches for the chatbot refactor.
// Zero value means all off → 100% legacy path (Phase 1 acceptance).
type Flags struct {
	IdempotencyV1           bool `json:"idempotency_v1" koanf:"idempotency_v1"`
	SessionLockV1           bool `json:"session_lock_v1" koanf:"session_lock_v1"`
	OrchestratorV2          bool `json:"orchestrator_v2" koanf:"orchestrator_v2"`
	WaitContractV1          bool `json:"wait_contract_v1" koanf:"wait_contract_v1"`
	InteractiveTitleMatchV1 bool `json:"interactive_title_match_v1" koanf:"interactive_title_match_v1"`
	PriorityLadderV1        bool `json:"priority_ladder_v1" koanf:"priority_ladder_v1"`
	AIPipelineV1            bool `json:"ai_pipeline_v1" koanf:"ai_pipeline_v1"`
	ResponsePlannerV1       bool `json:"response_planner_v1" koanf:"response_planner_v1"`
	FlowVersionsV1          bool `json:"flow_versions_v1" koanf:"flow_versions_v1"`
	ShadowCompareV1         bool `json:"shadow_compare_v1" koanf:"shadow_compare_v1"`
}

// Get returns the boolean for a named flag. Unknown names are false.
func (f Flags) Get(name string) bool {
	switch name {
	case FlagIdempotencyV1:
		return f.IdempotencyV1
	case FlagSessionLockV1:
		return f.SessionLockV1
	case FlagOrchestratorV2:
		return f.OrchestratorV2
	case FlagWaitContractV1:
		return f.WaitContractV1
	case FlagInteractiveTitleMatchV1:
		return f.InteractiveTitleMatchV1
	case FlagPriorityLadderV1:
		return f.PriorityLadderV1
	case FlagAIPipelineV1:
		return f.AIPipelineV1
	case FlagResponsePlannerV1:
		return f.ResponsePlannerV1
	case FlagFlowVersionsV1:
		return f.FlowVersionsV1
	case FlagShadowCompareV1:
		return f.ShadowCompareV1
	default:
		return false
	}
}

// AnyEnabled reports whether any refactor flag is on (useful for diagnostics).
func (f Flags) AnyEnabled() bool {
	return f.IdempotencyV1 || f.SessionLockV1 || f.OrchestratorV2 ||
		f.WaitContractV1 || f.InteractiveTitleMatchV1 || f.PriorityLadderV1 ||
		f.AIPipelineV1 || f.ResponsePlannerV1 || f.FlowVersionsV1 || f.ShadowCompareV1
}
