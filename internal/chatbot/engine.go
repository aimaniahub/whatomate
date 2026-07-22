package chatbot

import (
	chatbotconfig "github.com/shridarpatil/whatomate/internal/chatbot/config"
	"github.com/shridarpatil/whatomate/internal/chatbot/conversation"
	"github.com/shridarpatil/whatomate/internal/chatbot/events"
	"github.com/shridarpatil/whatomate/internal/chatbot/idempotency"
	"github.com/shridarpatil/whatomate/internal/chatbot/interactive"
	"github.com/shridarpatil/whatomate/internal/chatbot/keyword"
	"github.com/shridarpatil/whatomate/internal/chatbot/lock"
	"github.com/shridarpatil/whatomate/internal/chatbot/nodes"
	"github.com/shridarpatil/whatomate/internal/chatbot/orchestrator"
	"github.com/shridarpatil/whatomate/internal/chatbot/planner"
	"github.com/shridarpatil/whatomate/internal/chatbot/session"
	"github.com/shridarpatil/whatomate/internal/chatbot/variable"
)

// Engine is the composition root for all chatbot refactor modules (Phases 1–18).
type Engine struct {
	Flags        chatbotconfig.Resolver
	Idempotency  idempotency.Store
	Lock         lock.Locker
	Sessions     *session.Manager
	Conversation *conversation.Manager
	Orchestrator *orchestrator.Orchestrator
	Interactive  *interactive.Engine
	Planner      *planner.Engine
	Keywords     *keyword.Engine
	Variables    *variable.Engine
	Nodes        *nodes.Registry
	Events       *events.Bus
	// External dependency circuit breakers (Phase 17)
	RAGBreaker *lock.CircuitBreaker
	LLMBreaker *lock.CircuitBreaker
}

// NewEngine constructs an Engine with the given flag resolver.
func NewEngine(flags chatbotconfig.Resolver) *Engine {
	if flags == nil {
		flags = chatbotconfig.NewStaticResolver(chatbotconfig.Flags{})
	}
	return &Engine{
		Flags:     flags,
		Keywords:  keyword.NewEngine(),
		Variables: variable.NewEngine(),
		Nodes:     nodes.NewRegistry(),
		Events:    events.NewBus(),
		RAGBreaker: lock.NewCircuitBreaker(5, 0),
		LLMBreaker: lock.NewCircuitBreaker(5, 0),
	}
}

// UseLegacyPath reports whether inbound should call processIncomingMessageFull
// directly (true) or go through Orchestrator.HandleTurn (false).
func (e *Engine) UseLegacyPath(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil {
		return true
	}
	if !e.Flags.Enabled(chatbotconfig.FlagOrchestratorV2, scope) {
		return true
	}
	if e.Orchestrator == nil || e.Orchestrator.Legacy == nil {
		return true
	}
	return false
}

// ShadowCompareEnabled reports whether shadow routing plans should be logged.
func (e *Engine) ShadowCompareEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagShadowCompareV1, scope)
}

// WaitContractEnabled reports whether WaitContract persist/clear is active.
func (e *Engine) WaitContractEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil || e.Interactive == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagWaitContractV1, scope)
}

// TitleMatchEnabled reports whether typed button titles resolve to option ids.
func (e *Engine) TitleMatchEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil || e.Interactive == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagInteractiveTitleMatchV1, scope)
}

// IdempotencyEnabled reports whether WAMID at-most-once is active for scope.
func (e *Engine) IdempotencyEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil || e.Idempotency == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagIdempotencyV1, scope)
}

// SessionLockEnabled reports whether per-session Redis lock is active for scope.
func (e *Engine) SessionLockEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil || e.Lock == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagSessionLockV1, scope)
}

// PlannerEnabled reports whether response planner dedupe is active.
func (e *Engine) PlannerEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil || e.Planner == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagResponsePlannerV1, scope)
}

// AIPipelineEnabled reports whether knowledge→RAG→LLM plan is active.
func (e *Engine) AIPipelineEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagAIPipelineV1, scope)
}

// FlowVersionsEnabled reports whether flow version pinning is active.
func (e *Engine) FlowVersionsEnabled(scope chatbotconfig.Scope) bool {
	if e == nil || e.Flags == nil {
		return false
	}
	return e.Flags.Enabled(chatbotconfig.FlagFlowVersionsV1, scope)
}
