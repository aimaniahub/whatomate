package handlers

import (
	"github.com/redis/go-redis/v9"
	"github.com/shridarpatil/whatomate/internal/chatbot"
	chatbotconfig "github.com/shridarpatil/whatomate/internal/chatbot/config"
	"github.com/shridarpatil/whatomate/internal/chatbot/conversation"
	"github.com/shridarpatil/whatomate/internal/chatbot/events"
	"github.com/shridarpatil/whatomate/internal/chatbot/idempotency"
	"github.com/shridarpatil/whatomate/internal/chatbot/interactive"
	"github.com/shridarpatil/whatomate/internal/chatbot/lock"
	"github.com/shridarpatil/whatomate/internal/chatbot/orchestrator"
	"github.com/shridarpatil/whatomate/internal/chatbot/planner"
	"github.com/shridarpatil/whatomate/internal/chatbot/session"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

// NewChatbotEngine builds the chatbot refactor engine from application config.
// All flags default to false. Modules for phases 1–18 are wired here.
func NewChatbotEngine(cfg *config.Config, db *gorm.DB, rdb *redis.Client, legacy orchestrator.LegacyHandler, log logf.Logger) *chatbot.Engine {
	flags := chatbotconfig.Flags{}
	if cfg != nil {
		flags = ChatbotFlagsFromConfig(cfg.Chatbot)
	}
	eng := chatbot.NewEngine(chatbotconfig.NewStaticResolver(flags))
	if db != nil {
		eng.Idempotency = idempotency.NewPostgresStore(db)
		eng.Sessions = session.NewManager(db)
		eng.Conversation = conversation.NewManager(eng.Sessions)
	}
	if rdb != nil {
		eng.Lock = lock.NewRedisLocker(rdb)
	}
	if legacy != nil {
		orch := orchestrator.New(legacy, log)
		if flags.ShadowCompareV1 {
			orch.ShadowCompare = true
		}
		eng.Orchestrator = orch
	}
	eng.Interactive = interactive.NewEngine(flags.WaitContractV1, flags.InteractiveTitleMatchV1)
	eng.Planner = planner.NewEngine(flags.ResponsePlannerV1)
	// Optional debug subscriber for TurnCompleted etc.
	if eng.Events != nil {
		eng.Events.On(events.TurnCompleted, func(e events.Event) {
			log.Debug("chatbot event", "name", e.Name, "session", e.SessionID.String())
		})
	}
	return eng
}

// ChatbotFlagsFromConfig maps app config to chatbot feature flags.
func ChatbotFlagsFromConfig(c config.ChatbotConfig) chatbotconfig.Flags {
	return chatbotconfig.Flags{
		IdempotencyV1:           c.IdempotencyV1,
		SessionLockV1:           c.SessionLockV1,
		OrchestratorV2:          c.OrchestratorV2,
		WaitContractV1:          c.WaitContractV1,
		InteractiveTitleMatchV1: c.InteractiveTitleMatchV1,
		PriorityLadderV1:        c.PriorityLadderV1,
		AIPipelineV1:            c.AIPipelineV1,
		ResponsePlannerV1:       c.ResponsePlannerV1,
		FlowVersionsV1:          c.FlowVersionsV1,
		ShadowCompareV1:         c.ShadowCompareV1,
	}
}
