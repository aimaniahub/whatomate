package dailyreport

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// FallbackSummarize builds a structured report without calling an LLM.
func FallbackSummarize(reportDate string, orgName string, chats []ContactChat) ReportData {
	if len(chats) == 0 {
		return ReportData{
			ReportDate: reportDate,
			OrgName:    orgName,
			EmptyDay:   true,
			Overview: DayOverview{
				TotalChats: 0,
				Notes:      "No customer chats were recorded for this day.",
			},
			Chats: []ChatSummary{},
		}
	}

	out := make([]ChatSummary, 0, len(chats))
	callback := 0
	intentCounts := map[string]int{}

	for _, c := range chats {
		inbound := collectInboundTexts(c.Messages)
		summary := "Customer messaged with no clear text content."
		if len(inbound) > 0 {
			joined := strings.Join(inbound, " | ")
			summary = "Customer discussed: " + truncateRunes(joined, 280)
		}
		intent := guessIntent(inbound)
		intentCounts[intent]++
		callReq := looksLikeCallRequest(inbound)
		if callReq {
			callback++
		}
		prio := "normal"
		if callReq {
			prio = "high"
		}
		next := "Review chat and follow up if needed"
		if callReq {
			next = "Call the customer back"
		}
		out = append(out, ChatSummary{
			Name:          nonEmpty(c.Name, c.Phone),
			Phone:         c.Phone,
			Summary:       summary,
			Intent:        intent,
			CallRequested: callReq,
			Priority:      prio,
			NextAction:    next,
		})
	}

	top := topKeys(intentCounts, 5)
	return ReportData{
		ReportDate: reportDate,
		OrgName:    orgName,
		EmptyDay:   false,
		Overview: DayOverview{
			TotalChats:    len(out),
			TopIntents:    top,
			NeedsCallback: callback,
			Notes:         fmt.Sprintf("Summarized %d chats (rule-based; AI not used).", len(out)),
		},
		Chats: out,
	}
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

func looksLikeCallRequest(inbound []string) bool {
	blob := strings.ToLower(strings.Join(inbound, " "))
	keys := []string{"call me", "call us", "please call", "callback", "call back", "phone me", "ring me", "talk to someone", "speak to"}
	for _, k := range keys {
		if strings.Contains(blob, k) {
			return true
		}
	}
	return false
}

func guessIntent(inbound []string) string {
	blob := strings.ToLower(strings.Join(inbound, " "))
	switch {
	case strings.Contains(blob, "price") || strings.Contains(blob, "cost") || strings.Contains(blob, "rate"):
		return "pricing"
	case strings.Contains(blob, "register") || strings.Contains(blob, "registration"):
		return "registration"
	case strings.Contains(blob, "iot") || strings.Contains(blob, "sensor"):
		return "iot"
	case strings.Contains(blob, "sandalwood"):
		return "sandalwood"
	case strings.Contains(blob, "deliver"):
		return "delivery"
	case looksLikeCallRequest(inbound):
		return "callback"
	default:
		return "general"
	}
}

func topKeys(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	// simple selection sort by count desc
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].v > list[i].v {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	if n > len(list) {
		n = len(list)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, list[i].k)
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
