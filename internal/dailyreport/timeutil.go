package dailyreport

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

// ScheduleLeadMinutes is how early the scheduler starts generation before send_time.
const ScheduleLeadMinutes = 10

// LoadLocation resolves the org timezone (defaults to Asia/Kolkata).
func LoadLocation(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		tz = models.DefaultDailyReportTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation(models.DefaultDailyReportTimezone)
		if loc == nil {
			return time.FixedZone("IST", 5*3600+30*60)
		}
	}
	return loc
}

// DayBounds returns UTC instants for the local calendar day of reportDate (YYYY-MM-DD).
func DayBounds(reportDate string, loc *time.Location) (startUTC, endUTC time.Time, err error) {
	if loc == nil {
		loc = LoadLocation("")
	}
	t, err := time.ParseInLocation("2006-01-02", reportDate, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid report date: %w", err)
	}
	startLocal := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	endLocal := startLocal.Add(24 * time.Hour)
	return startLocal.UTC(), endLocal.UTC(), nil
}

// TodayDate returns YYYY-MM-DD in the given location.
func TodayDate(loc *time.Location) string {
	if loc == nil {
		loc = LoadLocation("")
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// NormalizeSendTime returns HH:MM. Accepts "H:MM", "HH:MM", "HH:MM:SS".
func NormalizeSendTime(sendTime string) (string, error) {
	h, m, err := ParseSendTime(sendTime)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%02d:%02d", h, m), nil
}

// ParseSendTime parses HH:MM or HH:MM:SS into hour and minute.
func ParseSendTime(sendTime string) (hour, minute int, err error) {
	sendTime = strings.TrimSpace(sendTime)
	if sendTime == "" {
		sendTime = models.DefaultDailyReportSendTime
	}
	// Browsers often send "20:00:00" from <input type="time">
	parts := strings.Split(sendTime, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, fmt.Errorf("send_time must be HH:MM")
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid send_time %q", sendTime)
	}
	if len(parts) == 3 {
		if _, err3 := strconv.Atoi(strings.TrimSpace(parts[2])); err3 != nil {
			return 0, 0, fmt.Errorf("invalid send_time %q", sendTime)
		}
	}
	return h, m, nil
}

// PrepareAt returns the local time when the job should start (send_time - lead).
func PrepareAt(nowBase time.Time, loc *time.Location, sendTime string, leadMinutes int) (time.Time, error) {
	if loc == nil {
		loc = LoadLocation("")
	}
	if leadMinutes < 0 {
		leadMinutes = ScheduleLeadMinutes
	}
	local := nowBase.In(loc)
	h, m, err := ParseSendTime(sendTime)
	if err != nil {
		return time.Time{}, err
	}
	sendAt := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	return sendAt.Add(-time.Duration(leadMinutes) * time.Minute), nil
}

// SendAt returns today's send_time as a local timestamp.
func SendAt(nowBase time.Time, loc *time.Location, sendTime string) (time.Time, error) {
	if loc == nil {
		loc = LoadLocation("")
	}
	local := nowBase.In(loc)
	h, m, err := ParseSendTime(sendTime)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc), nil
}

// NextFireTime returns the next prepare/fire instant for an enabled daily schedule.
// If today's prepare time has not passed, returns today's prepare; else tomorrow's.
func NextFireTime(now time.Time, loc *time.Location, sendTime string, leadMinutes int) (time.Time, error) {
	if loc == nil {
		loc = LoadLocation("")
	}
	if leadMinutes < 0 {
		leadMinutes = ScheduleLeadMinutes
	}
	local := now.In(loc)
	prep, err := PrepareAt(local, loc, sendTime, leadMinutes)
	if err != nil {
		return time.Time{}, err
	}
	if local.Before(prep) {
		return prep, nil
	}
	// Tomorrow
	tomorrow := local.Add(24 * time.Hour)
	return PrepareAt(tomorrow, loc, sendTime, leadMinutes)
}

// ShouldRunSchedule reports whether local now is past prepare time (send_time - lead)
// for reportDate day. Caller still checks DB for already-completed runs.
func ShouldRunSchedule(now time.Time, loc *time.Location, sendTime, reportDate string) bool {
	return ShouldRunScheduleLead(now, loc, sendTime, reportDate, ScheduleLeadMinutes)
}

// ShouldRunScheduleLead is like ShouldRunSchedule with explicit lead minutes.
func ShouldRunScheduleLead(now time.Time, loc *time.Location, sendTime, reportDate string, leadMinutes int) bool {
	if loc == nil {
		loc = LoadLocation("")
	}
	local := now.In(loc)
	if local.Format("2006-01-02") != reportDate {
		return false
	}
	prepareAt, err := PrepareAt(now, loc, sendTime, leadMinutes)
	if err != nil {
		return false
	}
	return !local.Before(prepareAt)
}

// IsTerminalRunStatus is true when a scheduled day should not re-fire.
func IsTerminalRunStatus(status string) bool {
	switch status {
	case models.DailyReportStatusCompleted, models.DailyReportStatusEmpty:
		return true
	default:
		return false
	}
}
