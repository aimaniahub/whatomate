package dailyreport

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// BuildPDF renders a simple multi-page PDF for the daily report.
// Uses core Helvetica only (no external fonts/deps).
func BuildPDF(data ReportData) ([]byte, error) {
	var b pdfBuilder
	b.begin()

	title := "Daily Chat Report"
	if data.ReportDate != "" {
		title = fmt.Sprintf("Daily Chat Report - %s", data.ReportDate)
	}
	b.setFont(16, true)
	b.textLine(title)
	b.setFont(10, false)
	if data.OrgName != "" {
		b.textLine("Organization: " + data.OrgName)
	}
	b.textLine("Generated: " + time.Now().Format(time.RFC3339))
	b.blank()

	if data.EmptyDay || len(data.Chats) == 0 {
		b.setFont(12, true)
		b.textLine("No chats today")
		b.setFont(10, false)
		b.textLine("There were no customer conversations recorded for this day.")
		if data.Overview.Notes != "" {
			b.blank()
			b.wrapText(data.Overview.Notes, 90)
		}
		return b.end()
	}

	b.setFont(12, true)
	b.textLine("Day overview")
	b.setFont(10, false)
	b.textLine(fmt.Sprintf("Total chats: %d", data.Overview.TotalChats))
	b.textLine(fmt.Sprintf("Needs callback: %d", data.Overview.NeedsCallback))
	if len(data.Overview.TopIntents) > 0 {
		b.textLine("Top intents: " + strings.Join(data.Overview.TopIntents, ", "))
	}
	if data.Overview.Notes != "" {
		b.wrapText("Notes: "+data.Overview.Notes, 90)
	}
	b.blank()

	b.setFont(12, true)
	b.textLine("Conversations")
	b.blank()

	for i, c := range data.Chats {
		b.setFont(11, true)
		b.textLine(fmt.Sprintf("%d. %s", i+1, nonEmpty(c.Name, "Unknown")))
		b.setFont(10, false)
		phone := nonEmpty(c.Phone, "-")
		b.textLine("Number: " + phone)
		if phone != "-" {
			b.textLine("Call: tel:" + normalizePhoneForTel(phone))
		}
		b.wrapText("Summary: "+nonEmpty(c.Summary, "-"), 90)
		if c.Intent != "" {
			b.textLine("Intent: " + c.Intent)
		}
		callFlag := "No"
		if c.CallRequested {
			callFlag = "Yes"
		}
		b.textLine("Call requested: " + callFlag)
		if c.Priority != "" {
			b.textLine("Priority: " + c.Priority)
		}
		if c.NextAction != "" {
			b.wrapText("Next action: "+c.NextAction, 90)
		}
		b.blank()
	}

	b.setFont(9, false)
	b.textLine("- End of report -")
	return b.end()
}

func nonEmpty(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func normalizePhoneForTel(phone string) string {
	phone = strings.TrimSpace(phone)
	var b strings.Builder
	for _, r := range phone {
		if (r >= '0' && r <= '9') || r == '+' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type pdfBuilder struct {
	pages        [][]string
	cur          []string
	yLine        int
	linesPerPage int
	fontBold     bool
	fontSize     int
}

func (p *pdfBuilder) begin() {
	p.linesPerPage = 48
	p.fontSize = 10
	p.newPage()
}

func (p *pdfBuilder) newPage() {
	if p.cur != nil {
		p.pages = append(p.pages, p.cur)
	}
	p.cur = make([]string, 0, p.linesPerPage)
	p.yLine = 0
}

func (p *pdfBuilder) ensureSpace(n int) {
	if p.yLine+n > p.linesPerPage {
		p.newPage()
	}
}

func (p *pdfBuilder) setFont(size int, bold bool) {
	p.fontSize = size
	p.fontBold = bold
}

func (p *pdfBuilder) blank() {
	p.textLine("")
}

func (p *pdfBuilder) textLine(s string) {
	p.ensureSpace(1)
	prefix := "N"
	if p.fontBold {
		prefix = "B"
	}
	p.cur = append(p.cur, fmt.Sprintf("%s|%d|%s", prefix, p.fontSize, s))
	p.yLine++
}

func (p *pdfBuilder) wrapText(s string, width int) {
	words := strings.Fields(s)
	if len(words) == 0 {
		p.textLine("")
		return
	}
	var line strings.Builder
	for _, w := range words {
		if line.Len() == 0 {
			line.WriteString(w)
			continue
		}
		if line.Len()+1+len(w) > width {
			p.textLine(line.String())
			line.Reset()
			line.WriteString(w)
			continue
		}
		line.WriteByte(' ')
		line.WriteString(w)
	}
	if line.Len() > 0 {
		p.textLine(line.String())
	}
}

func (p *pdfBuilder) end() ([]byte, error) {
	if p.cur != nil {
		p.pages = append(p.pages, p.cur)
		p.cur = nil
	}
	if len(p.pages) == 0 {
		p.pages = [][]string{{}}
	}

	n := len(p.pages)
	font1ID := 3 + 2*n
	font2ID := 4 + 2*n

	type pageBuilt struct {
		contentBody string
		pageBody    string
	}
	built := make([]pageBuilt, n)
	for i, lines := range p.pages {
		var content bytes.Buffer
		content.WriteString("BT\n")
		y := 800
		for _, raw := range lines {
			parts := strings.SplitN(raw, "|", 3)
			bold, size, text := "N", 10, raw
			if len(parts) == 3 {
				bold = parts[0]
				size = atoiDefault(parts[1], 10)
				text = parts[2]
			}
			font := "/F1"
			if bold == "B" {
				font = "/F2"
			}
			fmt.Fprintf(&content, "1 0 0 1 50 %d Tm\n%s %d Tf\n(%s) Tj\n", y, font, size, escapePDF(text))
			y -= 14
			if y < 50 {
				break
			}
		}
		content.WriteString("ET\n")
		stream := content.String()
		contentObj := 3 + 2*i
		built[i].contentBody = fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)
		built[i].pageBody = fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> >>",
			contentObj, font1ID, font2ID,
		)
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, font2ID+1) // index by object id

	writeObj := func(id int, body string) {
		offsets[id] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}

	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")

	var kids strings.Builder
	kids.WriteString("[")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&kids, " %d 0 R", 4+2*i)
	}
	kids.WriteString(" ]")
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids %s /Count %d >>", kids.String(), n))

	for i := 0; i < n; i++ {
		writeObj(3+2*i, built[i].contentBody)
		writeObj(4+2*i, built[i].pageBody)
	}
	writeObj(font1ID, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	writeObj(font2ID, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>")

	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", font2ID+1)
	buf.WriteString("0000000000 65535 f \n")
	for id := 1; id <= font2ID; id++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", font2ID+1, xrefStart)
	return buf.Bytes(), nil
}

func escapePDF(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r > 126 {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func atoiDefault(s string, def int) int {
	n := 0
	ok := false
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
		ok = true
	}
	if !ok {
		return def
	}
	return n
}
