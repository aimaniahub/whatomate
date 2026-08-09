package dailyreport

import (
	"strings"
	"time"
	"unicode/utf8"
)

// FallbackSummarize builds a structured report without calling an LLM (bullets from transcript).
func FallbackSummarize(reportDate string, orgName string, chats []ContactChat) ReportData {
	genAt := time.Now().Format("2006-01-02 15:04")
	if len(chats) == 0 {
		return ReportData{
			ReportDate:  reportDate,
			GeneratedAt: genAt,
			OrgName:     orgName,
			EmptyDay:    true,
			AIUsed:      false,
			AIModel:     "none",
			Overview: DayOverview{
				TotalChats: 0,
				Notes:      "",
			},
			Chats: []ChatSummary{},
		}
	}

	out := make([]ChatSummary, 0, len(chats))
	for i, c := range chats {
		serial := c.Serial
		if serial <= 0 {
			serial = i + 1
		}
		bullets := BulletsFromMessages(c.Messages, 3)
		date := c.ChatDate
		if date == "" {
			date = reportDate
		}
		out = append(out, ChatSummary{
			Serial:   serial,
			Date:     date,
			Name:     nonEmpty(c.Name, c.Phone),
			Phone:    c.Phone,
			Bullets:  bullets,
			Summary:  strings.Join(bullets, " • "),
			Excerpts: MessageExcerpts(c.Messages, 5),
		})
	}

	return ReportData{
		ReportDate:  reportDate,
		GeneratedAt: genAt,
		OrgName:     orgName,
		EmptyDay:    false,
		AIUsed:      false,
		AIModel:     "fallback",
		Overview: DayOverview{
			TotalChats: len(out),
			Notes:      "",
		},
		Chats: out,
	}
}

// MergeAISummaries maps AI bullets by serial id onto source chats (name/phone/date from DB).
// Always attaches message excerpts so the DOCX never has a blank summary cell.
func MergeAISummaries(reportDate, orgName, generatedAt, aiModel string, chats []ContactChat, items []AISummaryItem) ReportData {
	// Detect accidental 0-based ids (model returns 0..n-1 instead of 1..n).
	zeroBased := false
	for _, it := range items {
		if it.ID == 0 && len(normalizeBullets(it.Bullets, 3)) > 0 {
			zeroBased = true
			break
		}
	}

	byID := map[int][]string{}
	for _, it := range items {
		bullets := normalizeBullets(it.Bullets, 3)
		if len(bullets) == 0 {
			continue
		}
		id := it.ID
		if zeroBased {
			// Map 0→1, 1→2, ...
			id = it.ID + 1
		}
		byID[id] = bullets
	}

	out := make([]ChatSummary, 0, len(chats))
	for i, c := range chats {
		serial := c.Serial
		if serial <= 0 {
			serial = i + 1
		}
		bullets := byID[serial]
		if len(bullets) == 0 {
			// AI missed this id — build from transcript (incoming first, then any).
			bullets = BulletsFromMessages(c.Messages, 3)
		}
		date := c.ChatDate
		if date == "" {
			date = reportDate
		}
		out = append(out, ChatSummary{
			Serial:   serial,
			Date:     date,
			Name:     nonEmpty(c.Name, c.Phone),
			Phone:    c.Phone,
			Bullets:  bullets,
			Summary:  strings.Join(bullets, " • "),
			Excerpts: MessageExcerpts(c.Messages, 5),
		})
	}

	if generatedAt == "" {
		generatedAt = time.Now().Format("2006-01-02 15:04")
	}
	return ReportData{
		ReportDate:  reportDate,
		GeneratedAt: generatedAt,
		OrgName:     orgName,
		EmptyDay:    false,
		AIUsed:      true,
		AIModel:     aiModel,
		Overview: DayOverview{
			TotalChats: len(out),
			Notes:      "",
		},
		Chats: out,
	}
}

// EnsureFilledSummaries guarantees every chat row has non-empty bullets/summary/excerpts
// from source transcripts. Call before BuildDOCX so the Word file never has blank summary cells.
func EnsureFilledSummaries(data ReportData, chats []ContactChat) ReportData {
	if len(chats) == 0 {
		return data
	}
	bySerial := map[int]ContactChat{}
	for i, c := range chats {
		s := c.Serial
		if s <= 0 {
			s = i + 1
		}
		bySerial[s] = c
	}

	// If AI/merge dropped all rows, rebuild from source.
	if len(data.Chats) == 0 {
		filled := FallbackSummarize(data.ReportDate, data.OrgName, chats)
		filled.GeneratedAt = data.GeneratedAt
		filled.AIUsed = data.AIUsed
		filled.AIModel = data.AIModel
		return filled
	}

	for i := range data.Chats {
		row := &data.Chats[i]
		src, ok := bySerial[row.Serial]
		if !ok && i < len(chats) {
			src = chats[i]
			ok = true
		}
		if row.Name == "" && ok {
			row.Name = nonEmpty(src.Name, src.Phone)
		}
		if row.Phone == "" && ok {
			row.Phone = src.Phone
		}
		if row.Date == "" {
			if ok && src.ChatDate != "" {
				row.Date = src.ChatDate
			} else {
				row.Date = data.ReportDate
			}
		}

		// Drop empty / whitespace-only bullets
		row.Bullets = normalizeBullets(row.Bullets, 3)
		if len(row.Bullets) == 0 && ok {
			row.Bullets = BulletsFromMessages(src.Messages, 3)
		}
		if len(row.Bullets) == 0 {
			row.Bullets = []string{"No message text available for this chat."}
		}
		row.Summary = strings.Join(row.Bullets, " • ")

		if len(row.Excerpts) == 0 && ok {
			row.Excerpts = MessageExcerpts(src.Messages, 5)
		}
		if len(row.Excerpts) == 0 {
			// Last resort: mirror bullets so DOCX cell is never empty.
			row.Excerpts = append([]string{}, row.Bullets...)
		}
	}

	data.EmptyDay = false
	data.Overview.TotalChats = len(data.Chats)
	return data
}

// BulletsFromMessages builds up to max short summary lines from a chat transcript.
// Prefers customer/incoming messages; falls back to any direction so bot-only
// or mis-tagged rows still produce visible report content.
func BulletsFromMessages(msgs []ChatMessage, max int) []string {
	if max <= 0 {
		max = 3
	}
	incoming := collectTextsByDirection(msgs, true)
	if len(incoming) > 0 {
		return truncateList(incoming, max, 120)
	}
	any := collectTextsByDirection(msgs, false)
	if len(any) > 0 {
		return truncateList(any, max, 120)
	}
	if len(msgs) == 0 {
		return []string{"No messages recorded for this contact on this day."}
	}
	return []string{"Messages present but no readable text content."}
}

// MessageExcerpts returns short transcript lines for the DOCX (direction + time + text).
func MessageExcerpts(msgs []ChatMessage, max int) []string {
	if max <= 0 {
		max = 5
	}
	out := make([]string, 0, max)
	for _, m := range msgs {
		t := strings.TrimSpace(m.Text)
		if t == "" {
			continue
		}
		t = truncateRunes(t, 160)
		dir := "out"
		if isIncomingDirection(m.Direction) {
			dir = "in"
		}
		prefix := dir
		if at := strings.TrimSpace(m.At); at != "" {
			prefix = dir + " " + at
		}
		out = append(out, prefix+": "+t)
		if len(out) >= max {
			break
		}
	}
	return out
}

func normalizeBullets(in []string, max int) []string {
	out := make([]string, 0, max)
	for _, b := range in {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		// strip leading bullet markers
		b = strings.TrimLeft(b, "•-* \t")
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		// skip pure placeholder junk from some models
		if b == "—" || b == "-" || b == "n/a" || b == "N/A" || b == "null" {
			continue
		}
		b = truncateRunes(b, 160)
		out = append(out, b)
		if len(out) >= max {
			break
		}
	}
	return out
}

// collectTextsByDirection when incomingOnly is true keeps customer-side messages;
// when false keeps all non-empty texts.
func collectTextsByDirection(msgs []ChatMessage, incomingOnly bool) []string {
	var out []string
	for _, m := range msgs {
		if incomingOnly && !isIncomingDirection(m.Direction) {
			continue
		}
		t := strings.TrimSpace(m.Text)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

func isIncomingDirection(dir string) bool {
	d := strings.ToLower(strings.TrimSpace(dir))
	switch d {
	case "incoming", "inbound", "in", "customer", "user":
		return true
	default:
		return false
	}
}

func truncateList(in []string, max, runeMax int) []string {
	out := make([]string, 0, max)
	for _, t := range in {
		out = append(out, truncateRunes(t, runeMax))
		if len(out) >= max {
			break
		}
	}
	return out
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "..."
}
