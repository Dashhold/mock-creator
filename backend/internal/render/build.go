package render

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mockcreator/internal/models"
)

// BrandName is printed on every booklet.
const BrandName = "Dashhold-EdTech"

// Accent is the booklet's brand colour.
const Accent = "#0B4AA8"

// Options are the choices a caller makes when exporting.
type Options struct {
	Part Part
	// Explanations prints worked solutions after the answer key.
	Explanations bool
	// Draft stamps the booklet as not cleared for delivery, with the reason.
	Draft       bool
	DraftReason string
	// Watermark prints a faint brand watermark on every page.
	Watermark bool
}

// Build turns a paper into the print model. It is pure: it reads only the
// paper, which must have Exam, Items, Items.Question, Items.Question.Options
// and Items.Question.Passage loaded.
func Build(p models.TestPaper, opts Options) Document {
	if _, ok := ParsePart(string(opts.Part)); !ok {
		opts.Part = PartBoth
	}

	exam := models.Exam{}
	if p.Exam != nil {
		exam = *p.Exam
	}
	bilingual := isBilingual(exam.Language)
	l := labelsFor(bilingual)

	items := append([]models.TestPaperItem(nil), p.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SequenceNo != items[j].SequenceNo {
			return items[i].SequenceNo < items[j].SequenceNo
		}
		return items[i].ID < items[j].ID
	})

	doc := Document{
		Brand:         Brand{Name: BrandName, Logo: "logo.png"},
		Accent:        Accent,
		Lang:          docLang(exam.Language),
		Bilingual:     bilingual,
		Labels:        l,
		Title:         clean(p.Title),
		RunningTitle:  truncateRunes(clean(p.Title), 60),
		Exam:          clean(exam.Name),
		Code:          PaperCode(exam.Code, p.ID),
		Revision:      1,
		Date:          paperDate(p.CreatedAt),
		Duration:      durationText(p.DurationMin, bilingual),
		QuestionCount: len(items),
		Draft:         opts.Draft,
		DraftReason:   clean(opts.DraftReason),
		Watermark:     opts.Watermark,
		Part:          opts.Part,
		PartLabel:     l.QuestionPaper,
		KeyLabel:      l.KeyAndSolutions,
		Instructions:  []string{},
		Sections:      []Section{},
		Key:           []KeySection{},
		Solutions:     []Solution{},
	}
	if opts.Part == PartKey {
		doc.Subtitle = pick(bilingual, "Mock Test Series · Answer Key and Solutions", "मॉक टेस्ट सीरीज़ · उत्तर कुंजी एवं हल")
	} else {
		doc.Subtitle = pick(bilingual, "Mock Test Series · Question Booklet", "मॉक टेस्ट सीरीज़ · प्रश्न-पुस्तिका")
	}

	totalMarks := 0.0
	anyNegative := false
	number := 0
	for i := 0; i < len(items); {
		name := strings.TrimSpace(items[i].SectionName)
		j := i
		for j < len(items) && strings.TrimSpace(items[j].SectionName) == name {
			j++
		}
		sec := buildSection(items[i:j], len(doc.Sections), &number, l, bilingual, opts, &doc)
		for _, it := range items[i:j] {
			totalMarks += it.Marks
			if it.NegativeMarks > 0 {
				anyNegative = true
			}
		}
		doc.Sections = append(doc.Sections, sec)
		i = j
	}

	marks := p.TotalMarks
	if marks <= 0 {
		marks = totalMarks
	}
	doc.MaxMarks = num(marks)
	switch {
	case p.NegativeMarks > 0:
		doc.Negative = pick(bilingual, num(p.NegativeMarks)+" per wrong answer", num(p.NegativeMarks)+" प्रति गलत उत्तर")
	case anyNegative:
		doc.Negative = pick(bilingual, "As shown for each part", "प्रत्येक भाग में दर्शाए अनुसार")
	default:
		doc.Negative = pick(bilingual, "None", "नहीं")
	}
	doc.Instructions = instructions(doc, items, p.DurationMin, bilingual)
	return doc
}

func buildSection(items []models.TestPaperItem, index int, number *int, l Labels, bilingual bool, opts Options, doc *Document) Section {
	name := strings.TrimSpace(items[0].SectionName)
	if name == "" {
		name = pick(bilingual, "General", "सामान्य")
	}
	sec := Section{
		Label:  "Part " + partLetter(index),
		Name:   clean(name),
		From:   *number + 1,
		Count:  len(items),
		Blocks: []Block{},
	}

	marks, uniformMarks := uniform(items, func(it models.TestPaperItem) float64 { return it.Marks })
	neg, uniformNeg := uniform(items, func(it models.TestPaperItem) float64 { return it.NegativeMarks })
	sec.Marks = num(marks)
	sec.Negative = num(neg)
	if !uniformMarks {
		sec.Marks = pick(bilingual, "As marked", "अंकित अनुसार")
	}

	key := KeySection{Section: sec.Label + ": " + sec.Name, Answers: []Answer{}}

	for i := 0; i < len(items); {
		it := items[i]
		pid := passageID(it)
		if pid != 0 {
			// A passage set: every consecutive question on the same passage.
			j := i
			for j < len(items) && passageID(items[j]) == pid {
				j++
			}
			block := Block{Kind: "passage", Questions: []Question{}}
			first := *number + 1
			for _, member := range items[i:j] {
				*number++
				q := printQuestion(member, *number, uniformMarks, marks, l, bilingual)
				block.Questions = append(block.Questions, q)
				addAnswer(&key, doc, member, *number, l, opts)
			}
			text := clean(it.Question.Passage.Text)
			block.Passage = &Passage{From: first, To: *number, Text: text, Breakable: utf8.RuneCountInString(text) > 1400}
			sec.Blocks = append(sec.Blocks, block)
			i = j
			continue
		}
		*number++
		q := printQuestion(it, *number, uniformMarks, marks, l, bilingual)
		sec.Blocks = append(sec.Blocks, Block{Kind: "question", Question: &q, Questions: []Question{}})
		addAnswer(&key, doc, it, *number, l, opts)
		i++
	}
	sec.To = *number

	key.Cols = max(1, min(10, len(key.Answers)))
	doc.Key = append(doc.Key, key)

	qRange := fmt.Sprintf("%d–%d", sec.From, sec.To)
	switch {
	case !uniformMarks:
		sec.Note = pick(bilingual,
			"Questions "+qRange+" carry the marks shown against each question.",
			"प्रश्न "+qRange+" के अंक प्रत्येक प्रश्न के साथ दर्शाए गए हैं।")
	case uniformNeg && neg > 0:
		sec.Note = pick(bilingual,
			fmt.Sprintf("Questions %s carry %s marks each; %s marks are deducted for each wrong answer.", qRange, num(marks), num(neg)),
			fmt.Sprintf("प्रश्न %s प्रत्येक %s अंक के हैं; प्रत्येक गलत उत्तर के लिए %s अंक काटे जाएँगे।", qRange, num(marks), num(neg)))
	case uniformNeg:
		sec.Note = pick(bilingual,
			fmt.Sprintf("Questions %s carry %s marks each. There is no negative marking.", qRange, num(marks)),
			fmt.Sprintf("प्रश्न %s प्रत्येक %s अंक के हैं। कोई ऋणात्मक अंकन नहीं है।", qRange, num(marks)))
	default:
		sec.Note = pick(bilingual,
			fmt.Sprintf("Questions %s carry %s marks each; negative marking is shown against each question.", qRange, num(marks)),
			fmt.Sprintf("प्रश्न %s प्रत्येक %s अंक के हैं; ऋणात्मक अंकन प्रत्येक प्रश्न के साथ दर्शाया गया है।", qRange, num(marks)))
	}
	return sec
}

func printQuestion(it models.TestPaperItem, n int, uniformMarks bool, sectionMarks float64, l Labels, bilingual bool) Question {
	q := Question{Number: n, Options: []Option{}, Cols: 1}
	if it.Question == nil {
		q.Missing = true
		q.Stem = l.Missing
		return q
	}
	q.Stem = clean(it.Question.QuestionText)
	if !uniformMarks || it.Marks != sectionMarks {
		note := fmt.Sprintf("[%s marks", num(it.Marks))
		if it.NegativeMarks > 0 {
			note += fmt.Sprintf(", −%s", num(it.NegativeMarks))
		}
		q.MarksNote = note + "]"
	}
	opts := append([]models.QuestionOption(nil), it.Question.Options...)
	sort.SliceStable(opts, func(i, j int) bool {
		if opts[i].OrderIndex != opts[j].OrderIndex {
			return opts[i].OrderIndex < opts[j].OrderIndex
		}
		return opts[i].ID < opts[j].ID
	})
	longest, total := 0, utf8.RuneCountInString(q.Stem)
	for i, o := range opts {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			label = optionLetter(i)
		}
		text := clean(o.Text)
		q.Options = append(q.Options, Option{Label: label, Text: text})
		n := utf8.RuneCountInString(text)
		total += n
		if strings.Contains(text, "\n") {
			n = 999
		}
		if n > longest {
			longest = n
		}
	}
	switch {
	case len(q.Options) == 0:
		q.Cols = 1
	case longest <= 18 && len(q.Options) <= 4:
		q.Cols = len(q.Options)
	case longest <= 40:
		q.Cols = 2
	default:
		q.Cols = 1
	}
	q.Breakable = total > 1100
	return q
}

func addAnswer(key *KeySection, doc *Document, it models.TestPaperItem, n int, l Labels, opts Options) {
	answer := "—"
	answerText, explanation := "", ""
	if it.Question != nil {
		var correct []string
		sorted := append([]models.QuestionOption(nil), it.Question.Options...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OrderIndex < sorted[j].OrderIndex })
		for i, o := range sorted {
			if o.IsCorrect {
				label := strings.TrimSpace(o.Label)
				if label == "" {
					label = optionLetter(i)
				}
				correct = append(correct, label)
			}
		}
		answerText = clean(it.Question.AnswerText)
		explanation = clean(it.Question.Explanation)
		switch {
		case len(correct) > 0:
			answer = strings.Join(correct, ", ")
		case answerText != "":
			answer = l.SeeSolution
		}
	}
	key.Answers = append(key.Answers, Answer{N: n, A: answer})

	// Answers that are not an option label always print in full, otherwise
	// the key would say "see solution" with nothing to see.
	printed := answer
	if printed == l.SeeSolution || printed == "—" {
		printed = ""
	}
	switch {
	case opts.Explanations && explanation != "":
		text := explanation
		if printed == "" && answerText != "" {
			text = answerText + "\n" + explanation
		}
		doc.Solutions = append(doc.Solutions, Solution{N: n, A: printed, Text: text})
	case answerText != "" && printed == "":
		doc.Solutions = append(doc.Solutions, Solution{N: n, A: "", Text: answerText})
	}
}

func instructions(doc Document, items []models.TestPaperItem, durationMin int, bilingual bool) []string {
	var out []string
	parts := len(doc.Sections)
	partWord := "parts"
	if parts == 1 {
		partWord = "part"
	}
	out = append(out, pick(bilingual,
		fmt.Sprintf("This booklet contains %d questions in %d %s. Answer all questions.", doc.QuestionCount, parts, partWord),
		fmt.Sprintf("इस पुस्तिका में %d भागों में %d प्रश्न हैं। सभी प्रश्नों के उत्तर दीजिए।", parts, doc.QuestionCount)))
	if durationMin > 0 {
		out = append(out, pick(bilingual,
			fmt.Sprintf("You have %d minutes to complete the paper.", durationMin),
			fmt.Sprintf("प्रश्न-पत्र हल करने के लिए आपके पास %d मिनट हैं।", durationMin)))
	}
	marks, uniformMarks := uniform(items, func(it models.TestPaperItem) float64 { return it.Marks })
	neg, uniformNeg := uniform(items, func(it models.TestPaperItem) float64 { return it.NegativeMarks })
	switch {
	case len(items) == 0:
	case uniformMarks && uniformNeg && neg > 0:
		out = append(out, pick(bilingual,
			fmt.Sprintf("Each question carries %s marks; %s marks will be deducted for every wrong answer.", num(marks), num(neg)),
			fmt.Sprintf("प्रत्येक प्रश्न %s अंक का है; प्रत्येक गलत उत्तर के लिए %s अंक काटे जाएँगे।", num(marks), num(neg))))
	case uniformMarks && uniformNeg:
		out = append(out, pick(bilingual,
			fmt.Sprintf("Each question carries %s marks. There is no negative marking.", num(marks)),
			fmt.Sprintf("प्रत्येक प्रश्न %s अंक का है। कोई ऋणात्मक अंकन नहीं है।", num(marks))))
	default:
		out = append(out, pick(bilingual,
			"Marks and negative marking for each part are shown in the table below and at the start of each part.",
			"प्रत्येक भाग के अंक तथा ऋणात्मक अंकन नीचे दी गई तालिका और प्रत्येक भाग के आरंभ में दर्शाए गए हैं।"))
	}
	out = append(out,
		pick(bilingual, "Each question has only one correct answer. Mark only one option per question.",
			"प्रत्येक प्रश्न का केवल एक सही उत्तर है। प्रत्येक प्रश्न के लिए केवल एक विकल्प चिह्नित करें।"),
		pick(bilingual, "Read all instructions carefully before you open the booklet.",
			"प्रश्न-पुस्तिका खोलने से पहले सभी निर्देश ध्यान से पढ़ें।"),
		pick(bilingual, "Mobile phones, calculators and other electronic devices are not allowed.",
			"मोबाइल फ़ोन, कैलकुलेटर तथा अन्य इलेक्ट्रॉनिक उपकरण वर्जित हैं।"),
	)
	return out
}

// --- labels -------------------------------------------------------------------

func labelsFor(bilingual bool) Labels {
	return Labels{
		PaperCode:           pick(bilingual, "Paper code", "प्रश्न-पत्र कोड"),
		Date:                pick(bilingual, "Date", "दिनांक"),
		Duration:            pick(bilingual, "Duration", "समय"),
		Questions:           pick(bilingual, "Questions", "प्रश्न"),
		MaxMarks:            pick(bilingual, "Maximum marks", "पूर्णांक"),
		Negative:            pick(bilingual, "Negative marking", "ऋणात्मक अंकन"),
		CandidateName:       pick(bilingual, "Candidate's name", "अभ्यर्थी का नाम"),
		RollNo:              pick(bilingual, "Roll No.", "अनुक्रमांक"),
		Signature:           pick(bilingual, "Signature", "हस्ताक्षर"),
		GeneralInstructions: pick(bilingual, "General Instructions", "सामान्य निर्देश"),
		DoNotOpen:           pick(bilingual, "Do not open this booklet until you are told to do so.", "जब तक कहा न जाए, इस पुस्तिका को न खोलें।"),
		Directions:          pick(bilingual, "Directions", "निर्देश"),
		AnswerKey:           pick(bilingual, "Answer Key", "उत्तर कुंजी"),
		Solutions:           pick(bilingual, "Solutions", "हल"),
		QuestionPaper:       pick(bilingual, "Question paper", "प्रश्न-पत्र"),
		KeyAndSolutions:     pick(bilingual, "Answer key and solutions", "उत्तर कुंजी एवं हल"),
		Answer:              "Answer",
		Missing:             "[This question is no longer in the question bank]",
		SeeSolution:         "See solution",
		ColPart:             "Part",
		ColSection:          "Section",
		ColQuestions:        "Questions",
		ColMarksEach:        "Marks each",
		ColQNos:             "Q. Nos.",
	}
}

// pick returns the English text, or "English / Hindi" for a bilingual paper.
func pick(bilingual bool, en, hi string) string {
	if bilingual {
		return en + " / " + hi
	}
	return en
}

func isBilingual(lang string) bool {
	l := strings.ToLower(lang)
	return strings.Contains(l, "hi") && (strings.Contains(l, "en") || strings.ContainsAny(l, "+,/ "))
}

func docLang(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(l, "hi") {
		return "hi"
	}
	return "en"
}

// --- formatting -------------------------------------------------------------------

var codeChars = regexp.MustCompile(`[^A-Z0-9]+`)

// PaperCode is the printed paper identifier, e.g. DH-SSCCGL-0042.
func PaperCode(examCode string, id uint) string {
	base := codeChars.ReplaceAllString(strings.ToUpper(examCode), "")
	if len(base) > 12 {
		base = base[:12]
	}
	if base == "" {
		base = "MOCK"
	}
	return fmt.Sprintf("DH-%s-%04d", base, id)
}

func paperDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("02 Jan 2006")
}

func durationText(min int, bilingual bool) string {
	if min <= 0 {
		return "—"
	}
	return pick(bilingual, fmt.Sprintf("%d minutes", min), fmt.Sprintf("%d मिनट", min))
}

// num prints a mark without trailing zeros: 2, 0.5, 0.25.
func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func uniform(items []models.TestPaperItem, f func(models.TestPaperItem) float64) (float64, bool) {
	if len(items) == 0 {
		return 0, true
	}
	first := f(items[0])
	for _, it := range items[1:] {
		if f(it) != first {
			return first, false
		}
	}
	return first, true
}

func passageID(it models.TestPaperItem) uint {
	if it.Question == nil || it.Question.PassageID == nil || it.Question.Passage == nil {
		return 0
	}
	if strings.TrimSpace(it.Question.Passage.Text) == "" {
		return 0
	}
	return *it.Question.PassageID
}

func partLetter(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	return strconv.Itoa(i + 1)
}

func optionLetter(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	return strconv.Itoa(i + 1)
}

// clean normalises line endings and drops control characters, which
// extracted text carries and which neither XML nor Typst can print.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || r == 0xFFFE || r == 0xFFFF || r == utf8.RuneError:
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// truncateRunes shortens s to at most n runes. It prefers a word boundary and
// never ends on a combining mark or virama, so an Indic syllable is not cut in
// half (which would print a dangling vowel sign).
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n-1]
	if cut := lastSpace(r); cut > len(r)-20 && cut > 0 {
		r = r[:cut]
	}
	for len(r) > 0 && (unicode.In(r[len(r)-1], unicode.Mn, unicode.Mc) || r[len(r)-1] == '\u094D') {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r)) + "…"
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsSpace(r[i]) {
			return i
		}
	}
	return -1
}
