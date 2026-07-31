package dailyreport

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildPDF_EmptyDay(t *testing.T) {
	pdf, err := BuildPDF(ReportData{
		ReportDate:  "2026-07-31",
		GeneratedAt: "2026-07-31 20:00",
		OrgName:     "Test Org",
		EmptyDay:    true,
		Overview:    DayOverview{Notes: "No chats"},
	})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-1.4")))
	require.Contains(t, string(pdf), "No chats today")
	require.Contains(t, string(pdf), "Test Org")
	// A4 width present in MediaBox
	require.Contains(t, string(pdf), "595.28")
}

func TestBuildPDF_WithChats(t *testing.T) {
	pdf, err := BuildPDF(ReportData{
		ReportDate:  "2026-07-31",
		GeneratedAt: "2026-07-31 20:05",
		OrgName:     "Darvi",
		AIUsed:      true,
		AIModel:     "openrouter:test",
		Overview: DayOverview{
			TotalChats: 1,
			Notes:      "AI summarized 1 chats",
		},
		Chats: []ChatSummary{
			{
				Serial:  1,
				Date:    "2026-07-31",
				Name:    "Ravi",
				Phone:   "919876543210",
				Bullets: []string{"Asked sandalwood pricing", "Wants callback", "Interested in bulk"},
				Summary: "Asked sandalwood pricing • Wants callback • Interested in bulk",
			},
		},
	})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-1.4")))
	require.Contains(t, string(pdf), "Ravi")
	require.Contains(t, string(pdf), "919876543210")
	require.Contains(t, string(pdf), "Daily Chat Report")
}

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
	require.Equal(t, "911", r.Chats[0].Phone)
	require.Equal(t, []string{"Asked about price"}, r.Chats[0].Bullets)
	require.Equal(t, 2, r.Chats[1].Serial)
	require.Equal(t, "Ravi", r.Chats[1].Name)
	require.Len(t, r.Chats[1].Bullets, 2)
}

func TestShouldRunSchedule_Lead10(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	// 19:50 IST on a fixed day with send 20:00 → lead 10 → prepare at 19:50 → true
	now := timeInLoc(loc, 2026, 7, 31, 19, 50)
	require.True(t, ShouldRunSchedule(now, loc, "20:00", "2026-07-31"))
	// 19:49 → false
	nowEarly := timeInLoc(loc, 2026, 7, 31, 19, 49)
	require.False(t, ShouldRunSchedule(nowEarly, loc, "20:00", "2026-07-31"))
}

func timeInLoc(loc *time.Location, y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, loc)
}
