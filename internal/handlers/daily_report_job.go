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
	aiBatchSize           = 12 // chats per AI call
)

// RunDailyReport generates the PDF for reportDate and sends it to active recipients.
// It always runs AI first when there are chats (fails clearly if AI is not configured).
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

	fail := func(msg string) (*models.DailyReportRun, error) {
		finished := time.Now()
		run.Status = models.DailyReportStatusFailed
		run.ErrorMessage = msg
		run.FinishedAt = &finished
		_ = a.DB.Save(&run).Error
		return &run, fmt.Errorf("%s", msg)
	}

	orgName := ""
	var org models.Organization
	if err := a.DB.Select("id", "name").Where("id = ?", orgID).First(&org).Error; err == nil {
		orgName = org.Name
	}

	a.Log.Info("Daily report: collecting chats", "org", orgID, "date", reportDate)
	chats, totalMsgs, err := a.collectDailyChats(orgID, settings.WhatsAppAccount, reportDate, loc)
	if err != nil {
		return fail("collect chats: " + err.Error())
	}
	run.ChatCount = len(chats)
	run.MessageCount = totalMsgs

	genAt := time.Now().In(loc).Format("2006-01-02 15:04")
	var reportData dailyreport.ReportData
	var aiModel string

	if len(chats) == 0 {
		reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
		reportData.GeneratedAt = genAt
		aiModel = "none"
		a.Log.Info("Daily report: empty day (no AI)", "org", orgID, "date", reportDate)
	} else {
		a.Log.Info("Daily report: starting AI summarize",
			"org", orgID, "date", reportDate, "chats", len(chats), "messages", totalMsgs)
		reportData, aiModel, err = a.summarizeDailyChats(orgID, settings.WhatsAppAccount, reportDate, orgName, genAt, chats)
		if err != nil {
			a.Log.Error("Daily report AI failed", "org", orgID, "error", err)
			return fail("AI summarize failed: " + err.Error() + ". Configure AI in Settings → Chatbot (provider, model, API key) and enable AI.")
		}
		a.Log.Info("Daily report: AI summarize complete",
			"org", orgID, "model", aiModel, "rows", len(reportData.Chats), "ai_used", reportData.AIUsed)
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

	if b, err := json.Marshal(reportData); err == nil {
		var m models.JSONB
		if json.Unmarshal(b, &m) == nil {
			run.SummaryJSON = m
		}
	}

	// Send after AI + PDF are fully ready
	a.Log.Info("Daily report: sending PDF", "org", orgID, "recipients", len(settings.Recipients))
	sent, sendErrs := a.sendDailyReportPDF(settings, &run, pdfBytes, filename, reportData)
	run.SentCount = sent
	if len(sendErrs) > 0 {
		run.SendErrors = strings.Join(sendErrs, "; ")
	}

	finished := time.Now()
	run.FinishedAt = &finished
	if reportData.EmptyDay || len(chats) == 0 {
		run.Status = models.DailyReportStatusEmpty
	} else {
		run.Status = models.DailyReportStatusCompleted
	}
	if err := a.DB.Save(&run).Error; err != nil {
		return &run, err
	}
	a.Log.Info("Daily report: complete",
		"org", orgID, "status", run.Status, "sent", sent, "ai", aiModel)
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
				ChatDate:  reportDate,
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
		cc.Messages = append(cc.Messages, dailyreport.ChatMessage{
			Direction: string(r.Direction),
			At:        r.CreatedAt.In(loc).Format("15:04"),
			Text:      text,
		})
		cc.MessageCount++
	}

	out := make([]dailyreport.ContactChat, 0, len(order))
	for i, id := range order {
		cc := *byContact[id]
		cc.Serial = i + 1
		out = append(out, cc)
	}
	return out, total, nil
}

func (a *App) summarizeDailyChats(
	orgID uuid.UUID,
	waAccount, reportDate, orgName, generatedAt string,
	chats []dailyreport.ContactChat,
) (dailyreport.ReportData, string, error) {
	if len(chats) == 0 {
		r := dailyreport.FallbackSummarize(reportDate, orgName, chats)
		r.GeneratedAt = generatedAt
		return r, "none", nil
	}

	settings, err := a.getChatbotSettingsCached(orgID, waAccount)
	if err != nil {
		// try org-level empty account
		settings, err = a.getChatbotSettingsCached(orgID, "")
	}
	if err != nil || !localAIReady(settings) {
		return dailyreport.ReportData{}, "", fmt.Errorf("AI is not configured for this organization (enable AI + provider + API key in Chatbot settings)")
	}

	systemPrompt := `You summarize WhatsApp chat transcripts for an operations daily report.
Return ONLY valid JSON (no markdown fences) with this exact shape:
{"items":[{"id":1,"bullets":["point one","point two","point three"]}]}

Rules:
- Each input chat has an integer "id". Echo the same id in your output.
- For each id write at most 3 short bullet points (max 20 words each).
- Capture the customer's overall query / intent only.
- Base bullets ONLY on the messages provided. Do not invent facts.
- Do not include names, phone numbers, or greetings.
- Prefer inbound (customer) messages.`

	aiSettings := *settings
	aiSettings.AI.SystemPrompt = systemPrompt
	aiSettings.AI.IncludeHistory = false
	if aiSettings.AI.MaxTokens < 2000 {
		aiSettings.AI.MaxTokens = 3000
	}

	allItems := make([]dailyreport.AISummaryItem, 0, len(chats))
	modelLabel := string(aiSettings.AI.Provider) + ":" + aiSettings.AI.Model

	for start := 0; start < len(chats); start += aiBatchSize {
		end := start + aiBatchSize
		if end > len(chats) {
			end = len(chats)
		}
		batch := chats[start:end]
		payload := make([]dailyreport.AIChatPayload, 0, len(batch))
		for _, c := range batch {
			payload = append(payload, dailyreport.AIChatPayload{
				ID:       c.Serial,
				Messages: c.Messages,
			})
		}
		userJSON, err := json.Marshal(map[string]any{
			"report_date": reportDate,
			"chats":       payload,
		})
		if err != nil {
			return dailyreport.ReportData{}, "", err
		}

		a.Log.Info("Daily report AI batch",
			"org", orgID, "from_id", batch[0].Serial, "to_id", batch[len(batch)-1].Serial, "count", len(batch))

		answer, err := a.callDailyReportLLM(&aiSettings, string(userJSON))
		if err != nil {
			return dailyreport.ReportData{}, "", fmt.Errorf("batch %d-%d: %w", batch[0].Serial, batch[len(batch)-1].Serial, err)
		}
		answer = cleanAIResponse(answer)
		answer = extractJSONObject(answer)

		var parsed dailyreport.AISummaryResponse
		if err := json.Unmarshal([]byte(answer), &parsed); err != nil {
			return dailyreport.ReportData{}, "", fmt.Errorf("parse AI JSON (batch %d): %w; raw=%s", batch[0].Serial, err, truncateForLog(answer, 400))
		}
		allItems = append(allItems, parsed.Items...)
	}

	merged := dailyreport.MergeAISummaries(reportDate, orgName, generatedAt, modelLabel, chats, allItems)
	return merged, modelLabel, nil
}

func (a *App) callDailyReportLLM(settings *models.ChatbotSettings, userMessage string) (string, error) {
	var answer string
	var err error
	switch settings.AI.Provider {
	case models.AIProviderOpenAI:
		answer, err = a.generateOpenAIResponse(settings, nil, userMessage, "")
	case models.AIProviderAnthropic:
		answer, err = a.generateAnthropicResponse(settings, nil, userMessage, "")
	case models.AIProviderGoogle:
		answer, err = a.generateGoogleResponse(settings, nil, userMessage, "")
	case models.AIProviderOpenRouter:
		answer, err = a.generateOpenRouterResponse(settings, nil, userMessage, "")
	default:
		return "", fmt.Errorf("unsupported AI provider: %s", settings.AI.Provider)
	}
	return answer, err
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
		if data.AIUsed {
			caption += " · AI ready"
		}
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
		if err2 := a.DB.Where("organization_id = ?", orgID).First(&s).Error; err2 == nil {
			return &s, nil
		}
		return nil, err
	}
	return &s, nil
}
