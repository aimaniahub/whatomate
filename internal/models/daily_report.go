package models

import (
	"time"

	"github.com/google/uuid"
)

// Daily report run statuses.
const (
	DailyReportStatusPending   = "pending"
	DailyReportStatusRunning   = "running"
	DailyReportStatusCompleted = "completed"
	DailyReportStatusFailed    = "failed"
	DailyReportStatusEmpty     = "empty" // completed with zero chats (still may send "no chats" PDF)
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

// DailyReportSettings is per-organization configuration for end-of-day chat reports.
type DailyReportSettings struct {
	BaseModel
	OrganizationID  uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"organization_id"`
	WhatsAppAccount string    `gorm:"size:100" json:"whatsapp_account"` // Account used to send the PDF
	Enabled         bool      `gorm:"default:false" json:"enabled"`
	Timezone        string    `gorm:"size:64;default:'Asia/Kolkata'" json:"timezone"`
	SendTime        string    `gorm:"size:5;default:'20:00'" json:"send_time"` // HH:MM local
	ReportLanguage  string    `gorm:"size:10;default:'en'" json:"report_language"`

	Organization *Organization           `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
	Recipients   []DailyReportRecipient  `gorm:"foreignKey:SettingsID" json:"recipients,omitempty"`
}

func (DailyReportSettings) TableName() string {
	return "daily_report_settings"
}

// DailyReportRecipient is a person who receives the PDF (max 2 per org).
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
