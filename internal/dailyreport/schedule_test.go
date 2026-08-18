package dailyreport

import (
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideScheduleAction_WrongCalendarDay_Skip(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 19, 0, 10, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", nil)
	assert.Equal(t, ScheduleSkip, got)
}

func TestDecideScheduleAction_NoExisting_Runs(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 19, 55, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", nil)
	assert.Equal(t, ScheduleRun, got)
}

func TestDecideScheduleAction_CompletedWithChats_Skip(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 20, 10, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:    models.DailyReportStatusCompleted,
		ChatCount: 12,
		Usable:    true,
	})
	assert.Equal(t, ScheduleSkip, got)
}

func TestDecideScheduleAction_EmptyBeforeSendTime_Wait(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 19, 52, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:         models.DailyReportStatusEmpty,
		ChatCount:      0,
		Usable:         false,
		ProbeChatCount: 0,
	})
	assert.Equal(t, ScheduleWait, got)
}

func TestDecideScheduleAction_EmptyBeforeSendTime_ForceWhenChatsFound(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 19, 52, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:         models.DailyReportStatusEmpty,
		ChatCount:      0,
		ProbeChatCount: 7,
	})
	assert.Equal(t, ScheduleForce, got)
}

func TestDecideScheduleAction_EmptyAfterSendTime_ForceWhenChatsNowExist(t *testing.T) {
	// This is the production bug: schedule locked status=empty at fire time,
	// then never regenerated even though the inbox had chats (manual force worked).
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 20, 15, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:         models.DailyReportStatusEmpty,
		ChatCount:      0,
		Usable:         false,
		ProbeChatCount: 15,
	})
	assert.Equal(t, ScheduleForce, got)
}

func TestDecideScheduleAction_EmptyAfterSendTime_SkipWhenStillNoChats(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 20, 15, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:         models.DailyReportStatusEmpty,
		ChatCount:      0,
		ProbeChatCount: 0,
	})
	assert.Equal(t, ScheduleSkip, got)
}

func TestDecideScheduleAction_CompletedUnusable_Force(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 20, 5, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:    models.DailyReportStatusCompleted,
		ChatCount: 9,
		Usable:    false,
	})
	assert.Equal(t, ScheduleForce, got)
}

func TestDecideScheduleAction_Pending_Run(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 8, 18, 20, 0, 0, 0, loc)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status: models.DailyReportStatusPending,
	})
	assert.Equal(t, ScheduleRun, got)
}

func TestDecideScheduleAction_FreshRunning_Skip(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	started := time.Date(2026, 8, 18, 19, 55, 0, 0, loc)
	now := started.Add(5 * time.Minute)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:    models.DailyReportStatusRunning,
		StartedAt: &started,
	})
	assert.Equal(t, ScheduleSkip, got)
}

func TestDecideScheduleAction_StaleRunning_Force(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	started := time.Date(2026, 8, 18, 18, 0, 0, 0, loc)
	now := started.Add(50 * time.Minute)
	got := DecideScheduleAction(now, loc, "20:00", "2026-08-18", &ExistingRun{
		Status:    models.DailyReportStatusRunning,
		StartedAt: &started,
	})
	assert.Equal(t, ScheduleForce, got)
}

func TestCollectSQLWindow_PadsAroundLocalDay(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	sqlStart, sqlEnd, dayStart, dayEnd, err := CollectSQLWindow("2026-08-18", loc)
	require.NoError(t, err)
	assert.Equal(t, "2026-08-18 00:00", dayStart.Format("2006-01-02 15:04"))
	assert.Equal(t, "2026-08-19 00:00", dayEnd.Format("2006-01-02 15:04"))
	assert.True(t, sqlStart.Before(dayStart))
	assert.True(t, sqlEnd.After(dayEnd))
	assert.Equal(t, 14*time.Hour, dayStart.Sub(sqlStart))
	assert.Equal(t, 14*time.Hour, sqlEnd.Sub(dayEnd))
}

func TestInstantOnReportDate_IST(t *testing.T) {
	loc := LoadLocation("Asia/Kolkata")
	// 2026-08-17 18:30 UTC = 2026-08-18 00:00 IST
	ts := time.Date(2026, 8, 17, 18, 30, 0, 0, time.UTC)
	assert.True(t, InstantOnReportDate(ts, "2026-08-18", loc))
	assert.False(t, InstantOnReportDate(ts, "2026-08-17", loc))
}

func TestNaiveWallClockOnReportDate(t *testing.T) {
	// Stored as naive local wall clock, loaded as UTC with the same numbers.
	ts := time.Date(2026, 8, 18, 21, 15, 0, 0, time.UTC)
	assert.True(t, NaiveWallClockOnReportDate(ts, "2026-08-18"))
	assert.False(t, NaiveWallClockOnReportDate(ts, "2026-08-19"))
}

func TestPreferInstantDateFilter(t *testing.T) {
	assert.True(t, PreferInstantDateFilter(4, 4))
	assert.True(t, PreferInstantDateFilter(1, 0))
	assert.False(t, PreferInstantDateFilter(0, 9))
	assert.False(t, PreferInstantDateFilter(0, 0))
}
