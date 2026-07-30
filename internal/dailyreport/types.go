package dailyreport

// ChatMessage is one turn included in the report input.
type ChatMessage struct {
	Direction string `json:"direction"`
	At        string `json:"at"`
	Text      string `json:"text"`
}

// ContactChat is one contact's activity for the report day.
type ContactChat struct {
	ContactID    string        `json:"contact_id"`
	Name         string        `json:"name"`
	Phone        string        `json:"phone"`
	MessageCount int           `json:"message_count"`
	Messages     []ChatMessage `json:"messages"`
}

// ChatSummary is the AI (or fallback) summary for one contact.
type ChatSummary struct {
	Name          string `json:"name"`
	Phone         string `json:"phone"`
	Summary       string `json:"summary"`
	Intent        string `json:"intent"`
	CallRequested bool   `json:"call_requested"`
	Priority      string `json:"priority"`
	NextAction    string `json:"next_action"`
}

// DayOverview is the day-level rollup.
type DayOverview struct {
	TotalChats    int      `json:"total_chats"`
	TopIntents    []string `json:"top_intents"`
	NeedsCallback int      `json:"needs_callback"`
	Notes         string   `json:"notes"`
}

// ReportData is the structured report used for PDF + storage.
type ReportData struct {
	ReportDate string        `json:"report_date"`
	OrgName    string        `json:"org_name,omitempty"`
	Overview   DayOverview   `json:"overview"`
	Chats      []ChatSummary `json:"chats"`
	EmptyDay   bool          `json:"empty_day"`
}
