package dailyreport

import (
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

// ScheduleAction is what the daily-report ticker should do for one org.
type ScheduleAction string

const (
	// ScheduleSkip — already have a usable report, or a genuine empty day after send time.
	ScheduleSkip ScheduleAction = "skip"
	// ScheduleWait — prepare window opened but no chats yet; do not lock an empty day.
	ScheduleWait ScheduleAction = "wait"
	// ScheduleRun — first fire (or retry pending/failed).
	ScheduleRun ScheduleAction = "run"
	// ScheduleForce — regenerate (blank/unusable file, or empty run that now has chats).
	ScheduleForce ScheduleAction = "force"
)

// ExistingRun is the scheduler's view of today's run row (no DB types).
type ExistingRun struct {
	Status     string
	ChatCount  int
	StartedAt  *time.Time
	FinishedAt *time.Time
	Usable     bool
	// ProbeChatCount is how many contacts collect finds *now*.
	// <0 means the caller did not probe (only needed for empty/zero runs).
	ProbeChatCount int
}

// DecideScheduleAction chooses skip / wait / run / force.
//
// Caller must only invoke this when ShouldRunSchedule is already true
// (local now is at or after send_time − lead).
//
// Rules that used to break scheduled reports:
//   - status=empty was treated as "done forever", so a 0-chat fire at lead
//     time (or a timezone miss) blocked the real report for the rest of the day.
//   - Manual "Run now" uses force=true, which is why it still worked.
func DecideScheduleAction(now time.Time, loc *time.Location, sendTime, reportDate string, existing *ExistingRun) ScheduleAction {
	if loc == nil {
		loc = LoadLocation("")
	}
	if now.In(loc).Format("2006-01-02") != reportDate {
		return ScheduleSkip
	}
	if existing == nil {
		return ScheduleRun
	}

	switch existing.Status {
	case models.DailyReportStatusRunning:
		if existing.StartedAt != nil && now.Sub(*existing.StartedAt) < 45*time.Minute {
			return ScheduleSkip
		}
		return ScheduleForce

	case models.DailyReportStatusCompleted:
		if existing.Usable && existing.ChatCount > 0 {
			return ScheduleSkip
		}
		if existing.ChatCount == 0 {
			return decideEmptyOrZero(now, loc, sendTime, existing)
		}
		// Completed but file/summary missing — regenerate.
		return ScheduleForce

	case models.DailyReportStatusEmpty:
		return decideEmptyOrZero(now, loc, sendTime, existing)

	default:
		// pending / failed / unknown → retry
		return ScheduleRun
	}
}

func decideEmptyOrZero(now time.Time, loc *time.Location, sendTime string, existing *ExistingRun) ScheduleAction {
	sendAt, err := SendAt(now, loc, sendTime)
	if err != nil {
		// Can't parse send time — still try to produce a report.
		if existing != nil && existing.ProbeChatCount > 0 {
			return ScheduleForce
		}
		return ScheduleRun
	}
	local := now.In(loc)
	if local.Before(sendAt) {
		// Lead window: do not finalize an empty day; wait until send time
		// so late-afternoon chats (and a second collect) still count.
		if existing != nil && existing.ProbeChatCount > 0 {
			return ScheduleForce
		}
		return ScheduleWait
	}
	// At/after send time: regenerate only when chats exist now.
	if existing != nil && existing.ProbeChatCount > 0 {
		return ScheduleForce
	}
	return ScheduleSkip
}

// CollectSQLWindow is a padded [start,end) used for the SQL scan so we still
// see rows whether created_at was stored as timestamptz UTC or as a naive
// local wall clock. Callers must then apply FilterRowsForReportDate.
func CollectSQLWindow(reportDate string, loc *time.Location) (sqlStart, sqlEnd, dayStartLocal, dayEndLocal time.Time, err error) {
	if loc == nil {
		loc = LoadLocation("")
	}
	t, err := time.ParseInLocation("2006-01-02", reportDate, loc)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, err
	}
	dayStartLocal = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	dayEndLocal = dayStartLocal.Add(24 * time.Hour)
	const pad = 14 * time.Hour // wider than any civil timezone offset
	return dayStartLocal.Add(-pad), dayEndLocal.Add(pad), dayStartLocal, dayEndLocal, nil
}

// InstantOnReportDate is true when the timestamp, interpreted as an instant
// and converted to loc, falls on reportDate.
func InstantOnReportDate(ts time.Time, reportDate string, loc *time.Location) bool {
	if ts.IsZero() || loc == nil {
		return false
	}
	return ts.In(loc).Format("2006-01-02") == reportDate
}

// NaiveWallClockOnReportDate is true when the timestamp's Y-M-D numbers
// (ignoring location) equal reportDate. This recovers rows stored as
// timestamp-without-time-zone in the org's local wall clock.
func NaiveWallClockOnReportDate(ts time.Time, reportDate string) bool {
	if ts.IsZero() {
		return false
	}
	return ts.Format("2006-01-02") == reportDate
}

// PreferInstantDateFilter reports whether the primary (timezone-correct)
// instant filter found any rows. Used by collect to pick a strategy.
func PreferInstantDateFilter(instantHits, naiveHits int) bool {
	return instantHits > 0
}

// DateFilterLabel is a short diagnostic tag for logs / empty-day notes.
func DateFilterLabel(usedNaive bool) string {
	if usedNaive {
		return "naive_wall_clock"
	}
	return "timezone_instant"
}

// NormalizeAccountFilter returns the trimmed send-account name, or "" to
// mean "all WhatsApp accounts in the org".
func NormalizeAccountFilter(name string) string {
	return strings.TrimSpace(name)
}
