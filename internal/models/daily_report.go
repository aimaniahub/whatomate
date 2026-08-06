package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Daily report run statuses.
const (
	DailyReportStatusPending   = "pending"
	DailyReportStatusRunning   = "running"
	DailyReportStatusCompleted = "completed"
	DailyReportStatusFailed    = "failed"
	DailyReportStatusEmpty     = "empty" // completed with zero chats (still may send "no chats" DOCX)
)

// Daily report trigger sources.
const (
	DailyReportTriggerSchedule = "schedule"
	DailyReportTriggerManual   = "manual"
)

// MaxDailyReportRecipients is the hard cap for report delivery targets.
const MaxDailyReportRecipients = 2

// DefaultDailyReportTimezone is used when settings leave timezone empty.
const DefaultDailyReportTimezone = "Asia/Kolkata"

// DefaultDailyReportSendTime is local HH:MM when not configured.
const DefaultDailyReportSendTime = "20:00"

// DefaultDailyReportAISystemPrompt is the built-in summarizer prompt (editable per org).
const DefaultDailyReportAISystemPrompt = `You summarize WhatsApp chat transcripts for an operations daily report.
Return ONLY valid JSON (no markdown fences) with this exact shape:
{"items":[{"id":1,"bullets":["point one","point two","point three"]}]}

Rules:
- Each input chat has an integer "id". Echo the same id in your output.
- For each id write at most 3 short bullet points (max 20 words each).
- Capture the customer's overall query / intent only.
- Base bullets ONLY on the messages provided. Do not invent facts.
- Do not include names, phone numbers, or greetings.
- Prefer inbound (customer) messages.`

// DailyReportSettings is per-organization configuration for end-of-day chat reports.
type DailyReportSettings struct {
	BaseModel
	OrganizationID  uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"organization_id"`
	WhatsAppAccount string    `gorm:"size:100" json:"whatsapp_account"` // Account used to send + filter chats
	Enabled         bool      `gorm:"default:false;index" json:"enabled"`
	Timezone        string    `gorm:"size:64;default:'Asia/Kolkata'" json:"timezone"`
	SendTime        string    `gorm:"size:8;default:'20:00'" json:"send_time"` // HH:MM local (normalized)
	ReportLanguage  string    `gorm:"size:10;default:'en'" json:"report_language"`

	// Selected WhatsApp template (dropdown on Daily Reports). Prefer APPROVED UTILITY
	// with TEXT header for notify-outside-24h. DOCUMENT header optional for attach-in-header.
	// Body vars by order: {{1}}=date, {{2}}=chat count, {{3}}=report type (if present).
	ReportTemplateName     string `gorm:"size:255" json:"report_template_name"`
	ReportTemplateLanguage string `gorm:"size:20;default:'en'" json:"report_template_language"`

	// Dedicated AI settings for daily reports (independent of chatbot settings UI).
	AIEnabled      bool    `gorm:"default:false" json:"ai_enabled"`
	AIProvider     string  `gorm:"size:30" json:"ai_provider"` // openai, openrouter, anthropic, google
	AIAPIKey       string  `gorm:"column:ai_api_key;type:text" json:"-"`
	AIModel        string  `gorm:"size:120" json:"ai_model"`
	AIMaxTokens    int     `gorm:"default:3000" json:"ai_max_tokens"`
	AITemperature  float64 `gorm:"type:decimal(3,2);default:0.3" json:"ai_temperature"`
	AISystemPrompt string  `gorm:"type:text" json:"ai_system_prompt"`

	// Persistent schedule audit (updated after each scheduled fire).
	LastScheduledAt     *time.Time `json:"last_scheduled_at,omitempty"`
	LastScheduledDate   string     `gorm:"size:10" json:"last_scheduled_date,omitempty"` // YYYY-MM-DD
	LastScheduledStatus string     `gorm:"size:20" json:"last_scheduled_status,omitempty"`
	LastScheduledRunID  *uuid.UUID `gorm:"type:uuid" json:"last_scheduled_run_id,omitempty"`

	Organization *Organization          `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
	Recipients   []DailyReportRecipient `gorm:"foreignKey:SettingsID" json:"recipients,omitempty"`
}

// DailyReportAIReady reports whether dedicated report AI can be used.
func (s *DailyReportSettings) DailyReportAIReady() bool {
	if s == nil {
		return false
	}
	return s.AIEnabled && strings.TrimSpace(s.AIProvider) != "" &&
		strings.TrimSpace(s.AIAPIKey) != "" && strings.TrimSpace(s.AIModel) != ""
}

func (DailyReportSettings) TableName() string {
	return "daily_report_settings"
}

// DailyReportRecipient is a person who receives the report notify (max 2 per org).
type DailyReportRecipient struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;index;not null" json:"organization_id"`
	SettingsID     uuid.UUID `gorm:"type:uuid;index;not null" json:"settings_id"`
	Name           string    `gorm:"size:255;not null" json:"name"`
	PhoneNumber    string    `gorm:"size:50;not null" json:"phone_number"`
	IsActive       bool      `gorm:"default:true" json:"is_active"`
	SortOrder      int       `gorm:"default:0" json:"sort_order"`

	Settings *DailyReportSettings `gorm:"foreignKey:SettingsID" json:"settings,omitempty"`
}

func (DailyReportRecipient) TableName() string {
	return "daily_report_recipients"
}

// DailyReportRun is one generation attempt for a calendar day in the org timezone.
type DailyReportRun struct {
	BaseModel
	OrganizationID uuid.UUID  `gorm:"type:uuid;index;not null;uniqueIndex:idx_daily_report_org_date" json:"organization_id"`
	ReportDate     string     `gorm:"size:10;not null;uniqueIndex:idx_daily_report_org_date" json:"report_date"` // YYYY-MM-DD
	Status         string     `gorm:"size:20;default:'pending';index" json:"status"`
	TriggeredBy    string     `gorm:"size:20;default:'schedule'" json:"triggered_by"`
	ChatCount      int        `gorm:"default:0" json:"chat_count"`
	MessageCount   int        `gorm:"default:0" json:"message_count"`
	PDFPath        string     `gorm:"type:text" json:"pdf_path,omitempty"`
	PDFFilename    string     `gorm:"size:255" json:"pdf_filename,omitempty"`
	AIModel        string     `gorm:"size:100" json:"ai_model,omitempty"`
	ErrorMessage   string     `gorm:"type:text" json:"error_message,omitempty"`
	SummaryJSON    JSONB      `gorm:"type:jsonb;default:'{}'" json:"summary_json,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	SentCount      int        `gorm:"default:0" json:"sent_count"`
	SendErrors     string     `gorm:"type:text" json:"send_errors,omitempty"`

	Organization *Organization `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
}

func (DailyReportRun) TableName() string {
	return "daily_report_runs"
}
