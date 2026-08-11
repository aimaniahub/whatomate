package dailyreport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

// BuildDOCX renders a clean A4 Word document with a proper table.
func BuildDOCX(data ReportData) ([]byte, error) {
	body := buildDocumentXML(data)
	return packDOCX(body)
}

// ValidateDOCXContent opens the DOCX package and ensures document.xml has
// expected report content before we save/send (catches blank scheduled files).
func ValidateDOCXContent(docx []byte, chatCount int) error {
	if len(docx) < 100 || !bytes.HasPrefix(docx, []byte("PK")) {
		return fmt.Errorf("invalid docx package")
	}
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		return fmt.Errorf("open docx zip: %w", err)
	}
	var docXML string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("open document.xml: %w", err)
			}
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(rc); err != nil {
				_ = rc.Close()
				return fmt.Errorf("read document.xml: %w", err)
			}
			_ = rc.Close()
			docXML = buf.String()
			break
		}
	}
	if docXML == "" {
		return fmt.Errorf("document.xml missing")
	}
	if !strings.Contains(docXML, "Daily Chat Report") {
		return fmt.Errorf("document missing report title")
	}
	if chatCount > 0 {
		if strings.Contains(docXML, "No chats today") {
			return fmt.Errorf("document says no chats but %d chats were collected", chatCount)
		}
		if !strings.Contains(docXML, "Total chats:") {
			return fmt.Errorf("document missing total chats line")
		}
		// Table body should have at least one data-ish paragraph beyond headers
		if !strings.Contains(docXML, "<w:tbl>") {
			return fmt.Errorf("document missing chat table")
		}
	}
	return nil
}

func buildDocumentXML(data ReportData) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<w:body>`)

	// Company title
	company := nonEmpty(data.OrgName, "Company")
	b.WriteString(pStyled(company, true, 32, "000000", "center"))
	b.WriteString(pStyled("Daily Chat Report", true, 28, "1F4E79", "center"))
	b.WriteString(pStyled(fmt.Sprintf("Report date: %s", nonEmpty(data.ReportDate, "—")), false, 20, "333333", "center"))
	gen := data.GeneratedAt
	if gen == "" {
		gen = time.Now().Format("2006-01-02 15:04")
	}
	b.WriteString(pStyled(fmt.Sprintf("Generated: %s", gen), false, 18, "666666", "center"))
	b.WriteString(emptyP())

	if data.EmptyDay || len(data.Chats) == 0 {
		b.WriteString(pStyled("No chats today", true, 24, "000000", "left"))
		b.WriteString(pStyled("There were no customer conversations recorded for this day.", false, 20, "333333", "left"))
		if data.Overview.Notes != "" {
			b.WriteString(pStyled(data.Overview.Notes, false, 18, "666666", "left"))
		}
		b.WriteString(sectPr())
		b.WriteString(`</w:body></w:document>`)
		return b.String()
	}

	// Overview — no model names / internal AI labels in the document
	b.WriteString(pStyled(fmt.Sprintf("Total chats: %d", data.Overview.TotalChats), false, 18, "333333", "left"))
	b.WriteString(emptyP())

	// Table: # | Date | Name | Phone | Summary
	// A4 content width ≈ 9026 DXA with 1" margins; use slightly tighter margins via sectPr
	// usable ~ 10080 DXA with 0.75" margins
	const tableW = 10080
	cols := []int{600, 1600, 2000, 2000, 3880} // sum = 10080

	b.WriteString(`<w:tbl>`)
	b.WriteString(`<w:tblPr>`)
	b.WriteString(fmt.Sprintf(`<w:tblW w:w="%d" w:type="dxa"/>`, tableW))
	b.WriteString(`<w:tblBorders>`)
	b.WriteString(`<w:top w:val="single" w:sz="8" w:space="0" w:color="1F4E79"/>`)
	b.WriteString(`<w:left w:val="single" w:sz="8" w:space="0" w:color="1F4E79"/>`)
	b.WriteString(`<w:bottom w:val="single" w:sz="8" w:space="0" w:color="1F4E79"/>`)
	b.WriteString(`<w:right w:val="single" w:sz="8" w:space="0" w:color="1F4E79"/>`)
	b.WriteString(`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>`)
	b.WriteString(`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>`)
	b.WriteString(`</w:tblBorders>`)
	b.WriteString(`<w:tblLayout w:type="fixed"/>`)
	b.WriteString(`</w:tblPr>`)
	b.WriteString(`<w:tblGrid>`)
	for _, c := range cols {
		b.WriteString(fmt.Sprintf(`<w:gridCol w:w="%d"/>`, c))
	}
	b.WriteString(`</w:tblGrid>`)

	// Header row
	headers := []string{"#", "Date", "Name", "Phone", "Summary"}
	b.WriteString(`<w:tr>`)
	for i, h := range headers {
		b.WriteString(tc(cols[i], h, true, "1F4E79", "FFFFFF", false))
	}
	b.WriteString(`</w:tr>`)

	// Data rows — every cell has a non-empty value so Word never shows blank fields.
	for _, chat := range data.Chats {
		summary := formatSummaryCell(chat)
		vals := []string{
			fmt.Sprintf("%d", nonZeroSerial(chat.Serial)),
			nonEmpty(chat.Date, nonEmpty(data.ReportDate, "—")),
			nonEmpty(chat.Name, nonEmpty(chat.Phone, "Unknown")),
			nonEmpty(chat.Phone, "—"),
			nonEmpty(summary, "No message text available"),
		}
		fill := "FFFFFF"
		if chat.Serial%2 == 0 {
			fill = "F2F7FB"
		}
		b.WriteString(`<w:tr>`)
		for i, v := range vals {
			b.WriteString(tc(cols[i], v, false, fill, "000000", i == 4))
		}
		b.WriteString(`</w:tr>`)
	}

	b.WriteString(`</w:tbl>`)
	b.WriteString(emptyP())
	b.WriteString(pStyled("End of report", false, 16, "999999", "center"))
	b.WriteString(sectPr())
	b.WriteString(`</w:body></w:document>`)
	return b.String()
}

func nonZeroSerial(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

// formatSummaryCell shows clean summary bullets only.
// Raw message lines (Excerpts) are used only when there are no bullets —
// i.e. AI failed and we fell back — never both at once.
func formatSummaryCell(c ChatSummary) string {
	parts := make([]string, 0, 4)

	for _, bl := range c.Bullets {
		bl = strings.TrimSpace(bl)
		if bl == "" {
			continue
		}
		// plain dashes for Word (not fancy bullets that break encoding)
		parts = append(parts, "- "+bl)
	}

	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}

	if s := strings.TrimSpace(c.Summary); s != "" && s != "—" {
		return "- " + s
	}

	// Fallback-only: raw transcript snippets (AI failed path)
	if len(c.Excerpts) > 0 {
		raw := make([]string, 0, len(c.Excerpts))
		for _, ex := range c.Excerpts {
			ex = strings.TrimSpace(ex)
			if ex == "" {
				continue
			}
			raw = append(raw, "• "+ex)
		}
		if len(raw) > 0 {
			return strings.Join(raw, "\n")
		}
	}

	return "No message summary available"
}

func tc(width int, text string, bold bool, bg, fg string, multiline bool) string {
	// multiline: split on \n into multiple paragraphs
	var inner strings.Builder
	lines := []string{text}
	if multiline {
		lines = strings.Split(text, "\n")
	}
	for _, line := range lines {
		inner.WriteString(`<w:p>`)
		inner.WriteString(`<w:pPr><w:spacing w:before="40" w:after="40"/><w:jc w:val="left"/></w:pPr>`)
		inner.WriteString(`<w:r>`)
		inner.WriteString(`<w:rPr>`)
		inner.WriteString(`<w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:cs="Arial"/>`)
		if bold {
			inner.WriteString(`<w:b/>`)
		}
		inner.WriteString(`<w:sz w:val="18"/><w:szCs w:val="18"/>`)
		inner.WriteString(fmt.Sprintf(`<w:color w:val="%s"/>`, fg))
		inner.WriteString(`</w:rPr>`)
		inner.WriteString(`<w:t xml:space="preserve">`)
		inner.WriteString(xmlEscape(line))
		inner.WriteString(`</w:t></w:r></w:p>`)
	}
	return fmt.Sprintf(
		`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/><w:shd w:val="clear" w:color="auto" w:fill="%s"/><w:tcMar><w:top w:w="60"/><w:left w:w="80"/><w:bottom w:w="60"/><w:right w:w="80"/></w:tcMar></w:tcPr>%s</w:tc>`,
		width, bg, inner.String(),
	)
}

func pStyled(text string, bold bool, sizeHalfPts int, color, align string) string {
	b := ""
	if bold {
		b = `<w:b/>`
	}
	return fmt.Sprintf(
		`<w:p><w:pPr><w:spacing w:before="60" w:after="60"/><w:jc w:val="%s"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:cs="Arial"/>%s<w:sz w:val="%d"/><w:szCs w:val="%d"/><w:color w:val="%s"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		align, b, sizeHalfPts, sizeHalfPts, color, xmlEscape(text),
	)
}

func emptyP() string {
	return `<w:p><w:pPr><w:spacing w:before="60" w:after="60"/></w:pPr></w:p>`
}

// A4 page, 0.75" margins
func sectPr() string {
	return `<w:sectPr>
<w:pgSz w:w="11906" w:h="16838"/>
<w:pgMar w:top="1080" w:right="1080" w:bottom="1080" w:left="1080" w:header="720" w:footer="720"/>
</w:sectPr>`
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\r':
			// skip
		case '\n':
			// newlines handled by splitting paragraphs for summary; keep space here
			b.WriteByte(' ')
		default:
			if r < 0x20 && r != '\t' {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func packDOCX(documentXML string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`,
		"word/document.xml": documentXML,
	}

	// Write in stable order
	order := []string{"[Content_Types].xml", "_rels/.rels", "word/_rels/document.xml.rels", "word/document.xml"}
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
