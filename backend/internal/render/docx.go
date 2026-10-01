package render

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// DOCX writes the document as a Word-native .docx: real styles, numbering,
// tables for options and the key, a branded header with the logo, and
// PAGE / NUMPAGES fields in the footer. Standard library only.
func DOCX(doc Document) ([]byte, error) {
	w := &docxWriter{d: doc, accent: strings.TrimPrefix(doc.Accent, "#")}
	if w.accent == "" {
		w.accent = strings.TrimPrefix(Accent, "#")
	}

	geom := fmt.Sprintf(`<w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="1247" w:right="%d" w:bottom="1020" w:left="%d" w:header="567" w:footer="454" w:gutter="0"/>`, pageW, pageH, marginX, marginX)
	ref := func(kind, typ, id string) string {
		return `<w:` + kind + `Reference w:type="` + typ + `" r:id="` + id + `"/>`
	}
	coverSect := func(defaultHeader string) string {
		return `<w:sectPr>` + ref("header", "default", defaultHeader) + ref("header", "first", "rIdH2") +
			ref("footer", "default", "rIdF1") + ref("footer", "first", "rIdF1") + geom + `<w:titlePg/></w:sectPr>`
	}
	hasH1, hasH3 := doc.Part != PartKey, doc.Part != PartPaper

	var bodySect string
	switch doc.Part {
	case PartPaper:
		w.cover()
		w.questions()
		bodySect = coverSect("rIdH1")
	case PartKey:
		w.key()
		bodySect = coverSect("rIdH3")
	default:
		w.cover()
		w.questions()
		// Section 1 ends in the pPr of the last (empty) gap paragraph, so the
		// key starts on a new page without a blank page in between.
		body := w.body.String()
		w.body.Reset()
		if strings.HasSuffix(body, gapPara) {
			body = strings.TrimSuffix(body, gapPara)
		}
		w.body.WriteString(body + `<w:p><w:pPr><w:pStyle w:val="Gap"/>` + coverSect("rIdH1") + `</w:pPr></w:p>`)
		w.key()
		bodySect = `<w:sectPr>` + ref("header", "default", "rIdH3") + ref("footer", "default", "rIdF1") +
			`<w:type w:val="nextPage"/>` + geom + `</w:sectPr>`
	}

	document := xmlHead + `<w:document ` + nsAll + `><w:body>` + w.body.String() + bodySect + `</w:body></w:document>`

	headerCT := func(n int) string {
		return fmt.Sprintf(`<Override PartName="/word/header%d.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>`, n)
	}
	headerRel := func(n int) string {
		return fmt.Sprintf(`<Relationship Id="rIdH%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header%d.xml"/>`, n, n)
	}
	headerOverrides, headerRels := headerCT(2), headerRel(2)
	if hasH1 {
		headerOverrides, headerRels = headerCT(1)+headerOverrides, headerRel(1)+headerRels
	}
	if hasH3 {
		headerOverrides, headerRels = headerOverrides+headerCT(3), headerRels+headerRel(3)
	}
	logoRels := xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdLogo" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/logo.png"/></Relationships>`

	type zpart struct{ name, body string }
	parts := []zpart{
		{"[Content_Types].xml", xmlHead + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
			`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
			`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>` +
			`<Override PartName="/word/fontTable.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.fontTable+xml"/>` +
			headerOverrides +
			`<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>` +
			`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
			`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`},
		{"_rels/.rels", xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
			`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`},
		{"docProps/core.xml", xmlHead + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
			`<dc:title>` + esc(doc.Title) + `</dc:title><dc:creator>` + esc(doc.Brand.Name) + `</dc:creator><cp:keywords>` + esc(doc.Code) + `</cp:keywords>` +
			`<dcterms:created xsi:type="dcterms:W3CDTF">` + docxTime + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + docxTime + `</dcterms:modified></cp:coreProperties>`},
		{"docProps/app.xml", xmlHead + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>Dashhold-EdTech Mock Creator</Application><Company>` + esc(doc.Brand.Name) + `</Company></Properties>`},
		{"word/document.xml", document},
		{"word/styles.xml", docxStyles(w.accent)},
		{"word/numbering.xml", docxNumbering},
		// No updateFields: it makes Word ask "update fields?" on open; PAGE and
		// NUMPAGES update on their own.
		{"word/settings.xml", xmlHead + `<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:defaultTabStop w:val="720"/><w:characterSpacingControl w:val="doNotCompress"/><w:compat><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat></w:settings>`},
		{"word/fontTable.xml", docxFontTable()},
	}
	if hasH1 {
		parts = append(parts, zpart{"word/header1.xml", w.header("paper")})
	}
	parts = append(parts, zpart{"word/header2.xml", w.header("first")})
	if hasH3 {
		parts = append(parts, zpart{"word/header3.xml", w.header("key")})
	}
	parts = append(parts, zpart{"word/footer1.xml", w.footer()},
		zpart{"word/_rels/document.xml.rels", xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
			`<Relationship Id="rIdNumbering" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` +
			`<Relationship Id="rIdSettings" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>` +
			`<Relationship Id="rIdFonts" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/fontTable" Target="fontTable.xml"/>` +
			headerRels +
			`<Relationship Id="rIdF1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>` +
			`<Relationship Id="rIdLogo" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/logo.png"/></Relationships>`})
	if hasH1 {
		parts = append(parts, zpart{"word/_rels/header1.xml.rels", logoRels})
	}
	if hasH3 {
		parts = append(parts, zpart{"word/_rels/header3.xml.rels", logoRels})
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range parts {
		// A malformed part is how Word ends up offering to "recover" a file.
		if err := wellFormed(p.body); err != nil {
			return nil, fmt.Errorf("docx part %s is not well-formed: %w", p.name, err)
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: modified})
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(fw, p.body); err != nil {
			return nil, err
		}
	}
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "word/media/logo.png", Method: zip.Store, Modified: modified})
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(Logo()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Page geometry in twentieths of a point: A4 with 15 mm side margins.
const (
	pageW     = 11906
	pageH     = 16838
	marginX   = 850
	textWidth = pageW - 2*marginX
	qIndent   = 454 // hanging indent for question numbers, 8 mm
	emuPerMM  = 36000
	latinFont = "Noto Sans"
	indicFont = "Noto Sans Devanagari"
	docxTime  = "2026-01-01T00:00:00Z"
	gapPara   = `<w:p><w:pPr><w:pStyle w:val="Gap"/></w:pPr></w:p>`
)

const nsAll = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
	`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
	`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" ` +
	`xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office" ` +
	`xmlns:w10="urn:schemas-microsoft-com:office:word"`

const xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// esc makes text safe for XML; characters XML 1.0 forbids are dropped.
func esc(s string) string {
	var clean strings.Builder
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF && r != utf8.RuneError) {
			clean.WriteRune(r)
		}
	}
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(clean.String()))
	return out.String()
}

type rp struct {
	bold  bool
	size  int // half-points; 0 inherits
	color string
}

func (p rp) xml() string {
	var b strings.Builder
	if p.bold {
		b.WriteString(`<w:b/><w:bCs/>`)
	}
	if p.color != "" {
		fmt.Fprintf(&b, `<w:color w:val="%s"/>`, p.color)
	}
	if p.size > 0 {
		fmt.Fprintf(&b, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, p.size, p.size)
	}
	if b.Len() == 0 {
		return ""
	}
	return "<w:rPr>" + b.String() + "</w:rPr>"
}

// run renders text, turning newlines into line breaks inside the paragraph.
func run(text string, p rp) string {
	var b strings.Builder
	for i, line := range strings.Split(text, "\n") {
		b.WriteString("<w:r>" + p.xml())
		if i > 0 {
			b.WriteString("<w:br/>")
		}
		fmt.Fprintf(&b, `<w:t xml:space="preserve">%s</w:t></w:r>`, esc(line))
	}
	return b.String()
}

func para(ppr string, runs ...string) string {
	if ppr != "" {
		ppr = "<w:pPr>" + ppr + "</w:pPr>"
	}
	return "<w:p>" + ppr + strings.Join(runs, "") + "</w:p>"
}

func field(instr, placeholder string, p rp) string {
	return `<w:r>` + p.xml() + `<w:fldChar w:fldCharType="begin"/></w:r>` +
		`<w:r>` + p.xml() + `<w:instrText xml:space="preserve"> ` + instr + ` </w:instrText></w:r>` +
		`<w:r>` + p.xml() + `<w:fldChar w:fldCharType="separate"/></w:r>` +
		`<w:r>` + p.xml() + `<w:t>` + placeholder + `</w:t></w:r>` +
		`<w:r>` + p.xml() + `<w:fldChar w:fldCharType="end"/></w:r>`
}

type docxWriter struct {
	d      Document
	accent string
	docPr  int
	body   strings.Builder
}

func (w *docxWriter) nextID() int { w.docPr++; return w.docPr }

func (w *docxWriter) logo(heightMM float64) string {
	h := int(heightMM * emuPerMM)
	wd := h * 552 / 569
	id := w.nextID()
	return fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
		`<wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Dashhold logo %d" descr="Dashhold-EdTech logo"/>`+
		`<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>`+
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
		`<pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="logo.png"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="rIdLogo"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`+
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`, wd, h, id, id, id, wd, h)
}

// tableXML builds a fixed-layout table. keepRows keeps every row with the
// next, so a question's options never stay behind on the previous page.
func tableXML(cols []int, indent int, bordered bool, rows [][]string, keepRows bool, shadeFirstRow string) string {
	var b strings.Builder
	total := 0
	for _, c := range cols {
		total += c
	}
	border := `<w:top w:val="none" w:sz="0" w:space="0" w:color="auto"/><w:left w:val="none" w:sz="0" w:space="0" w:color="auto"/><w:bottom w:val="none" w:sz="0" w:space="0" w:color="auto"/><w:right w:val="none" w:sz="0" w:space="0" w:color="auto"/><w:insideH w:val="none" w:sz="0" w:space="0" w:color="auto"/><w:insideV w:val="none" w:sz="0" w:space="0" w:color="auto"/>`
	if bordered {
		border = `<w:top w:val="single" w:sz="4" w:space="0" w:color="BDBDBD"/><w:left w:val="single" w:sz="4" w:space="0" w:color="BDBDBD"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="BDBDBD"/><w:right w:val="single" w:sz="4" w:space="0" w:color="BDBDBD"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="BDBDBD"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="BDBDBD"/>`
	}
	fmt.Fprintf(&b, `<w:tbl><w:tblPr><w:tblW w:w="%d" w:type="dxa"/><w:tblInd w:w="%d" w:type="dxa"/><w:tblBorders>%s</w:tblBorders><w:tblLayout w:type="fixed"/><w:tblCellMar><w:top w:w="30" w:type="dxa"/><w:left w:w="80" w:type="dxa"/><w:bottom w:w="30" w:type="dxa"/><w:right w:w="80" w:type="dxa"/></w:tblCellMar><w:tblLook w:val="0000" w:firstRow="0" w:lastRow="0" w:firstColumn="0" w:lastColumn="0" w:noHBand="1" w:noVBand="1"/></w:tblPr><w:tblGrid>`, total, indent, border)
	for _, c := range cols {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, c)
	}
	b.WriteString(`</w:tblGrid>`)
	for ri, row := range rows {
		b.WriteString(`<w:tr><w:trPr><w:cantSplit/></w:trPr>`)
		for ci, cell := range row {
			shade := ""
			if ri == 0 && shadeFirstRow != "" {
				shade = `<w:shd w:val="clear" w:color="auto" w:fill="` + shadeFirstRow + `"/>`
			}
			keep := ""
			if keepRows && ri < len(rows)-1 {
				keep = `<w:keepNext/>`
			}
			fmt.Fprintf(&b, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>%s</w:tcPr>`, cols[ci%len(cols)], shade)
			b.WriteString(para(`<w:pStyle w:val="Cell"/>`+keep, cell))
			b.WriteString(`</w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	return b.String()
}

func even(n int) []int {
	n = max(1, n)
	cols := make([]int, n)
	for i := range cols {
		cols[i] = textWidth / n
	}
	return cols
}

func (w *docxWriter) brandBlock(subtitle, examLine string) {
	d := w.d
	w.body.WriteString(para(`<w:pStyle w:val="Brand"/><w:pBdr><w:bottom w:val="single" w:sz="12" w:space="6" w:color="`+w.accent+`"/></w:pBdr>`,
		w.logo(19), run("  ", rp{}), run(d.Brand.Name, rp{bold: true, size: 42, color: w.accent}),
		`<w:r><w:br/></w:r>`, run(subtitle, rp{size: 19, color: "5F5F5F"})))
	w.body.WriteString(para(`<w:pStyle w:val="PaperTitle"/>`, run(d.Title, rp{})))
	if examLine != "" {
		w.body.WriteString(para(`<w:pStyle w:val="PaperSubtitle"/>`, run(examLine, rp{})))
	}
	if d.Draft {
		w.body.WriteString(para(`<w:pStyle w:val="DraftBanner"/>`, run(strings.TrimSpace("DRAFT - NOT CLEARED FOR DELIVERY. "+d.DraftReason), rp{bold: true})))
	}
}

func (w *docxWriter) cover() {
	d, l := w.d, w.d.Labels
	w.brandBlock(d.Subtitle, d.Exam)
	meta := func(label, value string) string {
		return run(strings.ToUpper(label), rp{size: 15, color: "5F5F5F"}) + `<w:r><w:br/></w:r>` + run(value, rp{bold: true})
	}
	w.body.WriteString(tableXML(even(3), 0, true, [][]string{
		{meta(l.PaperCode, d.Code), meta(l.Date, d.Date), meta(l.Duration, d.Duration)},
		{meta(l.Questions, fmt.Sprint(d.QuestionCount)), meta(l.MaxMarks, d.MaxMarks), meta(l.Negative, d.Negative)},
	}, false, ""))
	w.body.WriteString(para(`<w:spacing w:after="0"/>`))
	line := run(strings.Repeat("_", 60), rp{color: "7A7A7A"})
	boxes := run(strings.Repeat("\u2610 ", 10), rp{size: 28, color: "7A7A7A"})
	w.body.WriteString(tableXML([]int{2600, textWidth - 2600}, 0, false, [][]string{
		{run(l.CandidateName+":", rp{}), line},
		{run(l.RollNo+":", rp{}), boxes},
		{run(l.Signature+":", rp{}), line},
	}, false, ""))
	w.body.WriteString(para(`<w:pStyle w:val="Heading2"/>`, run(l.GeneralInstructions, rp{})))
	for _, ins := range d.Instructions {
		w.body.WriteString(para(`<w:pStyle w:val="Instruction"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`, run(ins, rp{})))
	}
	rows := [][]string{{run(l.ColPart, rp{bold: true}), run(l.ColSection, rp{bold: true}), run(l.ColQuestions, rp{bold: true}), run(l.ColMarksEach, rp{bold: true}), run(l.ColQNos, rp{bold: true})}}
	for _, s := range d.Sections {
		rows = append(rows, []string{run(s.Label, rp{}), run(s.Name, rp{}), run(fmt.Sprint(s.Count), rp{}), run(s.Marks, rp{}), run(fmt.Sprintf("%d–%d", s.From, s.To), rp{})})
	}
	w.body.WriteString(para(`<w:spacing w:after="60"/>`))
	w.body.WriteString(tableXML([]int{1000, textWidth - 1000 - 1300 - 1300 - 1100, 1300, 1300, 1100}, 0, true, rows, false, "E3EAF6"))
	w.body.WriteString(para(`<w:spacing w:before="480"/><w:jc w:val="center"/>`, run(l.DoNotOpen, rp{bold: true, size: 18, color: "5F5F5F"})))
	w.body.WriteString(para("", `<w:r><w:br w:type="page"/></w:r>`))
}

func (w *docxWriter) question(q Question) {
	stem := run(q.Stem, rp{})
	if q.Missing {
		stem = run(q.Stem, rp{color: "5F5F5F"})
	}
	if q.MarksNote != "" {
		stem += run("  "+q.MarksNote, rp{size: 17, color: "5F5F5F"})
	}
	keep := `<w:keepNext/><w:keepLines/>`
	if q.Breakable {
		keep = ``
	}
	w.body.WriteString(para(`<w:pStyle w:val="Question"/>`+keep+`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr>`, stem))
	if len(q.Options) > 0 {
		cols := max(1, q.Cols)
		width := (textWidth - qIndent) / cols
		widths := make([]int, cols)
		for i := range widths {
			widths[i] = width
		}
		var rows [][]string
		for i := 0; i < len(q.Options); i += cols {
			var row []string
			for j := 0; j < cols; j++ {
				if i+j < len(q.Options) {
					o := q.Options[i+j]
					row = append(row, run("("+o.Label+")", rp{bold: true})+run("\u00a0"+o.Text, rp{}))
				} else {
					row = append(row, "")
				}
			}
			rows = append(rows, row)
		}
		w.body.WriteString(tableXML(widths, qIndent, false, rows, !q.Breakable, ""))
	}
	w.body.WriteString(gapPara)
}

func (w *docxWriter) questions() {
	l := w.d.Labels
	for _, s := range w.d.Sections {
		w.body.WriteString(para(`<w:pStyle w:val="SectionBar"/>`,
			run(s.Label+": "+s.Name, rp{bold: true}), `<w:r><w:tab/></w:r>`,
			run(fmt.Sprintf("Q. %d–%d · %d", s.From, s.To, s.Count), rp{size: 17})))
		w.body.WriteString(para(`<w:pStyle w:val="SectionNote"/>`, run(s.Note, rp{})))
		for _, b := range s.Blocks {
			switch {
			case b.Kind == "passage" && b.Passage != nil:
				w.body.WriteString(para(`<w:pStyle w:val="Passage"/>`,
					run(fmt.Sprintf("%s (Q. %d–%d): ", l.Directions, b.Passage.From, b.Passage.To), rp{bold: true}), run(b.Passage.Text, rp{})))
				for _, q := range b.Questions {
					w.question(q)
				}
			case b.Question != nil:
				w.question(*b.Question)
			}
		}
	}
}

func (w *docxWriter) key() {
	d, l := w.d, w.d.Labels
	// part=both: the section break already starts the key on a new page.
	if d.Part != PartBoth {
		examLine := d.Exam
		if examLine != "" {
			examLine += "  ·  "
		}
		w.brandBlock(d.Subtitle, examLine+d.KeyLabel)
	}
	w.body.WriteString(para(`<w:pStyle w:val="Heading1"/>`, run(l.AnswerKey, rp{})))
	for _, k := range d.Key {
		if len(k.Answers) == 0 {
			continue
		}
		w.body.WriteString(para(`<w:pStyle w:val="KeySection"/>`, run(k.Section, rp{bold: true})))
		cols := k.Cols
		if cols < 1 {
			cols = max(1, min(10, len(k.Answers)))
		}
		var rows [][]string
		for i := 0; i < len(k.Answers); i += cols {
			var row []string
			for j := 0; j < cols; j++ {
				if i+j < len(k.Answers) {
					a := k.Answers[i+j]
					row = append(row, run(fmt.Sprint(a.N), rp{color: "5F5F5F"})+run("\u00a0\u00a0"+a.A, rp{bold: true}))
				} else {
					row = append(row, "")
				}
			}
			rows = append(rows, row)
		}
		w.body.WriteString(tableXML(even(cols), 0, true, rows, false, ""))
	}
	if len(d.Solutions) > 0 {
		w.body.WriteString(para(`<w:pStyle w:val="Heading1"/>`, run(l.Solutions, rp{})))
		for _, s := range d.Solutions {
			lead := fmt.Sprintf("%d. ", s.N)
			if s.A != "" {
				lead += fmt.Sprintf("%s: (%s)  ", l.Answer, s.A)
			}
			w.body.WriteString(para(`<w:pStyle w:val="Solution"/>`, run(lead, rp{bold: true}), run(s.Text, rp{})))
		}
	}
}

// watermark is Word's own text watermark: a VML text path behind the page,
// placed in the header so it repeats on every page.
func watermark(text, color, opacity string) string {
	return `<w:r><w:pict><v:shapetype id="_x0000_t136" coordsize="21600,21600" o:spt="136" adj="10800" path="m@7,l@8,m@5,21600l@6,21600e">` +
		`<v:formulas><v:f eqn="sum #0 0 10800"/><v:f eqn="prod #0 2 1"/><v:f eqn="sum 21600 0 @1"/><v:f eqn="sum 0 0 @2"/><v:f eqn="sum 21600 0 @3"/><v:f eqn="if @0 @3 0"/><v:f eqn="if @0 21600 @1"/><v:f eqn="if @0 0 @2"/><v:f eqn="if @0 @4 21600"/><v:f eqn="mid @5 @6"/><v:f eqn="mid @8 @5"/><v:f eqn="mid @7 @8"/><v:f eqn="mid @6 @7"/><v:f eqn="sum @6 0 @5"/></v:formulas>` +
		`<v:path textpathok="t" o:connecttype="custom" o:connectlocs="@9,0;@10,10800;@11,21600;@12,10800" o:connectangles="270,180,90,0"/>` +
		`<v:textpath on="t" fitshape="t"/><v:handles><v:h position="#0,bottomRight" xrange="6629,14971"/></v:handles><o:lock v:ext="edit" text="t" shapetype="t"/></v:shapetype>` +
		`<v:shape id="PowerPlusWaterMarkObject" o:spid="_x0000_s2049" type="#_x0000_t136" style="position:absolute;margin-left:0;margin-top:0;width:430pt;height:110pt;rotation:315;z-index:-251654144;mso-position-horizontal:center;mso-position-horizontal-relative:margin;mso-position-vertical:center;mso-position-vertical-relative:margin" o:allowincell="f" fillcolor="#` + color + `" stroked="f">` +
		`<v:fill opacity="` + opacity + `"/><v:textpath style="font-family:&quot;Noto Sans&quot;;font-size:1pt" string="` + esc(text) + `"/></v:shape></w:pict></w:r>`
}

// header builds header1 (question pages), header2 (cover: watermark only) or
// header3 (key pages).
func (w *docxWriter) header(kind string) string {
	var mark string
	if w.d.Draft {
		mark = watermark("DRAFT", "C62828", ".22")
	} else if w.d.Watermark {
		mark = watermark(w.d.Brand.Name, w.accent, ".07")
	}
	if kind == "first" {
		return xmlHead + `<w:hdr ` + nsAll + `>` + para(`<w:pStyle w:val="Header"/>`, mark) + `</w:hdr>`
	}
	title := w.d.RunningTitle
	if title == "" {
		title = w.d.Title
	}
	part := w.d.PartLabel
	if kind == "key" {
		part = w.d.KeyLabel
	}
	label := title + "  ·  " + part
	return xmlHead + `<w:hdr ` + nsAll + `>` + para(`<w:pStyle w:val="Header"/><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="4" w:color="`+w.accent+`"/></w:pBdr><w:tabs><w:tab w:val="right" w:pos="`+fmt.Sprint(textWidth)+`"/></w:tabs>`,
		mark, w.logo(6.5), run(" "+w.d.Brand.Name, rp{bold: true, color: w.accent}), `<w:r><w:tab/></w:r>`, run(label, rp{color: "5F5F5F", size: 17})) + `</w:hdr>`
}

func (w *docxWriter) footer() string {
	small := rp{size: 15, color: "5F5F5F"}
	return xmlHead + `<w:ftr ` + nsAll + `>` + para(`<w:pStyle w:val="Footer"/><w:pBdr><w:top w:val="single" w:sz="4" w:space="4" w:color="BDBDBD"/></w:pBdr><w:tabs><w:tab w:val="center" w:pos="`+fmt.Sprint(textWidth/2)+`"/><w:tab w:val="right" w:pos="`+fmt.Sprint(textWidth)+`"/></w:tabs>`,
		run(w.d.Labels.PaperCode+": "+w.d.Code, small), `<w:r><w:tab/></w:r>`, run("Page ", small), field("PAGE", "1", small), run(" of ", small), field("NUMPAGES", "1", small),
		`<w:r><w:tab/></w:r>`, run("© "+w.d.Brand.Name, small)) + `</w:ftr>`
}

func docxStyles(accent string) string {
	pstyle := func(id, name, ppr, rpr string) string {
		return `<w:style w:type="paragraph" w:customStyle="1" w:styleId="` + id + `"><w:name w:val="` + name + `"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr>` + ppr + `</w:pPr><w:rPr>` + rpr + `</w:rPr></w:style>`
	}
	return xmlHead + `<w:styles ` + nsAll + `>` +
		`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="` + latinFont + `" w:hAnsi="` + latinFont + `" w:eastAsia="` + latinFont + `" w:cs="` + indicFont + `"/><w:sz w:val="20"/><w:szCs w:val="20"/><w:lang w:val="en-IN" w:eastAsia="en-IN" w:bidi="hi-IN"/></w:rPr></w:rPrDefault>` +
		`<w:pPrDefault><w:pPr><w:spacing w:after="0" w:line="264" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
		`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:bCs/><w:color w:val="` + accent + `"/><w:sz w:val="28"/><w:szCs w:val="28"/></w:rPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="80"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:bCs/><w:color w:val="` + accent + `"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Header"><w:name w:val="header"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="0"/></w:pPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Footer"><w:name w:val="footer"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="0"/></w:pPr></w:style>` +
		pstyle("Brand", "Brand block", `<w:spacing w:after="160"/>`, ``) +
		pstyle("PaperTitle", "Paper title", `<w:spacing w:after="40"/><w:jc w:val="center"/>`, `<w:b/><w:bCs/><w:sz w:val="30"/><w:szCs w:val="30"/>`) +
		pstyle("PaperSubtitle", "Paper subtitle", `<w:spacing w:after="160"/><w:jc w:val="center"/>`, `<w:color w:val="5F5F5F"/><w:sz w:val="21"/><w:szCs w:val="21"/>`) +
		pstyle("DraftBanner", "Draft banner", `<w:pBdr><w:top w:val="single" w:sz="8" w:space="4" w:color="C62828"/><w:left w:val="single" w:sz="8" w:space="4" w:color="C62828"/><w:bottom w:val="single" w:sz="8" w:space="4" w:color="C62828"/><w:right w:val="single" w:sz="8" w:space="4" w:color="C62828"/></w:pBdr><w:shd w:val="clear" w:color="auto" w:fill="FDECEA"/><w:spacing w:after="160"/>`, `<w:color w:val="B71C1C"/>`) +
		pstyle("Cell", "Table cell", `<w:spacing w:before="20" w:after="20"/>`, ``) +
		pstyle("Instruction", "Instruction", `<w:spacing w:after="60"/>`, ``) +
		pstyle("SectionBar", "Section bar", `<w:keepNext/><w:shd w:val="clear" w:color="auto" w:fill="`+accent+`"/><w:tabs><w:tab w:val="right" w:pos="`+fmt.Sprint(textWidth-100)+`"/></w:tabs><w:spacing w:before="120" w:after="60"/><w:ind w:left="100" w:right="100"/>`, `<w:color w:val="FFFFFF"/><w:sz w:val="21"/><w:szCs w:val="21"/>`) +
		pstyle("SectionNote", "Section note", `<w:keepNext/><w:spacing w:after="140"/>`, `<w:color w:val="5F5F5F"/><w:sz w:val="18"/><w:szCs w:val="18"/>`) +
		pstyle("Question", "Question", `<w:spacing w:before="60" w:after="40"/>`, ``) +
		pstyle("Passage", "Passage", `<w:keepNext/><w:pBdr><w:left w:val="single" w:sz="18" w:space="6" w:color="`+accent+`"/></w:pBdr><w:shd w:val="clear" w:color="auto" w:fill="F5F5F5"/><w:spacing w:before="60" w:after="120"/><w:ind w:left="120"/>`, ``) +
		pstyle("Gap", "Question gap", `<w:spacing w:after="80" w:line="120" w:lineRule="exact"/>`, `<w:sz w:val="8"/><w:szCs w:val="8"/>`) +
		pstyle("KeySection", "Key section", `<w:keepNext/><w:spacing w:before="120" w:after="60"/>`, ``) +
		pstyle("Solution", "Solution", `<w:keepLines/><w:spacing w:after="100"/>`, ``) +
		`</w:styles>`
}

const docxNumbering = xmlHead + `<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="360" w:hanging="360"/></w:pPr></w:lvl></w:abstractNum>` +
	`<w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="454" w:hanging="454"/></w:pPr><w:rPr><w:b/><w:bCs/></w:rPr></w:lvl></w:abstractNum>` +
	`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num></w:numbering>`

func docxFontTable() string {
	return xmlHead + `<w:fonts xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:font w:name="` + latinFont + `"><w:altName w:val="Arial"/><w:charset w:val="00"/><w:family w:val="swiss"/><w:pitch w:val="variable"/></w:font>` +
		`<w:font w:name="` + indicFont + `"><w:altName w:val="Nirmala UI"/><w:charset w:val="00"/><w:family w:val="swiss"/><w:pitch w:val="variable"/></w:font>` +
		`</w:fonts>`
}

// wellFormed walks every token so a broken part is caught here, not by Word.
func wellFormed(body string) error {
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
