package dailyreport

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

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

// ParseSendTime parses HH:MM into hour and minute.
func ParseSendTime(sendTime string) (hour, minute int, err error) {
	sendTime = strings.TrimSpace(sendTime)
	if sendTime == "" {
		sendTime = models.DefaultDailyReportSendTime
	}
	parts := strings.Split(sendTime, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("send_time must be HH:MM")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid send_time %q", sendTime)
	}
	return h, m, nil
}

// ShouldRunSchedule reports whether local now is past send_time for reportDate day
// and the job has not been completed yet (caller checks DB).
func ShouldRunSchedule(now time.Time, loc *time.Location, sendTime, reportDate string) bool {
	if loc == nil {
		loc = LoadLocation("")
	}
	local := now.In(loc)
	if local.Format("2006-01-02") != reportDate {
		return false
	}
	h, m, err := ParseSendTime(sendTime)
	if err != nil {
		return false
	}
	sendAt := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	return !local.Before(sendAt)
}
