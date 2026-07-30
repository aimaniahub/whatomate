package handlers

import (
	"context"
	"time"

	"github.com/shridarpatil/whatomate/internal/dailyreport"
	"github.com/shridarpatil/whatomate/internal/models"
)

// DailyReportScheduler periodically runs enabled org daily reports.
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
	// Run once shortly after boot for catch-up
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
	now := time.Now()
	for _, s := range settings {
		loc := dailyreport.LoadLocation(s.Timezone)
		reportDate := dailyreport.TodayDate(loc)
		if !dailyreport.ShouldRunSchedule(now, loc, s.SendTime, reportDate) {
			continue
		}
		// Skip if already completed/empty for today
		var existing models.DailyReportRun
		err := p.app.DB.Where("organization_id = ? AND report_date = ?", s.OrganizationID, reportDate).First(&existing).Error
		if err == nil {
			if existing.Status == models.DailyReportStatusCompleted ||
				existing.Status == models.DailyReportStatusEmpty ||
				existing.Status == models.DailyReportStatusRunning {
				continue
			}
		}

		p.app.Log.Info("Daily report schedule firing",
			"org", s.OrganizationID, "date", reportDate, "send_time", s.SendTime)
		if _, err := p.app.RunDailyReport(s.OrganizationID, reportDate, models.DailyReportTriggerSchedule, false); err != nil {
			p.app.Log.Error("Daily report scheduled run failed",
				"org", s.OrganizationID, "date", reportDate, "error", err)
		}
	}
}
