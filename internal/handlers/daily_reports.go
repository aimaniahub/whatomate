package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/dailyreport"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// DailyReportSettingsResponse is returned by GET settings.
type DailyReportSettingsResponse struct {
	ID              string                    `json:"id"`
	OrganizationID  string                    `json:"organization_id"`
	WhatsAppAccount string                    `json:"whatsapp_account"`
	Enabled         bool                      `json:"enabled"`
	Timezone        string                    `json:"timezone"`
	SendTime        string                    `json:"send_time"`
	ReportLanguage  string                    `json:"report_language"`
	Recipients      []DailyReportRecipientDTO `json:"recipients"`
	NextRunPreview  string                    `json:"next_run_preview,omitempty"`
	MaxRecipients   int                       `json:"max_recipients"`

	// WhatsApp template for cold notify outside 24h (prefer TEXT header utility)
	ReportTemplateName     string `json:"report_template_name"`
	ReportTemplateLanguage string `json:"report_template_language"`
	TemplateConfigured     bool   `json:"template_configured"`
	DeliveryMode           string `json:"delivery_mode"` // template | freeform

	// Schedule status for UI (persistent + computed)
	ScheduleActive          bool    `json:"schedule_active"`
	ScheduleCadence         string  `json:"schedule_cadence"` // human text
	NextFireAt              string  `json:"next_fire_at,omitempty"`
	NextSendAt              string  `json:"next_send_at,omitempty"`
	LastScheduledAt         string  `json:"last_scheduled_at,omitempty"`
	LastScheduledDate       string  `json:"last_scheduled_date,omitempty"`
	LastScheduledStatus     string  `json:"last_scheduled_status,omitempty"`
	LastScheduledRunID      string  `json:"last_scheduled_run_id,omitempty"`
	TodayReportDate         string  `json:"today_report_date,omitempty"`
	TodayRunStatus          string  `json:"today_run_status,omitempty"`
	TodayRunTriggeredBy     string  `json:"today_run_triggered_by,omitempty"`
	TodayRunID              string  `json:"today_run_id,omitempty"`
	ScheduleLeadMinutes     int     `json:"schedule_lead_minutes"`

	// Dedicated AI (this page) — API key never returned in full
	AIEnabled       bool    `json:"ai_enabled"`
	AIProvider      string  `json:"ai_provider"`
	AIModel         string  `json:"ai_model"`
	AIMaxTokens     int     `json:"ai_max_tokens"`
	AITemperature   float64 `json:"ai_temperature"`
	AISystemPrompt  string  `json:"ai_system_prompt"`
	AIHasAPIKey     bool    `json:"ai_has_api_key"`
	AIDefaultPrompt string  `json:"ai_default_prompt"`
	AIReady         bool    `json:"ai_ready"`
}

// DailyReportRecipientDTO is a setup recipient.
type DailyReportRecipientDTO struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	IsActive    bool   `json:"is_active"`
	SortOrder   int    `json:"sort_order"`
}

// UpdateDailyReportSettingsRequest is the PUT body.
type UpdateDailyReportSettingsRequest struct {
	WhatsAppAccount string                    `json:"whatsapp_account"`
	Enabled         *bool                     `json:"enabled"`
	Timezone        string                    `json:"timezone"`
	SendTime        string                    `json:"send_time"`
	ReportLanguage  string                    `json:"report_language"`
	Recipients      []DailyReportRecipientDTO `json:"recipients"`

	ReportTemplateName     *string `json:"report_template_name"`
	ReportTemplateLanguage *string `json:"report_template_language"`

	AIEnabled      *bool    `json:"ai_enabled"`
	AIProvider     *string  `json:"ai_provider"`
	AIAPIKey       *string  `json:"ai_api_key"` // omit or empty = keep existing; set to update
	AIModel        *string  `json:"ai_model"`
	AIMaxTokens    *int     `json:"ai_max_tokens"`
	AITemperature  *float64 `json:"ai_temperature"`
	AISystemPrompt *string  `json:"ai_system_prompt"`
}

// DailyReportRunDTO is a history row.
type DailyReportRunDTO struct {
	ID           string  `json:"id"`
	ReportDate   string  `json:"report_date"`
	Status       string  `json:"status"`
	TriggeredBy  string  `json:"triggered_by"`
	ChatCount    int     `json:"chat_count"`
	MessageCount int     `json:"message_count"`
	PDFFilename  string  `json:"pdf_filename,omitempty"`
	AIModel      string  `json:"ai_model,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	SendErrors   string  `json:"send_errors,omitempty"`
	SentCount    int     `json:"sent_count"`
	StartedAt    *string `json:"started_at,omitempty"`
	FinishedAt   *string `json:"finished_at,omitempty"`
	HasPDF       bool    `json:"has_pdf"`
}

// GetDailyReportSettings returns org daily report configuration.
func (a *App) GetDailyReportSettings(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionRead); err != nil {
		return err
	}

	s, err := a.getOrCreateDailyReportSettings(orgID)
	if err != nil {
		a.Log.Error("Failed to load daily report settings", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load settings", nil, "")
	}
	var recipients []models.DailyReportRecipient
	if err := a.DB.Where("settings_id = ?", s.ID).Order("sort_order asc, created_at asc").Find(&recipients).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load recipients", nil, "")
	}

	return r.SendEnvelope(a.toDailySettingsResponse(s, recipients))
}

// UpdateDailyReportSettings saves setup (max 2 recipients).
func (a *App) UpdateDailyReportSettings(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionWrite); err != nil {
		return err
	}

	var req UpdateDailyReportSettingsRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if len(req.Recipients) > models.MaxDailyReportRecipients {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Maximum 2 recipients allowed", nil, "")
	}
	normalizedSend := ""
	if req.SendTime != "" {
		norm, err := dailyreport.NormalizeSendTime(req.SendTime)
		if err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "send_time must be HH:MM", nil, "")
		}
		normalizedSend = norm
	}
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = models.DefaultDailyReportTimezone
	}
	// Validate timezone loads
	_ = dailyreport.LoadLocation(tz)

	for i, rec := range req.Recipients {
		if strings.TrimSpace(rec.Name) == "" {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Recipient name is required", nil, "")
		}
		if strings.TrimSpace(rec.PhoneNumber) == "" {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Recipient phone is required", nil, "")
		}
		req.Recipients[i].PhoneNumber = strings.TrimSpace(rec.PhoneNumber)
		req.Recipients[i].Name = strings.TrimSpace(rec.Name)
	}

	s, err := a.getOrCreateDailyReportSettings(orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load settings", nil, "")
	}

	s.WhatsAppAccount = strings.TrimSpace(req.WhatsAppAccount)
	s.Timezone = tz
	if normalizedSend != "" {
		s.SendTime = normalizedSend
	}
	if req.ReportLanguage != "" {
		s.ReportLanguage = req.ReportLanguage
	}
	if req.Enabled != nil {
		s.Enabled = *req.Enabled
	}
	if req.ReportTemplateName != nil {
		s.ReportTemplateName = strings.TrimSpace(*req.ReportTemplateName)
	}
	if req.ReportTemplateLanguage != nil {
		lang := strings.TrimSpace(*req.ReportTemplateLanguage)
		if lang == "" {
			lang = "en"
		}
		s.ReportTemplateLanguage = lang
	}

	// AI settings (dedicated to daily reports)
	if req.AIEnabled != nil {
		s.AIEnabled = *req.AIEnabled
	}
	if req.AIProvider != nil {
		p := strings.ToLower(strings.TrimSpace(*req.AIProvider))
		switch p {
		case "", string(models.AIProviderOpenAI), string(models.AIProviderOpenRouter),
			string(models.AIProviderAnthropic), string(models.AIProviderGoogle):
			s.AIProvider = p
		default:
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid AI provider (openai, openrouter, anthropic, google)", nil, "")
		}
	}
	if req.AIAPIKey != nil {
		key := strings.TrimSpace(*req.AIAPIKey)
		// Empty string means "leave unchanged"; only update when non-empty.
		// Use special value "-" to clear.
		if key == "-" {
			s.AIAPIKey = ""
		} else if key != "" {
			s.AIAPIKey = key
		}
	}
	if req.AIModel != nil {
		s.AIModel = strings.TrimSpace(*req.AIModel)
	}
	if req.AIMaxTokens != nil {
		if *req.AIMaxTokens < 256 {
			s.AIMaxTokens = 256
		} else if *req.AIMaxTokens > 16000 {
			s.AIMaxTokens = 16000
		} else {
			s.AIMaxTokens = *req.AIMaxTokens
		}
	}
	if req.AITemperature != nil {
		t := *req.AITemperature
		if t < 0 {
			t = 0
		}
		if t > 2 {
			t = 2
		}
		s.AITemperature = t
	}
	if req.AISystemPrompt != nil {
		s.AISystemPrompt = strings.TrimSpace(*req.AISystemPrompt)
	}

	tx := a.DB.Begin()
	if err := tx.Save(s).Error; err != nil {
		tx.Rollback()
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save settings", nil, "")
	}
	// Hard-delete so soft-deleted rows do not accumulate on every save.
	if err := tx.Unscoped().Where("settings_id = ?", s.ID).Delete(&models.DailyReportRecipient{}).Error; err != nil {
		tx.Rollback()
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update recipients", nil, "")
	}
	for i, rec := range req.Recipients {
		row := models.DailyReportRecipient{
			BaseModel:      models.BaseModel{ID: uuid.New()},
			OrganizationID: orgID,
			SettingsID:     s.ID,
			Name:           rec.Name,
			PhoneNumber:    rec.PhoneNumber,
			IsActive:       rec.IsActive,
			SortOrder:      i,
		}
		if err := tx.Create(&row).Error; err != nil {
			tx.Rollback()
			return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save recipient", nil, "")
		}
	}
	if err := tx.Commit().Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to commit settings", nil, "")
	}

	var recipients []models.DailyReportRecipient
	_ = a.DB.Where("settings_id = ?", s.ID).Order("sort_order asc").Find(&recipients).Error
	return r.SendEnvelope(a.toDailySettingsResponse(s, recipients))
}

// ListDailyReportRuns returns recent runs.
func (a *App) ListDailyReportRuns(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionRead); err != nil {
		return err
	}

	var runs []models.DailyReportRun
	if err := a.DB.Where("organization_id = ?", orgID).
		Order("report_date desc, created_at desc").
		Limit(60).
		Find(&runs).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list runs", nil, "")
	}

	out := make([]DailyReportRunDTO, 0, len(runs))
	for _, run := range runs {
		out = append(out, toDailyRunDTO(run))
	}
	return r.SendEnvelope(map[string]any{"runs": out})
}

// GetDailyReportRun returns one run.
func (a *App) GetDailyReportRun(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionRead); err != nil {
		return err
	}
	idStr, _ := r.RequestCtx.UserValue("id").(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid id", nil, "")
	}
	var run models.DailyReportRun
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&run).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Run not found", nil, "")
	}
	return r.SendEnvelope(toDailyRunDTO(run))
}

// DownloadDailyReportPDF streams the report file (DOCX) for a run.
func (a *App) DownloadDailyReportPDF(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionRead); err != nil {
		return err
	}
	idStr, _ := r.RequestCtx.UserValue("id").(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid id", nil, "")
	}
	var run models.DailyReportRun
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&run).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Run not found", nil, "")
	}
	if run.PDFPath == "" {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Report file not available", nil, "")
	}
	data, err := os.ReadFile(run.PDFPath)
	if err != nil {
		a.Log.Error("Failed to read daily report file", "path", run.PDFPath, "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Report file missing", nil, "")
	}
	name := run.PDFFilename
	if name == "" {
		name = filepath.Base(run.PDFPath)
	}
	ct := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if strings.HasSuffix(strings.ToLower(name), ".pdf") {
		ct = "application/pdf"
	}
	r.RequestCtx.Response.Header.Set("Content-Type", ct)
	r.RequestCtx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	r.RequestCtx.SetBody(data)
	r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
	return nil
}

// RunDailyReportNow triggers manual generation + send for a date (default today).
func (a *App) RunDailyReportNow(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionWrite); err != nil {
		return err
	}

	var body struct {
		Date string `json:"date"`
	}
	_ = a.decodeRequest(r, &body)

	settings, err := a.getOrCreateDailyReportSettings(orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load settings", nil, "")
	}
	loc := dailyreport.LoadLocation(settings.Timezone)
	date := strings.TrimSpace(body.Date)
	if date == "" {
		date = dailyreport.TodayDate(loc)
	}

	run, err := a.RunDailyReport(orgID, date, models.DailyReportTriggerManual, true)
	if err != nil && run == nil {
		a.Log.Error("Manual daily report failed", "error", err, "org", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, err.Error(), nil, "")
	}
	if run == nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Run failed", nil, "")
	}
	if err != nil {
		a.Log.Warn("Manual daily report finished with error", "error", err, "status", run.Status)
	}
	return r.SendEnvelope(toDailyRunDTO(*run))
}

// ResendDailyReport resends an existing run report file to recipients.
func (a *App) ResendDailyReport(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if err := a.requirePermission(r, userID, models.ResourceAnalytics, models.ActionWrite); err != nil {
		return err
	}
	idStr, _ := r.RequestCtx.UserValue("id").(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid id", nil, "")
	}
	var run models.DailyReportRun
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&run).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Run not found", nil, "")
	}
	if run.PDFPath == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "No report file to resend", nil, "")
	}
	fileBytes, err := os.ReadFile(run.PDFPath)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Report file missing", nil, "")
	}
	settings, err := a.getOrCreateDailyReportSettings(orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load settings", nil, "")
	}
	_ = a.DB.Where("settings_id = ?", settings.ID).Order("sort_order asc").Find(&settings.Recipients).Error

	var data dailyreport.ReportData
	if run.SummaryJSON != nil {
		b, _ := json.Marshal(run.SummaryJSON)
		_ = json.Unmarshal(b, &data)
	}
	data.ReportDate = run.ReportDate
	data.EmptyDay = run.Status == models.DailyReportStatusEmpty

	sent, sendErrs := a.sendDailyReportFile(settings, &run, fileBytes, run.PDFFilename, data)
	run.SentCount = sent
	run.SendErrors = strings.Join(sendErrs, "; ")
	_ = a.DB.Save(&run).Error
	return r.SendEnvelope(toDailyRunDTO(run))
}

func (a *App) toDailySettingsResponse(s *models.DailyReportSettings, recipients []models.DailyReportRecipient) DailyReportSettingsResponse {
	dto := make([]DailyReportRecipientDTO, 0, len(recipients))
	for _, r := range recipients {
		dto = append(dto, DailyReportRecipientDTO{
			ID:          r.ID.String(),
			Name:        r.Name,
			PhoneNumber: r.PhoneNumber,
			IsActive:    r.IsActive,
			SortOrder:   r.SortOrder,
		})
	}
	tz := s.Timezone
	if tz == "" {
		tz = models.DefaultDailyReportTimezone
	}
	sendTime := s.SendTime
	if norm, err := dailyreport.NormalizeSendTime(sendTime); err == nil {
		sendTime = norm
	} else if sendTime == "" {
		sendTime = models.DefaultDailyReportSendTime
	}
	loc := dailyreport.LoadLocation(tz)
	now := time.Now()
	today := dailyreport.TodayDate(loc)

	preview := fmt.Sprintf("Daily at %s %s", sendTime, tz)
	nextFire := ""
	nextSend := ""
	if s.Enabled {
		if nf, err := dailyreport.NextFireTime(now, loc, sendTime, dailyreport.ScheduleLeadMinutes); err == nil {
			nextFire = nf.Format(time.RFC3339)
			preview = fmt.Sprintf("Every day · next run %s · send %s %s",
				nf.In(loc).Format("02 Jan 15:04"), sendTime, tz)
		}
		if sa, err := dailyreport.SendAt(now, loc, sendTime); err == nil {
			local := now.In(loc)
			if !local.Before(sa) {
				sa = sa.Add(24 * time.Hour)
			}
			nextSend = sa.Format(time.RFC3339)
		}
	} else {
		preview = fmt.Sprintf("Schedule off · would run daily at %s %s when enabled", sendTime, tz)
	}

	todayStatus, todayTrig, todayRunID := "", "", ""
	var todayRun models.DailyReportRun
	if a != nil && a.DB != nil {
		if err := a.DB.Where("organization_id = ? AND report_date = ?", s.OrganizationID, today).
			Order("created_at desc").First(&todayRun).Error; err == nil {
			todayStatus = todayRun.Status
			todayTrig = todayRun.TriggeredBy
			todayRunID = todayRun.ID.String()
		}
	}

	prompt := s.AISystemPrompt
	if strings.TrimSpace(prompt) == "" {
		prompt = models.DefaultDailyReportAISystemPrompt
	}
	maxTok := s.AIMaxTokens
	if maxTok <= 0 {
		maxTok = 3000
	}
	temp := s.AITemperature
	if temp <= 0 {
		temp = 0.3
	}

	tplName := strings.TrimSpace(s.ReportTemplateName)
	tplLang := strings.TrimSpace(s.ReportTemplateLanguage)
	if tplLang == "" {
		tplLang = "en"
	}
	// template = cold notify via selected APPROVED template; freeform = DOCX only if 24h open
	deliveryMode := "freeform"
	if tplName != "" {
		deliveryMode = "template"
	}

	resp := DailyReportSettingsResponse{
		ID:              s.ID.String(),
		OrganizationID:  s.OrganizationID.String(),
		WhatsAppAccount: s.WhatsAppAccount,
		Enabled:         s.Enabled,
		Timezone:        tz,
		SendTime:        sendTime,
		ReportLanguage:  s.ReportLanguage,
		Recipients:      dto,
		NextRunPreview:  preview,
		MaxRecipients:   models.MaxDailyReportRecipients,

		ReportTemplateName:     tplName,
		ReportTemplateLanguage: tplLang,
		TemplateConfigured:     tplName != "",
		DeliveryMode:           deliveryMode,

		ScheduleActive:      s.Enabled,
		ScheduleCadence:     fmt.Sprintf("Every day at %s (%s)", sendTime, tz),
		NextFireAt:          nextFire,
		NextSendAt:          nextSend,
		LastScheduledDate:   s.LastScheduledDate,
		LastScheduledStatus: s.LastScheduledStatus,
		TodayReportDate:     today,
		TodayRunStatus:      todayStatus,
		TodayRunTriggeredBy: todayTrig,
		TodayRunID:          todayRunID,
		ScheduleLeadMinutes: dailyreport.ScheduleLeadMinutes,

		AIEnabled:       s.AIEnabled,
		AIProvider:      s.AIProvider,
		AIModel:         s.AIModel,
		AIMaxTokens:     maxTok,
		AITemperature:   temp,
		AISystemPrompt:  prompt,
		AIHasAPIKey:     strings.TrimSpace(s.AIAPIKey) != "",
		AIDefaultPrompt: models.DefaultDailyReportAISystemPrompt,
		AIReady:         s.DailyReportAIReady(),
	}
	if s.LastScheduledAt != nil {
		resp.LastScheduledAt = s.LastScheduledAt.UTC().Format(time.RFC3339)
	}
	if s.LastScheduledRunID != nil {
		resp.LastScheduledRunID = s.LastScheduledRunID.String()
	}
	return resp
}

func toDailyRunDTO(run models.DailyReportRun) DailyReportRunDTO {
	dto := DailyReportRunDTO{
		ID:           run.ID.String(),
		ReportDate:   run.ReportDate,
		Status:       run.Status,
		TriggeredBy:  run.TriggeredBy,
		ChatCount:    run.ChatCount,
		MessageCount: run.MessageCount,
		PDFFilename:  run.PDFFilename,
		AIModel:      run.AIModel,
		ErrorMessage: run.ErrorMessage,
		SendErrors:   run.SendErrors,
		SentCount:    run.SentCount,
		HasPDF:       run.PDFPath != "",
	}
	if run.StartedAt != nil {
		s := run.StartedAt.UTC().Format(time.RFC3339)
		dto.StartedAt = &s
	}
	if run.FinishedAt != nil {
		s := run.FinishedAt.UTC().Format(time.RFC3339)
		dto.FinishedAt = &s
	}
	return dto
}
