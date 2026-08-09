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
	// dailyReportStuckAfter: AI batches can run long — do not reclaim while still working.
	dailyReportStuckAfter = 45 * time.Minute
)

// RunDailyReport runs the full production pipeline **synchronously**:
//
//  1. Collect chats/messages for the local calendar day
//  2. Wait for AI summary to complete (all batches) — or rule-based fallback
//  3. Ensure every row has summary + message excerpts
//  4. Compose DOCX and save to disk
//  5. Send WhatsApp (sync — wait for Meta) only after steps 1–4 succeed
//
// reportDate is YYYY-MM-DD in the settings timezone. Idempotent for schedule unless force.
func (a *App) RunDailyReport(orgID uuid.UUID, reportDate, triggeredBy string, force bool) (*models.DailyReportRun, error) {
	pipelineStart := time.Now()
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
			if !force && run.StartedAt != nil && time.Since(*run.StartedAt) < dailyReportStuckAfter {
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
	staleBefore := now.Add(-dailyReportStuckAfter)
	updates := map[string]any{
		"status":        models.DailyReportStatusRunning,
		"triggered_by":  triggeredBy,
		"started_at":    now,
		"error_message": "",
		"send_errors":   "",
		"sent_count":    0,
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
		a.Log.Error("Daily report pipeline failed",
			"org", orgID, "date", reportDate, "error", msg, "elapsed", time.Since(pipelineStart))
		return &run, fmt.Errorf("%s", msg)
	}

	orgName := ""
	var org models.Organization
	if err := a.DB.Select("id", "name").Where("id = ?", orgID).First(&org).Error; err == nil {
		orgName = org.Name
	}

	// ── Phase 1: COLLECT ──────────────────────────────────────────────
	a.Log.Info("Daily report phase=collect start", "org", orgID, "date", reportDate, "trigger", triggeredBy)
	chats, totalMsgs, collectErr := a.collectDailyChats(orgID, settings.WhatsAppAccount, reportDate, loc)
	if collectErr != nil {
		return fail("collect chats: " + collectErr.Error())
	}
	run.ChatCount = len(chats)
	run.MessageCount = totalMsgs
	_ = a.DB.Model(&run).Updates(map[string]any{
		"chat_count":    run.ChatCount,
		"message_count": run.MessageCount,
	}).Error
	a.Log.Info("Daily report phase=collect done",
		"org", orgID, "chats", len(chats), "messages", totalMsgs, "elapsed", time.Since(pipelineStart))

	// ── Phase 2: AI SUMMARY (wait until all batches finish) ───────────
	genAt := time.Now().In(loc).Format("2006-01-02 15:04")
	var reportData dailyreport.ReportData
	var aiModel string

	if len(chats) == 0 {
		reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
		reportData.GeneratedAt = genAt
		aiModel = "none"
		a.Log.Info("Daily report phase=ai skipped (empty day)", "org", orgID, "date", reportDate)
	} else {
		a.Log.Info("Daily report phase=ai start",
			"org", orgID, "date", reportDate, "chats", len(chats), "messages", totalMsgs)
		aiStart := time.Now()
		var sumErr error
		reportData, aiModel, sumErr = a.summarizeDailyChats(orgID, settings.WhatsAppAccount, reportDate, orgName, genAt, chats)
		if sumErr != nil {
			// Still produce a usable report — never send before we have summaries.
			a.Log.Error("Daily report phase=ai failed; using rule-based summaries",
				"org", orgID, "error", sumErr, "chats", len(chats), "ai_elapsed", time.Since(aiStart))
			reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
			reportData.GeneratedAt = genAt
			aiModel = "fallback"
			run.ErrorMessage = "AI summarize failed (used rule-based summaries): " + sumErr.Error()
		} else {
			a.Log.Info("Daily report phase=ai done",
				"org", orgID, "model", aiModel, "rows", len(reportData.Chats),
				"ai_used", reportData.AIUsed, "ai_elapsed", time.Since(aiStart))
		}
		if len(reportData.Chats) == 0 && len(chats) > 0 {
			reportData = dailyreport.FallbackSummarize(reportDate, orgName, chats)
			reportData.GeneratedAt = genAt
			aiModel = "fallback"
		}
		// Fill any blank bullets from raw transcripts before compose.
		reportData = dailyreport.EnsureFilledSummaries(reportData, chats)
	}
	run.AIModel = aiModel

	filled := 0
	for _, c := range reportData.Chats {
		if strings.TrimSpace(c.Summary) != "" || len(c.Bullets) > 0 || len(c.Excerpts) > 0 {
			filled++
		}
	}
	a.Log.Info("Daily report phase=ai summaries ready",
		"org", orgID, "rows", len(reportData.Chats), "filled_rows", filled, "ai", aiModel)

	// Persist summary JSON before compose/send so History has data even if send fails later.
	if b, err := json.Marshal(reportData); err == nil {
		var m models.JSONB
		if json.Unmarshal(b, &m) == nil {
			run.SummaryJSON = m
		}
	}
	_ = a.DB.Model(&run).Updates(map[string]any{
		"ai_model":     run.AIModel,
		"summary_json": run.SummaryJSON,
		"error_message": run.ErrorMessage,
	}).Error

	// ── Phase 3: COMPOSE DOCX (only after summaries are ready) ────────
	a.Log.Info("Daily report phase=compose start", "org", orgID, "rows", len(reportData.Chats))
	docBytes, err := dailyreport.BuildDOCX(reportData)
	if err != nil {
		return fail("docx: " + err.Error())
	}
	if len(docBytes) < 64 {
		return fail("docx: generated file too small (empty or corrupt)")
	}

	filename := fmt.Sprintf("daily-chat-report-%s.docx", reportDate)
	docPath, err := a.saveDailyReportFile(orgID, reportDate, filename, docBytes)
	if err != nil {
		return fail("save docx: " + err.Error())
	}
	run.PDFPath = docPath
	run.PDFFilename = filename
	_ = a.DB.Model(&run).Updates(map[string]any{
		"pdf_path":     run.PDFPath,
		"pdf_filename": run.PDFFilename,
	}).Error
	a.Log.Info("Daily report phase=compose done",
		"org", orgID, "file", filename, "bytes", len(docBytes), "path", docPath)

	// ── Phase 4: SEND WHATSAPP (sync — only after collect + AI + DOCX) ─
	a.Log.Info("Daily report phase=send start",
		"org", orgID, "recipients", len(settings.Recipients), "template", settings.ReportTemplateName)
	sent, sendErrs := a.sendDailyReportFile(settings, &run, docBytes, filename, reportData)
	run.SentCount = sent
	if len(sendErrs) > 0 {
		run.SendErrors = strings.Join(sendErrs, "; ")
	}
	a.Log.Info("Daily report phase=send done",
		"org", orgID, "sent", sent, "errors", len(sendErrs))

	// ── Phase 5: FINALIZE ─────────────────────────────────────────────
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
	a.Log.Info("Daily report phase=complete",
		"org", orgID, "status", run.Status, "sent", sent, "ai", aiModel,
		"file", filename, "elapsed", time.Since(pipelineStart))
	return &run, nil
}

func (a *App) collectDailyChats(orgID uuid.UUID, waAccount, reportDate string, loc *time.Location) ([]dailyreport.ContactChat, int, error) {
	start, end, err := dailyreport.DayBounds(reportDate, loc)
	if err != nil {
		return nil, 0, err
	}

	// Prefer explicit Table+Scan with column aliases — custom Find/Select on
	// Model(&Message{}) can leave Content empty on some GORM/driver combos.
	type row struct {
		ContactID         uuid.UUID `gorm:"column:contact_id"`
		ProfileName       string    `gorm:"column:profile_name"`
		PhoneNumber       string    `gorm:"column:phone_number"`
		Direction         string    `gorm:"column:direction"`
		Content           string    `gorm:"column:content"`
		MessageType       string    `gorm:"column:message_type"`
		TemplateName      string    `gorm:"column:template_name"`
		MediaFilename     string    `gorm:"column:media_filename"`
		InteractiveJSON   string    `gorm:"column:interactive_json"`
		CreatedAt         time.Time `gorm:"column:created_at"`
	}

	// Cast jsonb → text so Scan never fails on interactive_data driver types.
	q := a.DB.Table("messages").
		Select(`messages.contact_id AS contact_id,
			contacts.profile_name AS profile_name,
			contacts.phone_number AS phone_number,
			messages.direction AS direction,
			COALESCE(messages.content, '') AS content,
			COALESCE(messages.message_type, '') AS message_type,
			COALESCE(messages.template_name, '') AS template_name,
			COALESCE(messages.media_filename, '') AS media_filename,
			COALESCE(messages.interactive_data::text, '') AS interactive_json,
			messages.created_at AS created_at`).
		Joins("JOIN contacts ON contacts.id = messages.contact_id AND contacts.deleted_at IS NULL").
		Where("messages.organization_id = ? AND messages.deleted_at IS NULL", orgID).
		Where("messages.created_at >= ? AND messages.created_at < ?", start, end).
		Order("messages.contact_id asc, messages.created_at asc")

	if strings.TrimSpace(waAccount) != "" {
		q = q.Where("messages.whats_app_account = ?", waAccount)
	}

	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	emptyContent := 0
	withText := 0
	for _, r := range rows {
		if strings.TrimSpace(r.Content) == "" {
			emptyContent++
		} else {
			withText++
		}
	}

	a.Log.Info("Daily report collect diagnostics",
		"org", orgID,
		"report_date", reportDate,
		"timezone", loc.String(),
		"start_utc", start.UTC().Format(time.RFC3339),
		"end_utc", end.UTC().Format(time.RFC3339),
		"account_filter", strings.TrimSpace(waAccount),
		"raw_rows", len(rows),
		"rows_with_content", withText,
		"rows_empty_content", emptyContent,
	)

	byContact := map[uuid.UUID]*dailyreport.ContactChat{}
	order := make([]uuid.UUID, 0)
	total := 0

	for _, r := range rows {
		if r.ContactID == uuid.Nil {
			continue
		}
		total++
		cc, ok := byContact[r.ContactID]
		if !ok {
			if len(byContact) >= maxChatsPerReport {
				continue
			}
			name := strings.TrimSpace(r.ProfileName)
			if name == "" {
				name = strings.TrimSpace(r.PhoneNumber)
			}
			if name == "" {
				name = "Unknown"
			}
			cc = &dailyreport.ContactChat{
				ContactID: r.ContactID.String(),
				Name:      name,
				Phone:     strings.TrimSpace(r.PhoneNumber),
				ChatDate:  reportDate,
				Messages:  nil,
			}
			byContact[r.ContactID] = cc
			order = append(order, r.ContactID)
		}
		if len(cc.Messages) >= maxMessagesPerContact {
			continue
		}
		var interactiveRaw []byte
		if s := strings.TrimSpace(r.InteractiveJSON); s != "" && s != "null" && s != "{}" {
			interactiveRaw = []byte(s)
		}
		text := extractMessageText(r.Content, r.MessageType, r.TemplateName, r.MediaFilename, interactiveRaw)
		if utf8.RuneCountInString(text) > maxMsgChars {
			text = string([]rune(text)[:maxMsgChars]) + "..."
		}
		dir := strings.TrimSpace(r.Direction)
		if dir == "" {
			dir = string(models.DirectionIncoming)
		}
		at := ""
		if !r.CreatedAt.IsZero() {
			at = r.CreatedAt.In(loc).Format("15:04")
		}
		cc.Messages = append(cc.Messages, dailyreport.ChatMessage{
			Direction: dir,
			At:        at,
			Text:      text,
		})
		cc.MessageCount++
	}

	out := make([]dailyreport.ContactChat, 0, len(order))
	msgsWithText := 0
	for i, id := range order {
		cc := *byContact[id]
		cc.Serial = i + 1
		for _, m := range cc.Messages {
			if strings.TrimSpace(m.Text) != "" {
				msgsWithText++
			}
		}
		out = append(out, cc)
	}

	// Sample first chat for ops debugging (no full PII dump)
	sampleLen := 0
	sampleDir := ""
	if len(out) > 0 && len(out[0].Messages) > 0 {
		sampleLen = utf8.RuneCountInString(out[0].Messages[0].Text)
		sampleDir = out[0].Messages[0].Direction
	}
	a.Log.Info("Daily report collect result",
		"org", orgID,
		"contacts", len(out),
		"message_rows", total,
		"messages_with_text", msgsWithText,
		"sample_msg_runes", sampleLen,
		"sample_dir", sampleDir,
	)
	return out, total, nil
}

// extractMessageText builds a human-readable line for the report from DB fields.
// Content is preferred; interactive JSON / media / template fill in when content is empty.
func extractMessageText(content, msgType, templateName, mediaFilename string, interactiveRaw []byte) string {
	text := strings.TrimSpace(content)
	if text != "" {
		return text
	}

	// Interactive payload often has body / button titles when content was not denormalized.
	if len(interactiveRaw) > 0 && string(interactiveRaw) != "null" && string(interactiveRaw) != "{}" {
		var m map[string]any
		if json.Unmarshal(interactiveRaw, &m) == nil {
			if body, ok := m["body"].(string); ok && strings.TrimSpace(body) != "" {
				return strings.TrimSpace(body)
			}
			if bt, ok := m["button_text"].(string); ok && strings.TrimSpace(bt) != "" {
				return strings.TrimSpace(bt)
			}
			if dt, ok := m["display_text"].(string); ok && strings.TrimSpace(dt) != "" {
				return strings.TrimSpace(dt)
			}
			// button reply style: {type, buttons:[{title}]} or single title
			if title, ok := m["title"].(string); ok && strings.TrimSpace(title) != "" {
				return strings.TrimSpace(title)
			}
			if buttons, ok := m["buttons"].([]any); ok && len(buttons) > 0 {
				titles := make([]string, 0, len(buttons))
				for _, b := range buttons {
					if bm, ok := b.(map[string]any); ok {
						if t, ok := bm["title"].(string); ok && strings.TrimSpace(t) != "" {
							titles = append(titles, strings.TrimSpace(t))
						}
					}
				}
				if len(titles) > 0 {
					return "[buttons: " + strings.Join(titles, " | ") + "]"
				}
			}
		}
	}

	mt := strings.ToLower(strings.TrimSpace(msgType))
	switch mt {
	case string(models.MessageTypeInteractive), "button_reply", "button", "list_reply":
		return "[interactive reply]"
	case string(models.MessageTypeImage):
		return "[image]"
	case string(models.MessageTypeDocument):
		if mediaFilename != "" {
			return "[document: " + mediaFilename + "]"
		}
		return "[document]"
	case string(models.MessageTypeAudio):
		return "[audio]"
	case string(models.MessageTypeVideo):
		return "[video]"
	case string(models.MessageTypeTemplate):
		if templateName != "" {
			return "[template: " + templateName + "]"
		}
		return "[template]"
	case string(models.MessageTypeFlow), "nfm_reply":
		return "[flow response]"
	case string(models.MessageTypeLocation):
		return "[location]"
	case string(models.MessageTypeContact):
		return "[contact card]"
	case string(models.MessageTypeReaction):
		return "[reaction]"
	default:
		if mediaFilename != "" {
			return "[file: " + mediaFilename + "]"
		}
		if mt != "" {
			return "[" + mt + "]"
		}
		return "[message]"
	}
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

		batchNo := start/aiBatchSize + 1
		totalBatches := (len(chats) + aiBatchSize - 1) / aiBatchSize
		a.Log.Info("Daily report AI batch waiting",
			"org", orgID, "batch", batchNo, "of", totalBatches,
			"from_id", batch[0].Serial, "to_id", batch[len(batch)-1].Serial, "count", len(batch))

		batchStart := time.Now()
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
		a.Log.Info("Daily report AI batch complete",
			"org", orgID, "batch", batchNo, "of", totalBatches,
			"items", len(parsed.Items), "elapsed", time.Since(batchStart))
	}

	// All AI batches finished — only then merge + return (caller composes DOCX next).
	merged := dailyreport.MergeAISummaries(reportDate, orgName, generatedAt, modelLabel, chats, allItems)
	a.Log.Info("Daily report AI all batches complete",
		"org", orgID, "items", len(allItems), "rows", len(merged.Chats), "model", modelLabel)
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
		// Long enough for Meta upload + template/document send (sync wait).
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)

		// Sync send options — wait for Meta before marking recipient done.
		sendOpts := DailyReportSendOptions()

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
			if _, err := a.SendOutgoingMessage(ctx, req, sendOpts); err != nil {
				lastErr = err
				a.Log.Warn("Daily report template send failed", "recipient", r.Name, "error", err)
			} else {
				templateOK = true
				a.Log.Info("Daily report template sent", "recipient", r.Name, "phone", phone)
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
			if _, err := a.SendOutgoingMessage(ctx, req2, sendOpts); err != nil {
				lastErr = err
				a.Log.Warn("Daily report free-form document failed", "recipient", r.Name, "error", err)
			} else {
				docOK = true
				a.Log.Info("Daily report document sent", "recipient", r.Name, "phone", phone)
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
