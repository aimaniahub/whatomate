package dailyreport

import "strings"

func nonEmpty(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}
