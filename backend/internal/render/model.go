// Package render turns an assembled test paper into a branded print booklet:
// PDF through the Typst CLI, Word through OOXML written here, and the legacy
// Markdown. All three read one print model, Document, so every format prints
// the same numbering, sections, answer key and DRAFT stamp.
package render

// Brand is what the booklet is branded with.
type Brand struct {
	Name string `json:"name"`
	// Logo is the logo's path inside the render root, for the Typst template.
	Logo string `json:"logo"`
}

// Labels are every fixed string the layout prints, so a bilingual paper can
// carry English and Hindi labels without the template knowing languages.
type Labels struct {
	PaperCode           string `json:"paper_code"`
	Date                string `json:"date"`
	Duration            string `json:"duration"`
	Questions           string `json:"questions"`
	MaxMarks            string `json:"max_marks"`
	Negative            string `json:"negative"`
	CandidateName       string `json:"candidate_name"`
	RollNo              string `json:"roll_no"`
	Signature           string `json:"signature"`
	GeneralInstructions string `json:"general_instructions"`
	DoNotOpen           string `json:"do_not_open"`
	Directions          string `json:"directions"`
	AnswerKey           string `json:"answer_key"`
	Solutions           string `json:"solutions"`
	QuestionPaper       string `json:"question_paper"`
	KeyAndSolutions     string `json:"key_and_solutions"`
	Answer              string `json:"answer"`
	Missing             string `json:"missing"`
	SeeSolution         string `json:"see_solution"`
	ColPart             string `json:"col_part"`
	ColSection          string `json:"col_section"`
	ColQuestions        string `json:"col_questions"`
	ColMarksEach        string `json:"col_marks_each"`
	ColQNos             string `json:"col_q_nos"`
}

// Option is one printed answer choice.
type Option struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Question is one printed question.
type Question struct {
	Number int `json:"number"`
	// Cols is how many columns the options are laid out in: 4, 2 or 1,
	// decided from their length so short options do not waste lines.
	Cols int `json:"cols"`
	// Breakable lets a very long question flow across a page break. Everything
	// else is kept whole, so a question is never separated from its options.
	Breakable bool     `json:"breakable"`
	Stem      string   `json:"stem"`
	MarksNote string   `json:"marks_note"`
	Missing   bool     `json:"missing"`
	Options   []Option `json:"options"`
}

// Passage is shared reading material printed once above its questions.
type Passage struct {
	From      int    `json:"from"`
	To        int    `json:"to"`
	Text      string `json:"text"`
	Breakable bool   `json:"breakable"`
}

// Block is either a single question or a passage with its questions.
type Block struct {
	Kind      string     `json:"kind"` // "question" or "passage"
	Question  *Question  `json:"question,omitempty"`
	Passage   *Passage   `json:"passage,omitempty"`
	Questions []Question `json:"questions"`
}

// Section is one part of the paper.
type Section struct {
	Label    string  `json:"label"`
	Name     string  `json:"name"`
	From     int     `json:"from"`
	To       int     `json:"to"`
	Count    int     `json:"count"`
	Marks    string  `json:"marks"`
	Negative string  `json:"negative"`
	Note     string  `json:"note"`
	Blocks   []Block `json:"blocks"`
}

// Answer is one cell of the answer-key grid.
type Answer struct {
	N int    `json:"n"`
	A string `json:"a"`
}

// KeySection is the answer-key grid for one section.
type KeySection struct {
	Section string   `json:"section"`
	Cols    int      `json:"cols"`
	Answers []Answer `json:"answers"`
}

// Solution is a worked answer.
type Solution struct {
	N    int    `json:"n"`
	A    string `json:"a"`
	Text string `json:"text"`
}

// Part selects what a booklet contains.
type Part string

const (
	PartPaper Part = "paper" // the question paper only
	PartKey   Part = "key"   // the answer key and solutions only
	PartBoth  Part = "both"  // the paper, then the key from a new page
)

// ParsePart reads a part name, reporting whether it was valid.
func ParsePart(s string) (Part, bool) {
	switch Part(s) {
	case PartPaper, PartKey, PartBoth:
		return Part(s), true
	}
	return "", false
}

// Document is the print model. The JSON tags are the keys the Typst template
// reads; every slice is non-nil so the template never sees null.
type Document struct {
	Brand         Brand        `json:"brand"`
	Accent        string       `json:"accent"`
	Lang          string       `json:"lang"`
	Bilingual     bool         `json:"bilingual"`
	Labels        Labels       `json:"labels"`
	Title         string       `json:"title"`
	RunningTitle  string       `json:"running_title"`
	Exam          string       `json:"exam"`
	Subtitle      string       `json:"subtitle"`
	Code          string       `json:"code"`
	Revision      int          `json:"revision"`
	Date          string       `json:"date"`
	Duration      string       `json:"duration"`
	QuestionCount int          `json:"question_count"`
	MaxMarks      string       `json:"max_marks"`
	Negative      string       `json:"negative"`
	Draft         bool         `json:"draft"`
	DraftReason   string       `json:"draft_reason"`
	Watermark     bool         `json:"watermark"`
	Part          Part         `json:"part"`
	PartLabel     string       `json:"part_label"`
	KeyLabel      string       `json:"key_label"`
	Instructions  []string     `json:"instructions"`
	Sections      []Section    `json:"sections"`
	Key           []KeySection `json:"key"`
	Solutions     []Solution   `json:"solutions"`
}
