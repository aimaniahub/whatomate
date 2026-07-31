package dailyreport

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// A4 page size in PDF points (1 pt = 1/72 inch).
const (
	a4Width  = 595.28
	a4Height = 841.89
	marginL  = 36.0
	marginR  = 36.0
	marginT  = 40.0
	marginB  = 40.0
)

// Column widths for: # | Date | Name | Phone | Summary  (total usable ≈ 523)
var colWidths = []float64{28, 70, 95, 90, 240}

// BuildPDF renders an A4 multi-page daily report with a table layout.
func BuildPDF(data ReportData) ([]byte, error) {
	doc := newA4Doc()
	doc.drawHeader(data)

	if data.EmptyDay || len(data.Chats) == 0 {
		doc.ensureSpace(40)
		doc.setFont(12, true)
		doc.emitText(marginL, doc.y, "No chats today")
		doc.y -= 18
		doc.setFont(10, false)
		doc.wrapAt(marginL, "There were no customer conversations recorded for this day.", a4Width-marginL-marginR)
		if data.Overview.Notes != "" {
			doc.y -= 8
			doc.wrapAt(marginL, data.Overview.Notes, a4Width-marginL-marginR)
		}
		return doc.finish()
	}

	// Overview line
	doc.setFont(9, false)
	doc.y -= 4
	overview := fmt.Sprintf("Total chats: %d", data.Overview.TotalChats)
	if data.AIUsed && data.AIModel != "" {
		overview += "  |  AI: " + data.AIModel
	}
	if data.Overview.Notes != "" {
		overview += "  |  " + data.Overview.Notes
	}
	doc.wrapAt(marginL, overview, a4Width-marginL-marginR)
	doc.y -= 10

	// Table header
	doc.drawTableHeader()

	for _, c := range data.Chats {
		doc.drawTableRow(c)
	}

	doc.y -= 16
	doc.setFont(8, false)
	doc.emitText(marginL, doc.y, "- End of report -")
	return doc.finish()
}

type a4Doc struct {
	pages   [][]string // each page is a list of raw PDF content ops (without BT/ET)
	ops     []string
	y       float64
	fontSz  int
	fontB   bool
}

func newA4Doc() *a4Doc {
	d := &a4Doc{y: a4Height - marginT, fontSz: 10}
	d.newPage()
	return d
}

func (d *a4Doc) newPage() {
	if d.ops != nil {
		d.pages = append(d.pages, d.ops)
	}
	d.ops = make([]string, 0, 64)
	d.y = a4Height - marginT
}

func (d *a4Doc) ensureSpace(need float64) {
	if d.y-need < marginB {
		d.newPage()
		// repeat thin header on continuation pages
		d.setFont(8, false)
		d.emitText(marginL, d.y, "(continued)")
		d.y -= 14
		d.drawTableHeader()
	}
}

func (d *a4Doc) setFont(size int, bold bool) {
	d.fontSz = size
	d.fontB = bold
}

func (d *a4Doc) emitText(x, y float64, s string) {
	fn := "F1"
	if d.fontB {
		fn = "F2"
	}
	d.ops = append(d.ops, fmt.Sprintf("BT /%s %d Tf 1 0 0 1 %.2f %.2f Tm (%s) Tj ET",
		fn, d.fontSz, x, y, escapePDF(s)))
}

func (d *a4Doc) drawHeader(data ReportData) {
	// Company
	d.setFont(16, true)
	company := nonEmpty(data.OrgName, "Company")
	d.emitText(marginL, d.y, company)
	d.y -= 20

	d.setFont(13, true)
	d.emitText(marginL, d.y, "Daily Chat Report")
	d.y -= 16

	d.setFont(10, false)
	rd := data.ReportDate
	if rd == "" {
		rd = "—"
	}
	gen := data.GeneratedAt
	if gen == "" {
		gen = time.Now().Format("2006-01-02 15:04")
	}
	d.emitText(marginL, d.y, fmt.Sprintf("Report date: %s", rd))
	d.y -= 13
	d.emitText(marginL, d.y, fmt.Sprintf("Generated: %s", gen))
	d.y -= 8

	// underline
	d.ops = append(d.ops, fmt.Sprintf("q 0.2 w %.2f %.2f m %.2f %.2f l S Q",
		marginL, d.y, a4Width-marginR, d.y))
	d.y -= 16
}

func (d *a4Doc) drawTableHeader() {
	d.ensureSpace(22)
	headers := []string{"#", "Date", "Name", "Phone", "Summary"}
	x := marginL
	// background bar (light gray via fill rect - optional skip for core PDF)
	d.setFont(9, true)
	for i, h := range headers {
		d.emitText(x+2, d.y, h)
		x += colWidths[i]
	}
	d.y -= 4
	d.ops = append(d.ops, fmt.Sprintf("q 0.6 w %.2f %.2f m %.2f %.2f l S Q",
		marginL, d.y, a4Width-marginR, d.y))
	d.y -= 12
}

func (d *a4Doc) drawTableRow(c ChatSummary) {
	bullets := c.Bullets
	if len(bullets) == 0 && c.Summary != "" {
		bullets = []string{c.Summary}
	}
	if len(bullets) > 3 {
		bullets = bullets[:3]
	}
	if len(bullets) == 0 {
		bullets = []string{"—"}
	}

	// Estimate row height: max(name lines, phone, bullets * 11)
	sumLines := wrapLines(strings.Join(formatBullets(bullets), " "), int(colWidths[4]/5.2))
	nameLines := wrapLines(nonEmpty(c.Name, "—"), int(colWidths[2]/5.2))
	phoneLines := wrapLines(nonEmpty(c.Phone, "—"), int(colWidths[3]/5.2))
	rowH := float64(maxInt(len(sumLines), maxInt(len(nameLines), maxInt(len(phoneLines), 1)))) * 11
	if rowH < 14 {
		rowH = 14
	}
	// Add extra for multi-line bullets as separate lines
	bulletLines := 0
	for _, b := range bullets {
		bulletLines += len(wrapLines("• "+b, int(colWidths[4]/5.2)))
	}
	if float64(bulletLines)*11 > rowH {
		rowH = float64(bulletLines) * 11
	}

	d.ensureSpace(rowH + 8)
	topY := d.y

	// Serial
	d.setFont(9, false)
	d.emitText(marginL+2, topY, fmt.Sprintf("%d", c.Serial))

	// Date
	d.emitText(marginL+colWidths[0]+2, topY, nonEmpty(c.Date, "—"))

	// Name (wrap)
	d.drawWrappedCol(marginL+colWidths[0]+colWidths[1]+2, topY, colWidths[2]-4, nonEmpty(c.Name, "—"))

	// Phone
	d.drawWrappedCol(marginL+colWidths[0]+colWidths[1]+colWidths[2]+2, topY, colWidths[3]-4, nonEmpty(c.Phone, "—"))

	// Summary bullets
	sumX := marginL + colWidths[0] + colWidths[1] + colWidths[2] + colWidths[3] + 2
	cy := topY
	d.setFont(8, false)
	for _, b := range bullets {
		lines := wrapLines("• "+b, int(colWidths[4]/5.0))
		for _, ln := range lines {
			d.emitText(sumX, cy, ln)
			cy -= 11
		}
	}

	// Row bottom = lowest of topY-rowH or cy
	bottom := topY - rowH
	if cy < bottom {
		bottom = cy
	}
	d.y = bottom - 6
	// separator
	d.ops = append(d.ops, fmt.Sprintf("q 0.3 w %.2f %.2f m %.2f %.2f l S Q",
		marginL, d.y+3, a4Width-marginR, d.y+3))
}

func (d *a4Doc) drawWrappedCol(x, topY, width float64, text string) {
	d.setFont(9, false)
	lines := wrapLines(text, int(width/5.2))
	cy := topY
	for _, ln := range lines {
		d.emitText(x, cy, ln)
		cy -= 11
	}
}

func (d *a4Doc) wrapAt(x float64, s string, width float64) {
	d.setFont(d.fontSz, d.fontB)
	lines := wrapLines(s, int(width/5.2))
	for _, ln := range lines {
		d.ensureSpace(14)
		d.emitText(x, d.y, ln)
		d.y -= 13
	}
}

func (d *a4Doc) finish() ([]byte, error) {
	if d.ops != nil {
		d.pages = append(d.pages, d.ops)
		d.ops = nil
	}
	if len(d.pages) == 0 {
		d.pages = [][]string{{}}
	}

	n := len(d.pages)
	font1ID := 3 + 2*n
	font2ID := 4 + 2*n

	type pageBuilt struct {
		contentBody string
		pageBody    string
	}
	built := make([]pageBuilt, n)
	for i, ops := range d.pages {
		var content bytes.Buffer
		for _, op := range ops {
			content.WriteString(op)
			content.WriteByte('\n')
		}
		stream := content.String()
		contentObj := 3 + 2*i
		built[i].contentBody = fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)
		built[i].pageBody = fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> >>",
			a4Width, a4Height, contentObj, font1ID, font2ID,
		)
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, font2ID+1)

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

func formatBullets(bullets []string) []string {
	out := make([]string, len(bullets))
	for i, b := range bullets {
		out[i] = "• " + b
	}
	return out
}

func wrapLines(s string, widthChars int) []string {
	if widthChars < 8 {
		widthChars = 8
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{""}
	}
	words := strings.Fields(s)
	var lines []string
	var line strings.Builder
	for _, w := range words {
		if line.Len() == 0 {
			line.WriteString(w)
			continue
		}
		if line.Len()+1+len(w) > widthChars {
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(w)
			continue
		}
		line.WriteByte(' ')
		line.WriteString(w)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nonEmpty(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
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
