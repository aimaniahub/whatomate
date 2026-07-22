package chatbot_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/chatbot"
	chatbotconfig "github.com/shridarpatil/whatomate/internal/chatbot/config"
	"github.com/stretchr/testify/assert"
)

func TestEngine_UseLegacyPathWithoutOrchestrator(t *testing.T) {
	// Flag on but no orchestrator wired → still legacy direct.
	r := chatbotconfig.NewStaticResolver(chatbotconfig.Flags{OrchestratorV2: true})
	eng := chatbot.NewEngine(r)
	assert.True(t, eng.UseLegacyPath(chatbotconfig.Scope{
		OrganizationID: uuid.New(), WhatsAppAccount: "wa",
	}))
}

func TestEngine_UseLegacyPathFlagOff(t *testing.T) {
	r := chatbotconfig.NewStaticResolver(chatbotconfig.Flags{})
	eng := chatbot.NewEngine(r)
	assert.True(t, eng.UseLegacyPath(chatbotconfig.Scope{}))
}

func TestEngine_NilFlagsDefaults(t *testing.T) {
	eng := chatbot.NewEngine(nil)
	assert.NotNil(t, eng.Flags)
	assert.True(t, eng.UseLegacyPath(chatbotconfig.Scope{}))
	assert.False(t, eng.Flags.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{}))
}
