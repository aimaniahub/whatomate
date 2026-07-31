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
	require.Contains(t, docXML, "w:tbl")
	require.NotContains(t, docXML, "openrouter")
	require.NotContains(t, docXML, "secret-model")
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
