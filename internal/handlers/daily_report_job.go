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
	"github.com/shridarpatil/whatomate/internal/templateutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxChatsPerReport     = 200
	maxMessagesPerContact = 40
	maxMsgChars           = 500
	aiBatchSize           = 12 // chats per AI call
)

// RunDailyReport generates the DOCX for reportDate and sends it to active recipients.
// With chats: tries page AI (or chatbot AI), then rule-based fallback so DOCX always has rows.
// Empty days still produce a "no chats" DOCX and attempt template notify.
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

	// Claim run as running. With force (manual), always overwrite.
	// Without force, only claim if not terminal and not a fresh running job (race-safe).
	now := time.Now()
	staleBefore := now.Add(-30 * time.Minute)
	updates := map[string]any{
		"status":        models.DailyReportStatusRunning,
		"triggered_by":  triggeredBy,
		"started_at":    now,
		"error_message": "",
		"send_errors":   "",
	}
	q := a.DB.Model(&models.DailyReportRun{}).Where("id = ?", run.ID)
	if !force {
		q = q.Where(
			"status NOT IN ? AND (status <> ? OR started_at IS NULL OR started_at < ?)",
			[]string{models.DailyReportStatusCompleted, models.DailyReportStatusEmpty},
			models.DailyReportStatusRunning,
			staleBefore,
		)
	}
	claim := q.Updates(updates)
	if claim.Error != nil {
		return nil, claim.Error
	}
	if claim.RowsAffected == 0 {
		if err := a.DB.Where("id = ?", run.ID).First(&run).Error; err != nil {
			return nil, err
		}
		if run.Status == models.DailyReportStatusCompleted || run.Status == models.DailyReportStatusEmpty {
			return &run, nil
		}
		if run.Status == models.DailyReportStatusRunning {
			return &run, fmt.Errorf("report already running for %s", reportDate)
		}
		// Unexpected status — last-chance claim
		if err := a.DB.Model(&run).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	if err := a.DB.Where("id = ?", run.ID).First(&run).Error; err != nil {
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
	chats, totalMsgs, collectErr := a.collectDailyChats(orgID, settings.WhatsAppAccount, reportDate, loc)
	if collectErr != nil {
		return fail("collect chats: " + collectErr.Error())
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
		var sumErr error
		reportData, aiModel, sumErr = a.summarizeDailyChats(orgID, settings.WhatsAppAccount, reportDate, orgName, genAt, chats)
		if sumErr != nil {
			// Production: still ship a filled DOCX so ops get contact list + rule-based bullets.
			a.Log.Error("Daily report AI failed; using rule-based summaries",
				"org", orgID, "error", sumErr, "chats", len(chats))
			reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
			reportData.GeneratedAt = genAt
			aiModel = "fallback"
			run.ErrorMessage = "AI summarize failed (used rule-based summaries): " + sumErr.Error()
		} else {
			a.Log.Info("Daily report: AI summarize complete",
				"org", orgID, "model", aiModel, "rows", len(reportData.Chats), "ai_used", reportData.AIUsed)
		}
		// Safety: never emit empty chat list when we collected contacts.
		if len(reportData.Chats) == 0 && len(chats) > 0 {
			reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
			reportData.GeneratedAt = genAt
			aiModel = "fallback"
		}
	}
	run.AIModel = aiModel

	docBytes, err := dailyreport.BuildDOCX(reportData)
	if err != nil {
		return fail("docx: " + err.Error())
	}

	filename := fmt.Sprintf("daily-chat-report-%s.docx", reportDate)
	docPath, err := a.saveDailyReportFile(orgID, reportDate, filename, docBytes)
	if err != nil {
		return fail("save docx: " + err.Error())
	}
	run.PDFPath = docPath // column stores report file path (docx)
	run.PDFFilename = filename

	if b, err := json.Marshal(reportData); err == nil {
		var m models.JSONB
		if json.Unmarshal(b, &m) == nil {
			run.SummaryJSON = m
		}
	}

	// Send after AI + DOCX are fully ready
	a.Log.Info("Daily report: sending DOCX", "org", orgID, "recipients", len(settings.Recipients))
	sent, sendErrs := a.sendDailyReportFile(settings, &run, docBytes, filename, reportData)
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
		"org", orgID, "status", run.Status, "sent", sent, "ai", aiModel, "file", filename)
	return &run, nil
}

func (a *App) collectDailyChats(orgID uuid.UUID, waAccount, reportDate string, loc *time.Location) ([]dailyreport.ContactChat, int, error) {
	start, end, err := dailyreport.DayBounds(reportDate, loc)
	if err != nil {
		return nil, 0, err
	}

	// GORM maps WhatsAppAccount → column whats_app_account.
	// Prefer selected WhatsApp account; if empty, include all accounts for the org.
	type row struct {
		ContactID    uuid.UUID
		ProfileName  string
		PhoneNumber  string
		Direction    models.Direction
		Content      string
		MessageType  models.MessageType
		TemplateName string
		CreatedAt    time.Time
	}

	q := a.DB.Model(&models.Message{}).
		Select("messages.contact_id, contacts.profile_name, contacts.phone_number, messages.direction, messages.content, messages.message_type, messages.template_name, messages.created_at").
		Joins("JOIN contacts ON contacts.id = messages.contact_id AND contacts.deleted_at IS NULL").
		Where("messages.organization_id = ? AND messages.deleted_at IS NULL", orgID).
		Where("messages.created_at >= ? AND messages.created_at < ?", start, end).
		Order("messages.contact_id, messages.created_at asc")

	if strings.TrimSpace(waAccount) != "" {
		q = q.Where("messages.whats_app_account = ?", waAccount)
	}

	var rows []row
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	a.Log.Info("Daily report collect diagnostics",
		"org", orgID,
		"report_date", reportDate,
		"timezone", loc.String(),
		"start_utc", start.UTC().Format(time.RFC3339),
		"end_utc", end.UTC().Format(time.RFC3339),
		"account_filter", strings.TrimSpace(waAccount),
		"raw_rows", len(rows),
	)

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
		if text == "" {
			// Enrich non-text / interactive rows so AI still has something useful.
			switch r.MessageType {
			case models.MessageTypeInteractive:
				text = "[interactive reply]"
			case models.MessageTypeImage:
				text = "[image]"
			case models.MessageTypeDocument:
				text = "[document]"
			case models.MessageTypeAudio:
				text = "[audio]"
			case models.MessageTypeVideo:
				text = "[video]"
			case models.MessageTypeTemplate:
				if r.TemplateName != "" {
					text = "[template: " + r.TemplateName + "]"
				} else {
					text = "[template]"
				}
			default:
				if r.MessageType != "" {
					text = "[" + string(r.MessageType) + "]"
				} else {
					text = "[message]"
				}
			}
		}
		if utf8.RuneCountInString(text) > maxMsgChars {
			text = string([]rune(text)[:maxMsgChars]) + "..."
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

	// Prefer dedicated Daily Reports AI settings (configured on the same page).
	drSettings, err := a.getOrCreateDailyReportSettings(orgID)
	if err != nil {
		return dailyreport.ReportData{}, "", fmt.Errorf("load daily report settings: %w", err)
	}

	var aiSettings models.ChatbotSettings
	var modelLabel string

	if drSettings.DailyReportAIReady() {
		prompt := strings.TrimSpace(drSettings.AISystemPrompt)
		if prompt == "" {
			prompt = models.DefaultDailyReportAISystemPrompt
		}
		maxTok := drSettings.AIMaxTokens
		if maxTok < 2000 {
			maxTok = 3000
		}
		temp := drSettings.AITemperature
		if temp <= 0 {
			temp = 0.3
		}
		aiSettings = models.ChatbotSettings{
			OrganizationID: orgID,
			AI: models.AIConfig{
				Enabled:        true,
				Provider:       models.AIProvider(drSettings.AIProvider),
				APIKey:         drSettings.AIAPIKey,
				Model:          drSettings.AIModel,
				MaxTokens:      maxTok,
				Temperature:    temp,
				SystemPrompt:   prompt,
				IncludeHistory: false,
			},
		}
		modelLabel = drSettings.AIProvider + ":" + drSettings.AIModel
		a.Log.Info("Daily report using page AI settings", "org", orgID, "model", modelLabel)
	} else {
		// Fallback to chatbot settings if page AI not configured
		settings, err := a.getChatbotSettingsCached(orgID, waAccount)
		if err != nil {
			settings, err = a.getChatbotSettingsCached(orgID, "")
		}
		if err != nil || !localAIReady(settings) {
			return dailyreport.ReportData{}, "", fmt.Errorf("AI is not configured on Daily Reports page — enable AI, set provider, model, and API key under Setup → AI settings")
		}
		aiSettings = *settings
		aiSettings.AI.SystemPrompt = models.DefaultDailyReportAISystemPrompt
		aiSettings.AI.IncludeHistory = false
		if aiSettings.AI.MaxTokens < 2000 {
			aiSettings.AI.MaxTokens = 3000
		}
		modelLabel = string(aiSettings.AI.Provider) + ":" + aiSettings.AI.Model
		a.Log.Info("Daily report using chatbot AI settings (page AI not ready)", "org", orgID, "model", modelLabel)
	}

	// Always apply report prompt if still empty
	if strings.TrimSpace(aiSettings.AI.SystemPrompt) == "" {
		aiSettings.AI.SystemPrompt = models.DefaultDailyReportAISystemPrompt
	}

	allItems := make([]dailyreport.AISummaryItem, 0, len(chats))

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

func (a *App) saveDailyReportFile(orgID uuid.UUID, reportDate, filename string, data []byte) (string, error) {
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

func (a *App) sendDailyReportFile(settings *models.DailyReportSettings, run *models.DailyReportRun, fileBytes []byte, filename string, data dailyreport.ReportData) (sent int, errs []string) {
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

	// Load selected APPROVED template (dropdown on Daily Reports page).
	// TEXT header = notify outside 24h; DOCUMENT header can attach the report file.
	template, tplErrs := a.loadDailyReportTemplate(settings, account.Name)
	errs = append(errs, tplErrs...)

	mime := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		mime = "application/pdf"
	}

	var headerMediaID string
	useDocHeader := template != nil && strings.EqualFold(template.HeaderType, "DOCUMENT")
	if useDocHeader && len(fileBytes) > 0 {
		waAcct := a.toWhatsAppAccount(account)
		ctxUp, cancelUp := context.WithTimeout(context.Background(), 60*time.Second)
		id, upErr := a.WhatsApp.UploadMedia(ctxUp, waAcct, fileBytes, mime, filename)
		cancelUp()
		if upErr != nil {
			errs = append(errs, "upload report media: "+upErr.Error())
			useDocHeader = false
		} else {
			headerMediaID = id
		}
	}

	reportType := "Daily chat report"
	if data.EmptyDay || data.Overview.TotalChats == 0 {
		reportType = "Daily chat report (no chats)"
	}
	bodyParams := map[string]string{}
	headerParams := map[string]string{}
	if template != nil {
		bodyParams = buildDailyReportTemplateParams(template.BodyContent, run.ReportDate, data.Overview.TotalChats, reportType)
		headerParams = buildDailyReportTemplateParams(template.HeaderContent, run.ReportDate, data.Overview.TotalChats, reportType)
	}

	caption := fmt.Sprintf("%s — %s (%d chats)", reportType, run.ReportDate, data.Overview.TotalChats)

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
			_ = a.DB.Model(contact).Update("whats_app_account", account.Name).Error
			contact.WhatsAppAccount = account.Name
		}

		templateOK := false
		docOK := false
		var lastErr error
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		// 1) Selected utility/text template — works outside 24h.
		if template != nil {
			req := OutgoingMessageRequest{
				Account:      account,
				Contact:      contact,
				Type:         models.MessageTypeTemplate,
				Template:     template,
				BodyParams:   bodyParams,
				HeaderParams: headerParams,
			}
			if useDocHeader && headerMediaID != "" {
				req.HeaderMediaID = headerMediaID
				req.HeaderMediaFilename = filename
				req.MediaMimeType = mime
			}
			if _, err := a.SendOutgoingMessage(ctx, req, DefaultSendOptions()); err != nil {
				lastErr = err
				a.Log.Warn("Daily report template send failed", "recipient", r.Name, "error", err)
			} else {
				templateOK = true
			}
		}

		// 2) Free-form DOCX only inside open 24h window (customer inbound required).
		windowOpen := contact.LastInboundAt != nil && time.Since(*contact.LastInboundAt) < 24*time.Hour
		if windowOpen && len(fileBytes) > 0 && (!useDocHeader || !templateOK) {
			req2 := OutgoingMessageRequest{
				Account:       account,
				Contact:       contact,
				Type:          models.MessageTypeDocument,
				MediaData:     fileBytes,
				MediaMimeType: mime,
				MediaFilename: filename,
				Caption:       caption,
			}
			if _, err := a.SendOutgoingMessage(ctx, req2, DefaultSendOptions()); err != nil {
				lastErr = err
				a.Log.Warn("Daily report free-form document failed", "recipient", r.Name, "error", err)
			} else {
				docOK = true
			}
		}
		cancel()

		if templateOK || docOK {
			sent++
			continue
		}
		if template == nil && !windowOpen {
			errs = append(errs, fmt.Sprintf("%s (%s): no usable template and outside 24h window — file saved in History only", r.Name, phone))
			continue
		}
		if lastErr != nil {
			msg := fmt.Sprintf("%s (%s): %v", r.Name, phone, lastErr)
			if strings.Contains(strings.ToLower(lastErr.Error()), "24") || strings.Contains(lastErr.Error(), "131047") {
				msg += " [outside 24h — pick an APPROVED template on Daily Reports; DOCX is still in History]"
			}
			errs = append(errs, msg)
			a.Log.Error("Daily report send failed", "recipient", r.Name, "phone", phone, "error", lastErr)
		}
	}
	if active == 0 {
		errs = append(errs, "no active recipients configured")
	}
	return sent, errs
}

// loadDailyReportTemplate resolves the dropdown-selected template for this WA account.
// Prefers exact account+name+language, then account+name, then name only (language match preferred in Go).
func (a *App) loadDailyReportTemplate(settings *models.DailyReportSettings, accountName string) (*models.Template, []string) {
	tplName := strings.TrimSpace(settings.ReportTemplateName)
	if tplName == "" {
		return nil, []string{"no template selected — choose an APPROVED template on Daily Reports setup"}
	}
	tplLang := strings.TrimSpace(settings.ReportTemplateLanguage)
	if tplLang == "" {
		tplLang = "en"
	}

	pick := func(rows []models.Template) *models.Template {
		if len(rows) == 0 {
			return nil
		}
		for i := range rows {
			if strings.EqualFold(rows[i].Language, tplLang) {
				return &rows[i]
			}
		}
		return &rows[0]
	}

	var rows []models.Template
	// 1) exact account + name + language
	if err := a.DB.Where("organization_id = ? AND name = ? AND whats_app_account = ? AND language = ?",
		settings.OrganizationID, tplName, accountName, tplLang).Limit(1).Find(&rows).Error; err == nil {
		if t := pick(rows); t != nil {
			if !strings.EqualFold(t.Status, "APPROVED") {
				return nil, []string{fmt.Sprintf("template %q status is %s (need APPROVED)", tplName, t.Status)}
			}
			return t, nil
		}
	}
	// 2) account + name (any language)
	rows = nil
	if err := a.DB.Where("organization_id = ? AND name = ? AND whats_app_account = ?",
		settings.OrganizationID, tplName, accountName).Find(&rows).Error; err == nil {
		if t := pick(rows); t != nil {
			if !strings.EqualFold(t.Status, "APPROVED") {
				return nil, []string{fmt.Sprintf("template %q status is %s (need APPROVED)", tplName, t.Status)}
			}
			return t, nil
		}
	}
	// 3) name only (any account)
	rows = nil
	if err := a.DB.Where("organization_id = ? AND name = ?", settings.OrganizationID, tplName).Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil, []string{fmt.Sprintf("template %q not found — sync Templates and select it again on Daily Reports", tplName)}
	}
	t := pick(rows)
	if t == nil {
		return nil, []string{fmt.Sprintf("template %q not found — sync Templates and select it again on Daily Reports", tplName)}
	}
	if !strings.EqualFold(t.Status, "APPROVED") {
		return nil, []string{fmt.Sprintf("template %q status is %s (need APPROVED)", tplName, t.Status)}
	}
	return t, nil
}

// buildDailyReportTemplateParams maps date/count/type onto the template's declared body/header variables only.
// Order for positional {{1}},{{2}},{{3}}: date, chat count, report type.
func buildDailyReportTemplateParams(content, date string, chatCount int, reportType string) map[string]string {
	out := map[string]string{}
	names := templateutil.ExtParamNames(content)
	if len(names) == 0 {
		return out
	}
	ordered := []string{date, fmt.Sprintf("%d", chatCount), reportType}
	for i, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		switch key {
		case "date", "report_date", "day":
			out[name] = date
		case "count", "chat_count", "chats", "total":
			out[name] = fmt.Sprintf("%d", chatCount)
		case "type", "report_type", "label", "title":
			out[name] = reportType
		default:
			// positional {{1}} {{2}} {{3}} or unknown named → fill by occurrence order
			if i < len(ordered) {
				out[name] = ordered[i]
			}
		}
	}
	return out
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
		AIEnabled:      false,
		AIProvider:     string(models.AIProviderOpenRouter),
		AIModel:        "openai/gpt-4o-mini",
		AIMaxTokens:    3000,
		AITemperature:  0.3,
		AISystemPrompt: models.DefaultDailyReportAISystemPrompt,
	}
	if err := a.DB.Create(&s).Error; err != nil {
		if err2 := a.DB.Where("organization_id = ?", orgID).First(&s).Error; err2 == nil {
			return &s, nil
		}
		return nil, err
	}
	return &s, nil
}
