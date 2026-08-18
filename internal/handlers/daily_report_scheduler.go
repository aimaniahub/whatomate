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

	var existing models.DailyReportRun
	var existingPtr *dailyreport.ExistingRun
	err := p.app.DB.Where("organization_id = ? AND report_date = ?", s.OrganizationID, reportDate).
		Order("created_at desc").
		First(&existing).Error
	if err == nil {
		probe := -1
		// Empty / zero-chat terminal runs must be re-collected. Manual "Run now"
		// always force-regenerates; schedule used to treat empty as done forever.
		if existing.Status == models.DailyReportStatusEmpty ||
			(dailyreport.IsTerminalRunStatus(existing.Status) && existing.ChatCount == 0) ||
			(dailyreport.IsTerminalRunStatus(existing.Status) && !isUsableDailyReportRun(&existing)) {
			if chats, _, cErr := p.app.collectDailyChats(s.OrganizationID, "", reportDate, loc); cErr != nil {
				p.app.Log.Error("Daily report schedule: probe collect failed",
					"org", s.OrganizationID, "date", reportDate, "error", cErr)
			} else {
				probe = len(chats)
			}
		}
		existingPtr = &dailyreport.ExistingRun{
			Status:         existing.Status,
			ChatCount:      existing.ChatCount,
			StartedAt:      existing.StartedAt,
			FinishedAt:     existing.FinishedAt,
			Usable:         isUsableDailyReportRun(&existing),
			ProbeChatCount: probe,
		}
	} else if err != gorm.ErrRecordNotFound {
		p.app.Log.Error("Daily report scheduler: load run failed",
			"org", s.OrganizationID, "date", reportDate, "error", err)
		return
	}

	action := dailyreport.DecideScheduleAction(now, loc, sendTime, reportDate, existingPtr)
	switch action {
	case dailyreport.ScheduleSkip:
		if existingPtr != nil && (s.LastScheduledDate != reportDate || s.LastScheduledStatus != existing.Status) {
			p.persistScheduleAudit(s, &existing, reportDate)
		}
		return
	case dailyreport.ScheduleWait:
		p.app.Log.Info("Daily report schedule: waiting until send time (no chats yet)",
			"org", s.OrganizationID, "date", reportDate, "send_time", sendTime)
		return
	}

	force := action == dailyreport.ScheduleForce
	p.app.Log.Info("Daily report schedule firing",
		"org", s.OrganizationID,
		"date", reportDate,
		"send_time", sendTime,
		"timezone", s.Timezone,
		"action", string(action),
		"force", force,
	)

	// Same pipeline as manual: generate → save DB/disk → validate → send.
	run, runErr := p.app.RunDailyReport(s.OrganizationID, reportDate, models.DailyReportTriggerSchedule, force)
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
