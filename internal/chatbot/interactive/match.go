package interactive

import (
	"strings"
	"unicode"
)

// MatchResult is a resolved option selection.
type MatchResult struct {
	OptionID string
	// Source: "id" | "title" | "alias" | "button_id"
	Source string
}

// Normalize collapses case, trim, and internal whitespace for comparison.
func Normalize(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// ResolveInput maps an inbound buttonID and/or free text to an option.
// Priority: buttonID exact → option id → exact title → alias.
// Returns nil if nothing matches.
func ResolveInput(opts []Option, buttonID, text string) *MatchResult {
	if buttonID != "" {
		bid := strings.TrimSpace(buttonID)
		for _, o := range opts {
			if o.ID == bid {
				return &MatchResult{OptionID: o.ID, Source: "button_id"}
			}
		}
		// Meta sometimes echoes id that equals title
		n := Normalize(bid)
		for _, o := range opts {
			if Normalize(o.ID) == n || Normalize(o.Title) == n {
				return &MatchResult{OptionID: o.ID, Source: "button_id"}
			}
		}
	}

	nText := Normalize(text)
	if nText == "" {
		return nil
	}

	for _, o := range opts {
		if Normalize(o.ID) == nText {
			return &MatchResult{OptionID: o.ID, Source: "id"}
		}
	}
	for _, o := range opts {
		if Normalize(o.Title) == nText {
			return &MatchResult{OptionID: o.ID, Source: "title"}
		}
	}
	for _, o := range opts {
		for _, a := range o.Aliases {
			if Normalize(a) == nText {
				return &MatchResult{OptionID: o.ID, Source: "alias"}
			}
		}
	}
	return nil
}

// OptionsFromButtonMaps extracts options from node button config maps.
func OptionsFromButtonMaps(buttons []map[string]any) []Option {
	out := make([]Option, 0, len(buttons))
	for _, b := range buttons {
		id, _ := b["id"].(string)
		title, _ := b["title"].(string)
		if id == "" && title == "" {
			continue
		}
		if id == "" {
			id = title
		}
		out = append(out, Option{ID: id, Title: title})
	}
	return out
}

// ResolveAgainstButtons is a convenience for live node config without a stored contract.
func ResolveAgainstButtons(buttons []map[string]any, buttonID, text string) *MatchResult {
	return ResolveInput(OptionsFromButtonMaps(buttons), buttonID, text)
}
