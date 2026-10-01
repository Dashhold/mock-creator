package render

import (
	"context"
	"time"
)

// SelfTest renders a one-question bilingual booklet through the PDF renderer,
// so a server can report at boot whether PDF export really works on this host
// (binary present, assets writable, fonts shaping) before anyone exports.
func SelfTest(ctx context.Context) (version string, size int, took time.Duration, err error) {
	r, err := DefaultPDF()
	if err != nil {
		return "", 0, 0, err
	}
	l := labelsFor(true)
	q := Question{Number: 1, Cols: 4, Stem: "2 + 2 = ? / दो और दो का योग क्या है?", Options: []Option{
		{Label: "A", Text: "3"}, {Label: "B", Text: "4"}, {Label: "C", Text: "5"}, {Label: "D", Text: "6"},
	}}
	doc := Document{
		Brand: Brand{Name: BrandName, Logo: "logo.png"}, Accent: Accent, Lang: "en", Bilingual: true, Labels: l,
		Title: "Self-test", RunningTitle: "Self-test", Exam: "Renderer check", Subtitle: "Self-test",
		Code: "DH-SELFTEST-0000", Revision: 1, Date: "—", Duration: "—", QuestionCount: 1, MaxMarks: "1", Negative: "—",
		Part: PartBoth, PartLabel: l.QuestionPaper, KeyLabel: l.KeyAndSolutions,
		Instructions: []string{"Self-test."},
		Sections: []Section{{Label: "Part A", Name: "Check", From: 1, To: 1, Count: 1, Marks: "1", Negative: "0", Note: "Self-test.",
			Blocks: []Block{{Kind: "question", Question: &q, Questions: []Question{}}}}},
		Key:       []KeySection{{Section: "Part A", Cols: 1, Answers: []Answer{{N: 1, A: "B"}}}},
		Solutions: []Solution{},
	}
	start := time.Now()
	pdf, err := r.Render(ctx, doc)
	if err != nil {
		return r.Version(), 0, time.Since(start), err
	}
	return r.Version(), len(pdf), time.Since(start), nil
}
