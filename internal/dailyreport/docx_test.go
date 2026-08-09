package dailyreport

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDOCX_WithChats(t *testing.T) {
	chats := []ContactChat{
		{
			Serial: 1, Name: "Ravi Kumar", Phone: "919876543210", ChatDate: "2026-07-31",
			Messages: []ChatMessage{{Direction: "incoming", At: "09:12", Text: "Sandalwood cost?"}},
		},
		{
			Serial: 2, Name: "Asha Patel", Phone: "919812345678", ChatDate: "2026-07-31",
			Messages: []ChatMessage{{Direction: "incoming", At: "10:05", Text: "IoT pricing?"}},
		},
	}
	data := FallbackSummarize("2026-07-31", "Darvi Group", chats)
	data.GeneratedAt = "2026-07-31 20:00"
	// Ensure model labels never leak into DOCX content paths we care about
	data.AIModel = "openrouter:secret-model"
	data.AIUsed = true

	docx, err := BuildDOCX(data)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(docx, []byte("PK")))

	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	require.NoError(t, err)
	var docXML string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			require.NoError(t, err)
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(rc)
			_ = rc.Close()
			docXML = buf.String()
		}
	}
	require.Contains(t, docXML, "Darvi Group")
	require.Contains(t, docXML, "Ravi Kumar")
	require.Contains(t, docXML, "Daily Chat Report")
	require.Contains(t, docXML, "Sandalwood") // summary / msg excerpt must appear
	require.Contains(t, docXML, "w:tbl")
	require.NotContains(t, docXML, "openrouter")
	require.NotContains(t, docXML, "secret-model")
}

func TestEnsureFilledSummaries_FillsBlankBullets(t *testing.T) {
	chats := []ContactChat{
		{
			Serial: 1, Name: "Asha", Phone: "911", ChatDate: "2026-07-31",
			Messages: []ChatMessage{
				{Direction: "incoming", At: "10:00", Text: "Need pricing for IoT"},
				{Direction: "outgoing", At: "10:01", Text: "Sure, sending quote"},
			},
		},
	}
	// Simulate AI returning empty rows / blank bullets (AI path → no raw Msgs dual render)
	data := ReportData{
		ReportDate: "2026-07-31",
		OrgName:    "Org",
		AIUsed:     true,
		Chats: []ChatSummary{
			{Serial: 1, Name: "Asha", Phone: "911", Date: "2026-07-31", Bullets: nil, Summary: "", Excerpts: []string{"in 10:00: Need pricing for IoT"}},
		},
	}
	filled := EnsureFilledSummaries(data, chats)
	require.Len(t, filled.Chats, 1)
	require.NotEmpty(t, filled.Chats[0].Bullets)
	require.Contains(t, filled.Chats[0].Bullets[0], "pricing")
	// AI path: excerpts cleared so DOCX shows summary only
	require.Empty(t, filled.Chats[0].Excerpts)

	docx, err := BuildDOCX(filled)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(docx, []byte("PK")))
	// Summary only — no dual "Msgs:" dump
	require.NotContains(t, string(mustDOCXXML(t, docx)), "Msgs:")
}

func mustDOCXXML(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			require.NoError(t, err)
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(rc)
			_ = rc.Close()
			return buf.String()
		}
	}
	t.Fatal("document.xml missing")
	return ""
}

func TestFormatSummaryCell_NoDualRender(t *testing.T) {
	// When bullets exist, ignore excerpts completely
	cell := formatSummaryCell(ChatSummary{
		Bullets:  []string{"Customer asked about sandalwood"},
		Excerpts: []string{"in 12:47: hi", "out 12:47: Hello welcome..."},
	})
	require.Contains(t, cell, "sandalwood")
	require.NotContains(t, cell, "Msgs:")
	require.NotContains(t, cell, "12:47")
	require.NotContains(t, cell, "hi")
}

func TestBuildDOCX_Empty(t *testing.T) {
	docx, err := BuildDOCX(ReportData{
		ReportDate:  "2026-07-31",
		GeneratedAt: "2026-07-31 20:00",
		OrgName:     "Test Org",
		EmptyDay:    true,
	})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(docx, []byte("PK")))
}
