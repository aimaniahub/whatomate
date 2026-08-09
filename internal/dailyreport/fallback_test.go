package dailyreport

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFallbackSummarize_Empty(t *testing.T) {
	r := FallbackSummarize("2026-07-31", "Org", nil)
	require.True(t, r.EmptyDay)
	require.Equal(t, 0, r.Overview.TotalChats)
}

func TestMergeAISummaries_ByID(t *testing.T) {
	chats := []ContactChat{
		{Serial: 1, Name: "Asha", Phone: "911", ChatDate: "2026-07-31", Messages: []ChatMessage{{Direction: "incoming", Text: "price?"}}},
		{Serial: 2, Name: "Ravi", Phone: "922", ChatDate: "2026-07-31", Messages: []ChatMessage{{Direction: "incoming", Text: "call me"}}},
	}
	r := MergeAISummaries("2026-07-31", "Org", "2026-07-31 20:00", "openrouter:m", chats, []AISummaryItem{
		{ID: 2, Bullets: []string{"Requested a call", "About product"}},
		{ID: 1, Bullets: []string{"Asked about price"}},
	})
	require.True(t, r.AIUsed)
	require.Len(t, r.Chats, 2)
	require.Equal(t, "Asha", r.Chats[0].Name)
	require.Equal(t, []string{"Asked about price"}, r.Chats[0].Bullets)
	require.Equal(t, 2, r.Chats[1].Serial)
	require.Len(t, r.Chats[1].Bullets, 2)
	// AI success: no raw transcript dual-render
	require.Empty(t, r.Chats[0].Excerpts)
	require.Empty(t, r.Chats[1].Excerpts)
}

func TestMergeAISummaries_EmptyAIUsesTranscript(t *testing.T) {
	chats := []ContactChat{
		{Serial: 1, Name: "Asha", Phone: "911", ChatDate: "2026-07-31",
			Messages: []ChatMessage{{Direction: "incoming", Text: "Sandalwood cost?"}}},
	}
	r := MergeAISummaries("2026-07-31", "Org", "now", "m", chats, []AISummaryItem{
		{ID: 1, Bullets: []string{"", "  "}},
	})
	require.NotEmpty(t, r.Chats[0].Bullets)
	require.Contains(t, r.Chats[0].Bullets[0], "Sandalwood")
	require.Empty(t, r.Chats[0].Excerpts)
}

func TestBulletsFromMessages_FallbackOutgoing(t *testing.T) {
	msgs := []ChatMessage{{Direction: "outgoing", Text: "Your order is ready"}}
	b := BulletsFromMessages(msgs, 3)
	require.Len(t, b, 1)
	require.Contains(t, b[0], "order is ready")
}
