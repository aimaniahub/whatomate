package handlers

import (
	"context"
	"time"

	"github.com/shridarpatil/whatomate/internal/dailyreport"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// DailyReportScheduler periodically runs enabled org daily reports every day
// at the configured local send time (generation starts lead minutes earlier).
type DailyReportScheduler struct {
	app      *App
	interval time.Duration
	stopCh   chan struct{}
}

// NewDailyReportScheduler creates a scheduler (default tick 1 minute).
func NewDailyReportScheduler(app *App, interval time.Duration) *DailyReportScheduler {
	if interval <= 0 {
		interval = time.Minute
	}
	return &DailyReportScheduler{
		app:      app,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the schedule loop.
func (p *DailyReportScheduler) Start(ctx context.Context) {
	p.app.Log.Info("Daily report scheduler started", "interval", p.interval)
	// Catch-up on boot if today's window already opened.
	p.tick()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.app.Log.Info("Daily report scheduler stopped by context")
			return
		case <-p.stopCh:
			p.app.Log.Info("Daily report scheduler stopped")
			return
		case <-ticker.C:
			p.tick()
		}
	}
}

// Stop stops the scheduler.
func (p *DailyReportScheduler) Stop() {
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
}

func (p *DailyReportScheduler) tick() {
	var settings []models.DailyReportSettings
	if err := p.app.DB.Where("enabled = ?", true).Find(&settings).Error; err != nil {
		p.app.Log.Error("Daily report scheduler: load settings failed", "error", err)
		return
	}
	if len(settings) == 0 {
		return
	}

	now := time.Now()
	for i := range settings {
		s := settings[i]
		p.processOrgSchedule(&s, now)
	}
}

func (p *DailyReportScheduler) processOrgSchedule(s *models.DailyReportSettings, now time.Time) {
	if s == nil || !s.Enabled {
		return
	}

	loc := dailyreport.LoadLocation(s.Timezone)
	sendTime := s.SendTime
	if norm, err := dailyreport.NormalizeSendTime(sendTime); err == nil {
		sendTime = norm
		// Heal bad/truncated DB values so schedule keeps working every day.
		if s.SendTime != norm {
			_ = p.app.DB.Model(s).Update("send_time", norm).Error
			s.SendTime = norm
		}
	}

	reportDate := dailyreport.TodayDate(loc)
	if !dailyreport.ShouldRunSchedule(now, loc, sendTime, reportDate) {
		return
	}

	// Source of truth for "already ran today" is the run row (not only settings audit).
	var existing models.DailyReportRun
	err := p.app.DB.Where("organization_id = ? AND report_date = ?", s.OrganizationID, reportDate).
		Order("created_at desc").
		First(&existing).Error
	if err == nil {
		if dailyreport.IsTerminalRunStatus(existing.Status) {
			// Keep settings audit aligned (manual or prior schedule).
			if s.LastScheduledDate != reportDate || s.LastScheduledStatus != existing.Status {
				p.persistScheduleAudit(s, &existing, reportDate)
			}
			return
		}
		if existing.Status == models.DailyReportStatusRunning {
			// Allow reclaim of stuck runs after 30 minutes.
			if existing.StartedAt != nil && time.Since(*existing.StartedAt) < 30*time.Minute {
				return
			}
		}
		// pending/failed → retry via RunDailyReport
	} else if err != gorm.ErrRecordNotFound {
		p.app.Log.Error("Daily report scheduler: load run failed",
			"org", s.OrganizationID, "date", reportDate, "error", err)
		return
	}

	p.app.Log.Info("Daily report schedule firing",
		"org", s.OrganizationID,
		"date", reportDate,
		"send_time", sendTime,
		"timezone", s.Timezone,
	)

	run, runErr := p.app.RunDailyReport(s.OrganizationID, reportDate, models.DailyReportTriggerSchedule, false)
	if runErr != nil {
		p.app.Log.Error("Daily report scheduled run failed",
			"org", s.OrganizationID, "date", reportDate, "error", runErr)
	}
	if run != nil {
		p.persistScheduleAudit(s, run, reportDate)
	}
}

// persistScheduleAudit writes last scheduled fire markers so UI + scheduler stay in sync.
func (p *DailyReportScheduler) persistScheduleAudit(s *models.DailyReportSettings, run *models.DailyReportRun, reportDate string) {
	if s == nil || run == nil {
		return
	}
	now := time.Now()
	updates := map[string]any{
		"last_scheduled_at":     now,
		"last_scheduled_date":   reportDate,
		"last_scheduled_status": run.Status,
		"last_scheduled_run_id": run.ID,
	}
	if err := p.app.DB.Model(&models.DailyReportSettings{}).
		Where("id = ?", s.ID).
		Updates(updates).Error; err != nil {
		p.app.Log.Error("Daily report scheduler: failed to persist schedule audit",
			"org", s.OrganizationID, "error", err)
		return
	}
	s.LastScheduledAt = &now
	s.LastScheduledDate = reportDate
	s.LastScheduledStatus = run.Status
	id := run.ID
	s.LastScheduledRunID = &id
}
