package interactive_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/chatbot/interactive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveInput_ButtonID(t *testing.T) {
	opts := []interactive.Option{
		{ID: "en", Title: "English"},
		{ID: "kn", Title: "ಕನ್ನಡ"},
	}
	m := interactive.ResolveInput(opts, "en", "")
	require.NotNil(t, m)
	assert.Equal(t, "en", m.OptionID)
	assert.Equal(t, "button_id", m.Source)
}

func TestResolveInput_Title(t *testing.T) {
	opts := []interactive.Option{
		{ID: "en", Title: "English"},
		{ID: "reg", Title: "Registration Hub"},
	}
	m := interactive.ResolveInput(opts, "", "english")
	require.NotNil(t, m)
	assert.Equal(t, "en", m.OptionID)
	assert.Equal(t, "title", m.Source)

	m = interactive.ResolveInput(opts, "", "  Registration   Hub ")
	require.NotNil(t, m)
	assert.Equal(t, "reg", m.OptionID)
}

func TestResolveInput_Alias(t *testing.T) {
	opts := []interactive.Option{
		{ID: "help", Title: "Support", Aliases: []string{"assist", "help me"}},
	}
	m := interactive.ResolveInput(opts, "", "assist")
	require.NotNil(t, m)
	assert.Equal(t, "help", m.OptionID)
	assert.Equal(t, "alias", m.Source)
}

func TestResolveInput_NoMatch(t *testing.T) {
	opts := []interactive.Option{{ID: "a", Title: "Alpha"}}
	assert.Nil(t, interactive.ResolveInput(opts, "", "beta"))
}

func TestNormalize(t *testing.T) {
	assert.Equal(t, "hello world", interactive.Normalize("  Hello   WORLD  "))
}
