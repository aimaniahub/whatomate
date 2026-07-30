package dailyreport

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPDF_EmptyDay(t *testing.T) {
	pdf, err := BuildPDF(ReportData{
		ReportDate: "2026-07-31",
		OrgName:    "Test Org",
		EmptyDay:   true,
		Overview:   DayOverview{Notes: "No chats"},
	})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-1.4")))
	require.Contains(t, string(pdf), "No chats today")
}

func TestBuildPDF_WithChats(t *testing.T) {
	pdf, err := BuildPDF(ReportData{
		ReportDate: "2026-07-31",
		OrgName:    "Darvi",
		Overview: DayOverview{
			TotalChats:    1,
			NeedsCallback: 1,
			TopIntents:    []string{"pricing"},
			Notes:         "Busy day",
		},
		Chats: []ChatSummary{
			{
				Name:          "Ravi",
				Phone:         "+919876543210",
				Summary:       "Asked about sandalwood pricing",
				Intent:        "pricing",
				CallRequested: true,
				Priority:      "high",
				NextAction:    "Call back",
			},
		},
	})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-1.4")))
	require.Contains(t, string(pdf), "Ravi")
	require.Contains(t, string(pdf), "tel:")
}

func TestFallbackSummarize_Empty(t *testing.T) {
	r := FallbackSummarize("2026-07-31", "Org", nil)
	require.True(t, r.EmptyDay)
	require.Equal(t, 0, r.Overview.TotalChats)
}

func TestFallbackSummarize_CallIntent(t *testing.T) {
	r := FallbackSummarize("2026-07-31", "Org", []ContactChat{
		{
			Name:  "Asha",
			Phone: "91999",
			Messages: []ChatMessage{
				{Direction: "incoming", Text: "Please call me about pricing"},
			},
		},
	})
	require.False(t, r.EmptyDay)
	require.Equal(t, 1, r.Overview.TotalChats)
	require.True(t, r.Chats[0].CallRequested)
	require.Equal(t, 1, r.Overview.NeedsCallback)
}
