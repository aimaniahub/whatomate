package handlers

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestIsUsableDailyReportRun_EmptyIsNeverUsable(t *testing.T) {
	// Regression: empty used to count as usable, so the scheduler never
	// regenerated after a 0-chat fire. Manual Run now (force=true) still worked.
	run := &models.DailyReportRun{
		Status:      models.DailyReportStatusEmpty,
		ChatCount:   0,
		PDFPath:     "/tmp/empty.docx",
		PDFFilename: "daily-chat-report-2026-08-18.docx",
	}
	assert.False(t, isUsableDailyReportRun(run))
}

func TestIsUsableDailyReportRun_NilAndNonTerminal(t *testing.T) {
	assert.False(t, isUsableDailyReportRun(nil))
	assert.False(t, isUsableDailyReportRun(&models.DailyReportRun{Status: models.DailyReportStatusPending}))
	assert.False(t, isUsableDailyReportRun(&models.DailyReportRun{Status: models.DailyReportStatusRunning}))
}

func TestIsUsableDailyReportRun_CompletedMissingFile(t *testing.T) {
	run := &models.DailyReportRun{
		Status:      models.DailyReportStatusCompleted,
		ChatCount:   4,
		PDFPath:     "",
		PDFFilename: "",
	}
	assert.False(t, isUsableDailyReportRun(run))
}

func TestExtractMessageText_PrefersContent(t *testing.T) {
	got := extractMessageText("Hello there", "text", "", "", nil)
	assert.Equal(t, "Hello there", got)
}

func TestExtractMessageText_InteractiveBody(t *testing.T) {
	got := extractMessageText("", "interactive", "", "", []byte(`{"body":"Pick an option"}`))
	assert.Equal(t, "Pick an option", got)
}

func TestExtractMessageText_MediaFallback(t *testing.T) {
	got := extractMessageText("", "image", "", "", nil)
	assert.Equal(t, "[image]", got)
}
