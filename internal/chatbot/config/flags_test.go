package config_test

import (
	"testing"

	"github.com/google/uuid"
	chatbotconfig "github.com/shridarpatil/whatomate/internal/chatbot/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlags_ZeroMeansAllOff(t *testing.T) {
	var f chatbotconfig.Flags
	assert.False(t, f.AnyEnabled())
	for _, name := range chatbotconfig.AllFlagNames {
		assert.False(t, f.Get(name), name)
	}
}

func TestFlags_GetKnownAndUnknown(t *testing.T) {
	f := chatbotconfig.Flags{OrchestratorV2: true, AIPipelineV1: true}
	assert.True(t, f.Get(chatbotconfig.FlagOrchestratorV2))
	assert.True(t, f.Get(chatbotconfig.FlagAIPipelineV1))
	assert.False(t, f.Get(chatbotconfig.FlagIdempotencyV1))
	assert.False(t, f.Get("chatbot.unknown"))
	assert.True(t, f.AnyEnabled())
}

func TestStaticResolver_GlobalDefaults(t *testing.T) {
	r := chatbotconfig.NewStaticResolver(chatbotconfig.Flags{IdempotencyV1: true})
	require.NotNil(t, r)

	assert.True(t, r.Enabled(chatbotconfig.FlagIdempotencyV1, chatbotconfig.Scope{}))
	assert.False(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{}))
	assert.True(t, r.Global().IdempotencyV1)
}

func TestStaticResolver_OrgAndAccountOverride(t *testing.T) {
	org := uuid.New()
	r := chatbotconfig.NewStaticResolver(chatbotconfig.Flags{})

	// Global off
	assert.False(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{
		OrganizationID: org, WhatsAppAccount: "main",
	}))

	// Org enables orchestrator
	r.SetOrgOverride(org, chatbotconfig.Flags{OrchestratorV2: true})
	assert.True(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{
		OrganizationID: org, WhatsAppAccount: "main",
	}))
	// Other org still off
	assert.False(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{
		OrganizationID: uuid.New(),
	}))

	// Account override replaces org snapshot entirely
	r.SetAccountOverride(org, "main", chatbotconfig.Flags{SessionLockV1: true})
	assert.False(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{
		OrganizationID: org, WhatsAppAccount: "main",
	}))
	assert.True(t, r.Enabled(chatbotconfig.FlagSessionLockV1, chatbotconfig.Scope{
		OrganizationID: org, WhatsAppAccount: "main",
	}))
	// Different account still uses org override
	assert.True(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{
		OrganizationID: org, WhatsAppAccount: "other",
	}))
}

func TestStaticResolver_NilSafe(t *testing.T) {
	var r *chatbotconfig.StaticResolver
	assert.False(t, r.Enabled(chatbotconfig.FlagOrchestratorV2, chatbotconfig.Scope{}))
	assert.Equal(t, chatbotconfig.Flags{}, r.Global())
}
