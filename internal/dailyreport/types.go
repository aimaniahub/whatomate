package dailyreport

// ChatMessage is one turn included in the report input.
type ChatMessage struct {
	Direction string `json:"direction"`
	At        string `json:"at"`
	Text      string `json:"text"`
}

// ContactChat is one contact's activity for the report day (source data from DB).
type ContactChat struct {
	// Serial is 1-based row id used to map AI summaries without sending name/phone to the model.
	Serial       int           `json:"serial"`
	ContactID    string        `json:"contact_id"`
	Name         string        `json:"name"`
	Phone        string        `json:"phone"`
	// ChatDate is local YYYY-MM-DD (usually the report date).
	ChatDate     string        `json:"chat_date"`
	MessageCount int           `json:"message_count"`
	Messages     []ChatMessage `json:"messages"`
}

// ChatSummary is one report row after AI merge / fallback.
type ChatSummary struct {
	Serial int    `json:"serial"`
	Date   string `json:"date"`
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	// Bullets are max 3 short AI (or rule-based) summary points.
	Bullets []string `json:"bullets"`
	// Summary is bullets joined for storage/search (optional).
	Summary string `json:"summary"`
	// Excerpts are raw transcript lines so the DOCX never shows a blank Summary cell.
	Excerpts []string `json:"excerpts,omitempty"`
}

// DayOverview is the day-level rollup.
type DayOverview struct {
	TotalChats int    `json:"total_chats"`
	Notes      string `json:"notes"`
}

// ReportData is the structured report used for PDF + storage.
type ReportData struct {
	ReportDate    string        `json:"report_date"`
	GeneratedAt   string        `json:"generated_at"` // local display time
	OrgName       string        `json:"org_name,omitempty"`
	Overview      DayOverview   `json:"overview"`
	Chats         []ChatSummary `json:"chats"`
	EmptyDay      bool          `json:"empty_day"`
	AIUsed        bool          `json:"ai_used"`
	AIModel       string        `json:"ai_model,omitempty"`
}

// AIChatPayload is what we send to the model (no PII name/phone).
type AIChatPayload struct {
	ID       int           `json:"id"`
	Messages []ChatMessage `json:"messages"`
}

// AISummaryItem is one AI response row keyed by id.
type AISummaryItem struct {
	ID      int      `json:"id"`
	Bullets []string `json:"bullets"`
}

// AISummaryResponse is the expected model JSON.
type AISummaryResponse struct {
	Items []AISummaryItem `json:"items"`
}
