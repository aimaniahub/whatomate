package dailyreport

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// FallbackSummarize builds a structured report without calling an LLM (bullets from inbound text).
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
				Notes:      "No customer chats were recorded for this day.",
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
		inbound := collectInboundTexts(c.Messages)
		bullets := make([]string, 0, 3)
		for _, t := range inbound {
			if len(bullets) >= 3 {
				break
			}
			bullets = append(bullets, truncateRunes(t, 120))
		}
		if len(bullets) == 0 {
			bullets = []string{"Customer messaged with no clear text content."}
		}
		date := c.ChatDate
		if date == "" {
			date = reportDate
		}
		out = append(out, ChatSummary{
			Serial:  serial,
			Date:    date,
			Name:    nonEmpty(c.Name, c.Phone),
			Phone:   c.Phone,
			Bullets: bullets,
			Summary: strings.Join(bullets, " • "),
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
			Notes:      fmt.Sprintf("Summarized %d chats (rule-based; AI not used).", len(out)),
		},
		Chats: out,
	}
}

// MergeAISummaries maps AI bullets by serial id onto source chats (name/phone/date from DB).
func MergeAISummaries(reportDate, orgName, generatedAt, aiModel string, chats []ContactChat, items []AISummaryItem) ReportData {
	byID := map[int][]string{}
	for _, it := range items {
		byID[it.ID] = normalizeBullets(it.Bullets, 3)
	}

	out := make([]ChatSummary, 0, len(chats))
	for i, c := range chats {
		serial := c.Serial
		if serial <= 0 {
			serial = i + 1
		}
		bullets := byID[serial]
		if len(bullets) == 0 {
			// AI missed this id — short fallback from transcript
			inbound := collectInboundTexts(c.Messages)
			for _, t := range inbound {
				if len(bullets) >= 3 {
					break
				}
				bullets = append(bullets, truncateRunes(t, 120))
			}
			if len(bullets) == 0 {
				bullets = []string{"No clear customer query extracted."}
			}
		}
		date := c.ChatDate
		if date == "" {
			date = reportDate
		}
		out = append(out, ChatSummary{
			Serial:  serial,
			Date:    date,
			Name:    nonEmpty(c.Name, c.Phone),
			Phone:   c.Phone,
			Bullets: bullets,
			Summary: strings.Join(bullets, " • "),
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
			Notes:      fmt.Sprintf("AI summarized %d chats (%s).", len(out), aiModel),
		},
		Chats: out,
	}
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
		b = truncateRunes(b, 160)
		out = append(out, b)
		if len(out) >= max {
			break
		}
	}
	return out
}

func collectInboundTexts(msgs []ChatMessage) []string {
	var out []string
	for _, m := range msgs {
		if !strings.EqualFold(m.Direction, "incoming") {
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

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "..."
}
