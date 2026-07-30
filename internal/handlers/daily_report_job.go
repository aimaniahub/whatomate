package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactutil"
	"github.com/shridarpatil/whatomate/internal/dailyreport"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxChatsPerReport     = 200
	maxMessagesPerContact = 40
	maxMsgChars           = 500
)

// RunDailyReport generates the PDF for reportDate and sends it to active recipients.
// reportDate is YYYY-MM-DD in the settings timezone. Idempotent for schedule unless force.
func (a *App) RunDailyReport(orgID uuid.UUID, reportDate, triggeredBy string, force bool) (*models.DailyReportRun, error) {
	settings, err := a.getOrCreateDailyReportSettings(orgID)
	if err != nil {
		return nil, err
	}
	if err := a.DB.Where("settings_id = ?", settings.ID).Order("sort_order asc, created_at asc").Find(&settings.Recipients).Error; err != nil {
		return nil, err
	}

	loc := dailyreport.LoadLocation(settings.Timezone)
	if reportDate == "" {
		reportDate = dailyreport.TodayDate(loc)
	}
	if _, _, err := dailyreport.DayBounds(reportDate, loc); err != nil {
		return nil, err
	}

	// Claim or create run row
	var run models.DailyReportRun
	err = a.DB.Where("organization_id = ? AND report_date = ?", orgID, reportDate).First(&run).Error
	if err == nil {
		if !force && (run.Status == models.DailyReportStatusCompleted || run.Status == models.DailyReportStatusEmpty) {
			return &run, nil
		}
		if run.Status == models.DailyReportStatusRunning {
			// Allow force after 30 minutes stuck
			if !force && run.StartedAt != nil && time.Since(*run.StartedAt) < 30*time.Minute {
				return &run, fmt.Errorf("report already running for %s", reportDate)
			}
		}
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	} else {
		run = models.DailyReportRun{
			BaseModel:      models.BaseModel{ID: uuid.New()},
			OrganizationID: orgID,
			ReportDate:     reportDate,
			Status:         models.DailyReportStatusPending,
			TriggeredBy:    triggeredBy,
		}
		if err := a.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "organization_id"}, {Name: "report_date"}},
			DoNothing: true,
		}).Create(&run).Error; err != nil {
			return nil, err
		}
		// re-load in case of race
		if err := a.DB.Where("organization_id = ? AND report_date = ?", orgID, reportDate).First(&run).Error; err != nil {
			return nil, err
		}
		if !force && (run.Status == models.DailyReportStatusCompleted || run.Status == models.DailyReportStatusEmpty) {
			return &run, nil
		}
	}

	now := time.Now()
	run.Status = models.DailyReportStatusRunning
	run.TriggeredBy = triggeredBy
	run.StartedAt = &now
	run.ErrorMessage = ""
	run.SendErrors = ""
	if err := a.DB.Save(&run).Error; err != nil {
		return nil, err
	}

	finished := time.Now()
	fail := func(msg string) (*models.DailyReportRun, error) {
		run.Status = models.DailyReportStatusFailed
		run.ErrorMessage = msg
		run.FinishedAt = &finished
		_ = a.DB.Save(&run).Error
		return &run, fmt.Errorf("%s", msg)
	}

	// Org name
	orgName := ""
	var org models.Organization
	if err := a.DB.Select("id", "name").Where("id = ?", orgID).First(&org).Error; err == nil {
		orgName = org.Name
	}

	chats, totalMsgs, err := a.collectDailyChats(orgID, settings.WhatsAppAccount, reportDate, loc)
	if err != nil {
		return fail("collect chats: " + err.Error())
	}
	run.ChatCount = len(chats)
	run.MessageCount = totalMsgs

	reportData, aiModel, err := a.summarizeDailyChats(orgID, settings.WhatsAppAccount, reportDate, orgName, chats)
	if err != nil {
		a.Log.Warn("Daily report AI summarize failed; using fallback", "org", orgID, "error", err)
		reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
		aiModel = "fallback"
	}
	run.AIModel = aiModel

	pdfBytes, err := dailyreport.BuildPDF(reportData)
	if err != nil {
		return fail("pdf: " + err.Error())
	}

	filename := fmt.Sprintf("daily-chat-report-%s.pdf", reportDate)
	pdfPath, err := a.saveDailyReportPDF(orgID, reportDate, filename, pdfBytes)
	if err != nil {
		return fail("save pdf: " + err.Error())
	}
	run.PDFPath = pdfPath
	run.PDFFilename = filename

	// Store summary
	if b, err := json.Marshal(reportData); err == nil {
		var m models.JSONB
		if json.Unmarshal(b, &m) == nil {
			run.SummaryJSON = m
		}
	}

	// Send to recipients
	sent, sendErrs := a.sendDailyReportPDF(settings, &run, pdfBytes, filename, reportData)
	run.SentCount = sent
	if len(sendErrs) > 0 {
		run.SendErrors = strings.Join(sendErrs, "; ")
	}

	finished = time.Now()
	run.FinishedAt = &finished
	if reportData.EmptyDay || len(chats) == 0 {
		run.Status = models.DailyReportStatusEmpty
	} else {
		run.Status = models.DailyReportStatusCompleted
	}
	// If PDF built but all sends failed and there were recipients, keep completed/empty but surface errors
	if err := a.DB.Save(&run).Error; err != nil {
		return &run, err
	}
	return &run, nil
}

func (a *App) collectDailyChats(orgID uuid.UUID, waAccount, reportDate string, loc *time.Location) ([]dailyreport.ContactChat, int, error) {
	start, end, err := dailyreport.DayBounds(reportDate, loc)
	if err != nil {
		return nil, 0, err
	}

	type row struct {
		ContactID   uuid.UUID
		ProfileName string
		PhoneNumber string
		Direction   models.Direction
		Content     string
		CreatedAt   time.Time
	}

	q := a.DB.Table("messages").
		Select("messages.contact_id, contacts.profile_name, contacts.phone_number, messages.direction, messages.content, messages.created_at").
		Joins("JOIN contacts ON contacts.id = messages.contact_id AND contacts.deleted_at IS NULL").
		Where("messages.organization_id = ? AND messages.deleted_at IS NULL", orgID).
		Where("messages.created_at >= ? AND messages.created_at < ?", start, end).
		Order("messages.contact_id, messages.created_at asc")

	if strings.TrimSpace(waAccount) != "" {
		q = q.Where("messages.whatsapp_account = ?", waAccount)
	}

	var rows []row
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	byContact := map[uuid.UUID]*dailyreport.ContactChat{}
	order := make([]uuid.UUID, 0)
	total := 0

	for _, r := range rows {
		total++
		cc, ok := byContact[r.ContactID]
		if !ok {
			if len(byContact) >= maxChatsPerReport {
				continue
			}
			name := strings.TrimSpace(r.ProfileName)
			if name == "" {
				name = r.PhoneNumber
			}
			cc = &dailyreport.ContactChat{
				ContactID: r.ContactID.String(),
				Name:      name,
				Phone:     r.PhoneNumber,
				Messages:  nil,
			}
			byContact[r.ContactID] = cc
			order = append(order, r.ContactID)
		}
		if len(cc.Messages) >= maxMessagesPerContact {
			continue
		}
		text := strings.TrimSpace(r.Content)
		if utf8.RuneCountInString(text) > maxMsgChars {
			text = string([]rune(text)[:maxMsgChars]) + "..."
		}
		if text == "" {
			text = "[non-text message]"
		}
		dir := string(r.Direction)
		cc.Messages = append(cc.Messages, dailyreport.ChatMessage{
			Direction: dir,
			At:        r.CreatedAt.UTC().Format(time.RFC3339),
			Text:      text,
		})
		cc.MessageCount++
	}

	out := make([]dailyreport.ContactChat, 0, len(order))
	for _, id := range order {
		out = append(out, *byContact[id])
	}
	return out, total, nil
}

func (a *App) summarizeDailyChats(orgID uuid.UUID, waAccount, reportDate, orgName string, chats []dailyreport.ContactChat) (dailyreport.ReportData, string, error) {
	if len(chats) == 0 {
		return dailyreport.FallbackSummarize(reportDate, orgName, chats), "none", nil
	}

	settings, err := a.getChatbotSettingsCached(orgID, waAccount)
	if err != nil || !localAIReady(settings) {
		return dailyreport.FallbackSummarize(reportDate, orgName, chats), "fallback", nil
	}

	// Build compact user payload
	payload := map[string]any{
		"report_date": reportDate,
		"chats":       chats,
	}
	userJSON, err := json.Marshal(payload)
	if err != nil {
		return dailyreport.ReportData{}, "", err
	}

	systemPrompt := `You are an operations analyst. Given WhatsApp chats for one business day, return ONLY valid JSON (no markdown) with this shape:
{
  "report_date": "YYYY-MM-DD",
  "overview": {
    "total_chats": number,
    "top_intents": ["..."],
    "needs_callback": number,
    "notes": "short day narrative"
  },
  "chats": [
    {
      "name": "string",
      "phone": "string",
      "summary": "1-3 sentence overall query summary",
      "intent": "short label",
      "call_requested": true/false,
      "priority": "low|normal|high",
      "next_action": "short"
    }
  ]
}
Rules: base every summary only on the transcript; do not invent facts; keep phone numbers unchanged; mark call_requested true if customer asked to be called or needs human callback.`

	// Temporarily override system prompt / history for this call
	aiSettings := *settings
	aiSettings.AI.SystemPrompt = systemPrompt
	aiSettings.AI.IncludeHistory = false
	if aiSettings.AI.MaxTokens < 1500 {
		aiSettings.AI.MaxTokens = 2500
	}

	var answer string
	switch aiSettings.AI.Provider {
	case models.AIProviderOpenAI:
		answer, err = a.generateOpenAIResponse(&aiSettings, nil, string(userJSON), "")
	case models.AIProviderAnthropic:
		answer, err = a.generateAnthropicResponse(&aiSettings, nil, string(userJSON), "")
	case models.AIProviderGoogle:
		answer, err = a.generateGoogleResponse(&aiSettings, nil, string(userJSON), "")
	case models.AIProviderOpenRouter:
		answer, err = a.generateOpenRouterResponse(&aiSettings, nil, string(userJSON), "")
	default:
		return dailyreport.FallbackSummarize(reportDate, orgName, chats), "fallback", nil
	}
	if err != nil {
		return dailyreport.ReportData{}, "", err
	}

	answer = cleanAIResponse(answer)
	answer = extractJSONObject(answer)

	var parsed dailyreport.ReportData
	if err := json.Unmarshal([]byte(answer), &parsed); err != nil {
		return dailyreport.ReportData{}, "", fmt.Errorf("parse AI JSON: %w", err)
	}
	parsed.ReportDate = reportDate
	parsed.OrgName = orgName
	if parsed.Overview.TotalChats == 0 {
		parsed.Overview.TotalChats = len(parsed.Chats)
	}
	// Ensure phones filled from source if AI dropped them
	phoneByName := map[string]string{}
	for _, c := range chats {
		phoneByName[strings.ToLower(strings.TrimSpace(c.Name))] = c.Phone
		phoneByName[c.Phone] = c.Phone
	}
	for i := range parsed.Chats {
		if strings.TrimSpace(parsed.Chats[i].Phone) == "" {
			if p, ok := phoneByName[strings.ToLower(strings.TrimSpace(parsed.Chats[i].Name))]; ok {
				parsed.Chats[i].Phone = p
			}
		}
	}
	model := string(aiSettings.AI.Provider) + ":" + aiSettings.AI.Model
	return parsed, model, nil
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

func (a *App) saveDailyReportPDF(orgID uuid.UUID, reportDate, filename string, data []byte) (string, error) {
	base := "./uploads"
	if a.Config != nil && a.Config.Storage.LocalPath != "" {
		base = a.Config.Storage.LocalPath
	}
	dir := filepath.Join(base, "daily-reports", orgID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) sendDailyReportPDF(settings *models.DailyReportSettings, run *models.DailyReportRun, pdf []byte, filename string, data dailyreport.ReportData) (sent int, errs []string) {
	if settings == nil {
		return 0, []string{"no settings"}
	}
	accountName := strings.TrimSpace(settings.WhatsAppAccount)
	if accountName == "" {
		// pick first account for org
		var acc models.WhatsAppAccount
		if err := a.DB.Where("organization_id = ?", settings.OrganizationID).Order("created_at asc").First(&acc).Error; err != nil {
			return 0, []string{"no whatsapp account configured"}
		}
		accountName = acc.Name
	}
	account, err := a.resolveWhatsAppAccount(settings.OrganizationID, accountName)
	if err != nil {
		return 0, []string{err.Error()}
	}

	caption := fmt.Sprintf("Daily chat report — %s", run.ReportDate)
	if data.EmptyDay {
		caption = fmt.Sprintf("Daily chat report — %s (no chats)", run.ReportDate)
	} else {
		caption = fmt.Sprintf("Daily chat report — %s (%d chats)", run.ReportDate, data.Overview.TotalChats)
	}

	active := 0
	for _, r := range settings.Recipients {
		if !r.IsActive {
			continue
		}
		active++
		if active > models.MaxDailyReportRecipients {
			errs = append(errs, "skipped extra recipient beyond max 2")
			break
		}
		phone := strings.TrimSpace(r.PhoneNumber)
		if phone == "" {
			errs = append(errs, fmt.Sprintf("%s: empty phone", r.Name))
			continue
		}
		contact, _, err := contactutil.GetOrCreateContact(a.DB, settings.OrganizationID, phone, r.Name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: contact: %v", r.Name, err))
			continue
		}
		// Ensure contact is associated with send account for routing
		if contact.WhatsAppAccount == "" {
			_ = a.DB.Model(contact).Update("whatsapp_account", account.Name).Error
			contact.WhatsAppAccount = account.Name
		}

		req := OutgoingMessageRequest{
			Account:       account,
			Contact:       contact,
			Type:          models.MessageTypeDocument,
			MediaData:     pdf,
			MediaMimeType: "application/pdf",
			MediaFilename: filename,
			Caption:       caption,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		_, sendErr := a.SendOutgoingMessage(ctx, req, DefaultSendOptions())
		cancel()
		if sendErr != nil {
			errs = append(errs, fmt.Sprintf("%s (%s): %v", r.Name, phone, sendErr))
			a.Log.Error("Daily report send failed", "recipient", r.Name, "phone", phone, "error", sendErr)
			continue
		}
		sent++
	}
	if active == 0 {
		errs = append(errs, "no active recipients configured")
	}
	return sent, errs
}

func (a *App) getOrCreateDailyReportSettings(orgID uuid.UUID) (*models.DailyReportSettings, error) {
	var s models.DailyReportSettings
	err := a.DB.Where("organization_id = ?", orgID).First(&s).Error
	if err == nil {
		return &s, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	s = models.DailyReportSettings{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: orgID,
		Enabled:        false,
		Timezone:       models.DefaultDailyReportTimezone,
		SendTime:       models.DefaultDailyReportSendTime,
		ReportLanguage: "en",
	}
	if err := a.DB.Create(&s).Error; err != nil {
		// race
		if err2 := a.DB.Where("organization_id = ?", orgID).First(&s).Error; err2 == nil {
			return &s, nil
		}
		return nil, err
	}
	return &s, nil
}
