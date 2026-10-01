package render

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockcreator/internal/models"
)

func ptr[T any](v T) *T { return &v }

// fixturePaper is a small bilingual paper: two sections, a passage set, a
// question with long options, Hindi text and maths symbols.
func fixturePaper() models.TestPaper {
	passage := &models.Passage{Text: "Read the passage and answer the questions.\nभारत एक विशाल देश है जिसकी संस्कृति विविधतापूर्ण है।"}
	passage.ID = 7
	q := func(id uint, text string, correct int, explanation string, opts ...string) *models.Question {
		qq := &models.Question{QuestionText: text, Explanation: explanation, HasAnswer: true}
		qq.ID = id
		for i, o := range opts {
			qq.Options = append(qq.Options, models.QuestionOption{Label: string(rune('A' + i)), Text: o, OrderIndex: i, IsCorrect: i == correct})
		}
		return qq
	}
	pq1 := q(4, "What is the passage about?", 1, "", "Rivers", "India", "Mountains", "Oceans")
	pq1.PassageID, pq1.Passage = ptr(uint(7)), passage
	pq2 := q(5, "भारत की संस्कृति कैसी है?", 2, "", "एकरूप", "सीमित", "विविधतापूर्ण", "नवीन")
	pq2.PassageID, pq2.Passage = ptr(uint(7)), passage

	exam := &models.Exam{Name: "SSC Combined Graduate Level (Tier-I)", Code: "ssc-cgl", Language: "en+hi"}
	p := models.TestPaper{
		Title: "SSC CGL Tier-I Mock Test 01", DurationMin: 60, TotalMarks: 10, NegativeMarks: 0.5, Exam: exam,
		Items: []models.TestPaperItem{
			{SequenceNo: 1, SectionName: "Quantitative Aptitude", Marks: 2, NegativeMarks: 0.5, Question: q(1, "If √x = 12, what is x − 2?", 1, "x = 144, so x − 2 = 142.", "140", "142", "144", "146")},
			{SequenceNo: 2, SectionName: "Quantitative Aptitude", Marks: 2, NegativeMarks: 0.5, Question: q(2, "Which statement about the Constitution of India is correct?", 2, "", "It originally contained 448 articles in 25 parts.", "Dr. Rajendra Prasad chaired its Drafting Committee.", "It was adopted on 26 November 1949 and came into force on 26 January 1950.", "It was drafted by the British Parliament.")},
			{SequenceNo: 3, SectionName: "General Awareness", Marks: 2, NegativeMarks: 0.5, Question: pq1},
			{SequenceNo: 4, SectionName: "General Awareness", Marks: 2, NegativeMarks: 0.5, Question: pq2},
			{SequenceNo: 5, SectionName: "General Awareness", Marks: 2, NegativeMarks: 0.5, Question: nil},
		},
	}
	p.ID = 42
	p.CreatedAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return p
}

func TestBuild(t *testing.T) {
	doc := Build(fixturePaper(), Options{Part: PartBoth, Explanations: true, Draft: true, DraftReason: "not checked yet", Watermark: true})

	if doc.Code != "DH-SSCCGL-0042" {
		t.Errorf("code = %q", doc.Code)
	}
	if !doc.Bilingual || !strings.Contains(doc.Labels.AnswerKey, "उत्तर कुंजी") {
		t.Errorf("expected bilingual labels, got %q", doc.Labels.AnswerKey)
	}
	if doc.QuestionCount != 5 || len(doc.Sections) != 2 {
		t.Fatalf("questions=%d sections=%d", doc.QuestionCount, len(doc.Sections))
	}
	s1, s2 := doc.Sections[0], doc.Sections[1]
	if s1.Label != "Part A" || s1.From != 1 || s1.To != 2 || s2.From != 3 || s2.To != 5 {
		t.Errorf("section ranges: %+v / %+v", s1, s2)
	}
	if s1.Blocks[0].Question.Cols != 4 {
		t.Errorf("short options should use 4 columns, got %d", s1.Blocks[0].Question.Cols)
	}
	if s1.Blocks[1].Question.Cols != 1 {
		t.Errorf("long options should use 1 column, got %d", s1.Blocks[1].Question.Cols)
	}
	if s2.Blocks[0].Kind != "passage" || len(s2.Blocks[0].Questions) != 2 || s2.Blocks[0].Passage.From != 3 || s2.Blocks[0].Passage.To != 4 {
		t.Errorf("passage block: %+v", s2.Blocks[0])
	}
	if !s2.Blocks[1].Question.Missing {
		t.Errorf("deleted question should print as missing")
	}
	if got := doc.Key[0].Answers[0].A; got != "B" {
		t.Errorf("answer 1 = %q", got)
	}
	if got := doc.Key[1].Answers[2].A; got != "—" {
		t.Errorf("missing question answer = %q", got)
	}
	if len(doc.Solutions) != 1 || doc.Solutions[0].N != 1 {
		t.Errorf("solutions: %+v", doc.Solutions)
	}
	if doc.MaxMarks != "10" || !strings.HasPrefix(doc.Negative, "0.5 per wrong answer") {
		t.Errorf("max=%q negative=%q", doc.MaxMarks, doc.Negative)
	}
	if len(doc.Instructions) < 4 {
		t.Errorf("instructions: %v", doc.Instructions)
	}

	// Nil slices become JSON null, which the template cannot iterate.
	raw, _ := json.Marshal(Build(models.TestPaper{Title: "Empty"}, Options{}))
	if bytes.Contains(raw, []byte("null")) {
		t.Errorf("print model must not contain null: %s", raw)
	}
}

func TestTruncateKeepsSyllables(t *testing.T) {
	s := strings.Repeat("क्ष", 40)
	got := truncateRunes(s, 20)
	r := []rune(strings.TrimSuffix(got, "…"))
	if last := r[len(r)-1]; last == '\u094D' {
		t.Errorf("truncation ended on a virama: %q", got)
	}
}

func TestDOCX(t *testing.T) {
	for _, part := range []Part{PartPaper, PartKey, PartBoth} {
		doc := Build(fixturePaper(), Options{Part: part, Explanations: true, Watermark: true})
		file, err := DOCX(doc)
		if err != nil {
			t.Fatalf("%s: %v", part, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
		if err != nil {
			t.Fatalf("%s: not a zip: %v", part, err)
		}
		names := map[string]string{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			names[f.Name] = string(b)
		}
		for _, want := range []string{"[Content_Types].xml", "word/document.xml", "word/styles.xml", "word/footer1.xml", "word/media/logo.png", "word/header2.xml"} {
			if _, ok := names[want]; !ok {
				t.Errorf("%s: missing %s", part, want)
			}
		}
		_, h1 := names["word/header1.xml"]
		_, h3 := names["word/header3.xml"]
		if h1 != (part != PartKey) || h3 != (part != PartPaper) {
			t.Errorf("%s: header1=%v header3=%v", part, h1, h3)
		}
		body := names["word/document.xml"]
		if part != PartKey && !strings.Contains(body, "भारत की संस्कृति") {
			t.Errorf("%s: Hindi text missing from the body", part)
		}
		if part != PartPaper && !strings.Contains(body, "उत्तर कुंजी") {
			t.Errorf("%s: answer key heading missing", part)
		}
		if !strings.Contains(names["word/footer1.xml"], "NUMPAGES") || !strings.Contains(names["word/footer1.xml"], "Dashhold-EdTech") {
			t.Errorf("%s: footer lacks page count or brand", part)
		}
		if got := strings.Count(body, "<w:sectPr>"); (part == PartBoth && got != 2) || (part != PartBoth && got != 1) {
			t.Errorf("%s: %d sections", part, got)
		}
	}
}

func TestAssets(t *testing.T) {
	fonts, err := fontFiles()
	if err != nil || len(fonts) != 21 {
		t.Fatalf("expected 21 fonts, got %d (%v)", len(fonts), err)
	}
	if len(Logo()) < 1000 {
		t.Fatal("logo missing")
	}
	if b, _ := assets.ReadFile("assets/paper.typ"); !bytes.Contains(b, []byte("sys.inputs.data")) {
		t.Fatal("template missing")
	}
}

// TestPDF renders through the real typst binary when one is available
// (TYPST_BIN or typst on PATH). RENDER_SAMPLES_DIR keeps the outputs and PNG
// previews of every page for a visual check.
func TestPDF(t *testing.T) {
	bin := os.Getenv("TYPST_BIN")
	if bin == "" {
		bin = "typst"
	}
	r, err := NewPDFRenderer(bin, 1, 60*time.Second)
	if err != nil {
		t.Skipf("typst not available: %v", err)
	}
	samples := os.Getenv("RENDER_SAMPLES_DIR")
	for _, part := range []Part{PartPaper, PartKey, PartBoth} {
		doc := Build(fixturePaper(), Options{Part: part, Explanations: true, Draft: part == PartKey, DraftReason: "needs a reviewer", Watermark: true})
		pdf, err := r.Render(context.Background(), doc)
		if err != nil {
			t.Fatalf("%s: %v", part, err)
		}
		if len(pdf) < 20000 {
			t.Errorf("%s: suspiciously small PDF (%d bytes)", part, len(pdf))
		}
		if samples == "" {
			continue
		}
		_ = os.MkdirAll(samples, 0o755)
		_ = os.WriteFile(filepath.Join(samples, "sample-"+string(part)+".pdf"), pdf, 0o644)
		if file, err := DOCX(doc); err == nil {
			_ = os.WriteFile(filepath.Join(samples, "sample-"+string(part)+".docx"), file, 0o644)
		}
		previewPNG(t, r, doc, filepath.Join(samples, "sample-"+string(part)+"-p{p}.png"))
	}
}

func previewPNG(t *testing.T, r *PDFRenderer, doc Document, out string) {
	data, _ := json.Marshal(doc)
	job := filepath.Join(r.root, "jobs", "preview")
	_ = os.MkdirAll(job, 0o700)
	defer os.RemoveAll(job)
	_ = os.WriteFile(filepath.Join(job, "data.json"), data, 0o600)
	cmd := r.command(context.Background(), "compile", "--root", r.root, "--font-path", filepath.Join(r.root, "fonts"),
		"--ignore-system-fonts", "--input", "data="+path.Join("jobs", "preview", "data.json"),
		"--format", "png", "--ppi", "60", "paper.typ", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("png preview: %v: %s", err, b)
	}
}
