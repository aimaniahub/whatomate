package dailyreport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSendTime_Variants(t *testing.T) {
	h, m, err := ParseSendTime("20:00")
	require.NoError(t, err)
	assert.Equal(t, 20, h)
	assert.Equal(t, 0, m)

	// Browser <input type="time"> often includes seconds
	h, m, err = ParseSendTime("20:00:00")
	require.NoError(t, err)
	assert.Equal(t, 20, h)
	assert.Equal(t, 0, m)

	h, m, err = ParseSendTime("9:05")
	require.NoError(t, err)
	assert.Equal(t, 9, h)
	assert.Equal(t, 5, m)

	norm, err := NormalizeSendTime("9:05:30")
	require.NoError(t, err)
	assert.Equal(t, "09:05", norm)
}

func TestShouldRunSchedule_Lead10(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 7, 31, 19, 50, 0, 0, loc)
	require.True(t, ShouldRunSchedule(now, loc, "20:00", "2026-07-31"))
	require.True(t, ShouldRunSchedule(now, loc, "20:00:00", "2026-07-31"))

	nowEarly := time.Date(2026, 7, 31, 19, 49, 0, 0, loc)
	require.False(t, ShouldRunSchedule(nowEarly, loc, "20:00", "2026-07-31"))
}

func TestNextFireTime(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	// Before prepare (19:50 for send 20:00)
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, loc)
	next, err := NextFireTime(now, loc, "20:00", 10)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-31 19:50", next.Format("2006-01-02 15:04"))

	// After prepare → tomorrow
	nowLate := time.Date(2026, 7, 31, 20, 5, 0, 0, loc)
	next, err = NextFireTime(nowLate, loc, "20:00", 10)
	require.NoError(t, err)
	assert.Equal(t, "2026-08-01 19:50", next.Format("2006-01-02 15:04"))
}

func TestIsTerminalRunStatus(t *testing.T) {
	assert.True(t, IsTerminalRunStatus("completed"))
	assert.True(t, IsTerminalRunStatus("empty"))
	assert.False(t, IsTerminalRunStatus("failed"))
	assert.False(t, IsTerminalRunStatus("running"))
}
