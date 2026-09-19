package extract

import (
	"regexp"
	"strings"

	"mockcreator/internal/models"
)

// ParsedOption is one answer choice read out of a document.
type ParsedOption struct {
	Label     string `json:"label"`
	Text      string `json:"text"`
	IsCorrect bool   `json:"is_correct"`
}

// Parse issue codes. These describe what the *parser* could not do cleanly, as
// opposed to what is wrong with the content, which is the quality engine's job.
const (
	// IssueNoOptions means no answer choices were found for a question that
	// looks like it should have them.
	IssueNoOptions = "no_options"
	// IssueIncompleteOptions means fewer choices were found than the document's
	// own convention calls for.
	IssueIncompleteOptions = "incomplete_options"
	// IssueStemTooShort means the stem is too short to be a real question.
	IssueStemTooShort = "stem_too_short"
	// IssueOptionOverrun means the last option grew far beyond its siblings,
	// which is what happens when it absorbs text belonging to something else.
	IssueOptionOverrun = "option_overrun"
	// IssueTrailingText means material appeared after a complete option set and
	// was set aside rather than appended to the last choice.
	IssueTrailingText = "trailing_text"
	// IssueOptionsReplaced means a second option row overwrote the first, which
	// happens in statement-list questions where the statements are themselves
	// numbered.
	IssueOptionsReplaced = "options_replaced"
	// IssueContextCapped means a shared preamble with no printed range was cut
	// off at the safety limit instead of running to the end of the document.
	IssueContextCapped = "context_capped"
	// IssueUnreadableSource means the converter could not map some characters,
	// so part of this question is genuinely missing.
	IssueUnreadableSource = "unreadable_source"
)

// ExtractIssue is one thing the parser could not resolve about a question.
type ExtractIssue struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// ParsedQuestion is a single question recovered from a document.
type ParsedQuestion struct {
	Number int `json:"number"`
	PageNo int `json:"page_no"`

	// SectionLabel is the heading this question appeared under, as printed.
	SectionLabel string      `json:"section_label,omitempty"`
	Subject      *SubjectRef `json:"subject,omitempty"`
	Topic        *TopicRef   `json:"topic,omitempty"`

	Text string `json:"text"`
	// Context holds shared material such as a comprehension passage or a
	// "Directions for questions 5 to 9" preamble that applies to this question.
	//
	// It is kept separate from Text on purpose. Folding it in would repeat a
	// 2,000-character passage into every question that cites it, which is both
	// wasteful and impossible to review.
	Context string `json:"context,omitempty"`
	// ContextKey groups questions that share one passage, so it can be stored
	// once and referenced.
	ContextKey string `json:"context_key,omitempty"`

	Options     []ParsedOption `json:"options,omitempty"`
	Explanation string         `json:"explanation,omitempty"`
	// AnswerText is the answer for formats with no option list.
	AnswerText string              `json:"answer_text,omitempty"`
	Type       models.QuestionType `json:"type"`

	// Trailing holds text that arrived after a complete option set. It is kept
	// for a reviewer to look at rather than silently discarded or, worse,
	// concatenated onto the last option.
	Trailing string `json:"trailing,omitempty"`

	// Issues records everything the parser could not do cleanly.
	Issues []ExtractIssue `json:"issues,omitempty"`
	// Confidence is 0..1 for this question alone.
	Confidence float64 `json:"confidence"`

	// SourceFirstLine and SourceLastLine locate this question in the converted
	// text, so a reviewer can be shown exactly what was read.
	SourceFirstLine int `json:"source_first_line"`
	SourceLastLine  int `json:"source_last_line"`
}

// AddIssue records a parse problem once per code.
func (q *ParsedQuestion) AddIssue(code, detail string) {
	for _, existing := range q.Issues {
		if existing.Code == code {
			return
		}
	}
	q.Issues = append(q.Issues, ExtractIssue{Code: code, Detail: detail})
}

// HasIssue reports whether a code was recorded.
func (q ParsedQuestion) HasIssue(code string) bool {
	for _, existing := range q.Issues {
		if existing.Code == code {
			return true
		}
	}
	return false
}

// HasAnswer reports whether a correct choice or an answer string is known.
func (q ParsedQuestion) HasAnswer() bool {
	if strings.TrimSpace(q.AnswerText) != "" {
		return true
	}
	for _, o := range q.Options {
		if o.IsCorrect {
			return true
		}
	}
	return false
}

// FilledOptions counts options that actually carry text.
func (q ParsedQuestion) FilledOptions() int {
	n := 0
	for _, o := range q.Options {
		if strings.TrimSpace(o.Text) != "" {
			n++
		}
	}
	return n
}

// OptionTexts returns just the option strings, for hashing.
func (q ParsedQuestion) OptionTexts() []string {
	out := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		out = append(out, o.Text)
	}
	return out
}

// FullText joins shared context and stem, which is what a paper would print.
//
// This is for display and for hashing a question against its passage. It is
// deliberately not what gets stored in the question's own text: see Context.
func (q ParsedQuestion) FullText() string {
	if q.Context == "" {
		return q.Text
	}
	return q.Context + "\n\n" + q.Text
}

// ParsedSection is a run of questions under one heading.
type ParsedSection struct {
	Label         string      `json:"label"`
	Subject       *SubjectRef `json:"subject,omitempty"`
	OrderIndex    int         `json:"order_index"`
	FirstQuestion int         `json:"first_question"`
	LastQuestion  int         `json:"last_question"`
	QuestionCount int         `json:"question_count"`
}

// ParseResult is everything one parse run learned about a document. The
// diagnostic fields are surfaced in the UI so a user can see why a document
// yielded fewer questions than expected instead of guessing.
type ParseResult struct {
	Questions []ParsedQuestion `json:"questions"`
	Sections  []ParsedSection  `json:"sections"`

	QuestionStyle string `json:"question_style"`
	OptionStyle   string `json:"option_style"`
	OptionExample string `json:"option_example,omitempty"`
	OptionCount   int    `json:"option_count"`

	AnswerKeyFound    bool `json:"answer_key_found"`
	ExplanationsFound bool `json:"explanations_found"`
	AnsweredCount     int  `json:"answered_count"`
	DroppedCount      int  `json:"dropped_count"`
	// FlaggedCount is how many questions carry at least one parse issue. These
	// are kept rather than discarded, so that a reviewer sees the gap instead of
	// the document merely appearing to hold fewer questions.
	FlaggedCount int `json:"flagged_count"`
	// Confidence is the mean per-question parse confidence, 0..1.
	Confidence float64 `json:"confidence"`

	PageCount int      `json:"page_count"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Options configures a parse run.
type Options struct {
	// Subjects recognises section headings. May be nil, in which case questions
	// come back with no subject and the caller must assign one.
	Subjects *SubjectMatcher
	// Topics tags questions after extraction. Optional.
	Topics *TopicMatcher

	// MinOptions is the fewest choices a question must have to be kept as an
	// objective question. Below this it is still kept, but typed descriptive.
	MinOptions int
	// MaxOptions bounds how many choices are collected per question.
	MaxOptions int
	// MinQuestionChars drops fragments too short to be real stems.
	MinQuestionChars int
	// ExpectedCount is a hint from the exam's pattern, used only to warn when
	// the yield looks wrong.
	ExpectedCount int

	// firstQuestion is the number the style detector found the document's
	// numbering starting at. Set internally by Parse, not by callers.
	firstQuestion int
}

func (o Options) withDefaults() Options {
	if o.MinOptions <= 0 {
		o.MinOptions = 2
	}
	if o.MaxOptions <= 0 {
		o.MaxOptions = 6
	}
	if o.MinQuestionChars <= 0 {
		o.MinQuestionChars = 8
	}
	return o
}

// Region markers. These are deliberately generic: every exam board words its
// answer section slightly differently, so the detector matches on intent
// ("answers", "solutions", "key") rather than any single phrase.
var (
	answerHeadingRe = regexp.MustCompile(`(?i)^\s*(?:` +
		`answer\s*keys?|answers?|keys?|answer\s*sheet|` +
		`answers?\s*(?:and|with|&)\s*(?:explanations?|solutions?|hints?)|` +
		`solutions?|explanations?|` +
		`hints?\s*(?:and|&)\s*solutions?|` +
		`detailed\s*(?:solutions?|explanations?)|` +
		`explanatory\s*answers?` +
		`)\s*[:.\-–—]?\s*$`)

	explanationHeadingRe = regexp.MustCompile(`(?i)^\s*(?:` +
		`answers?\s*(?:and|with|&)\s*(?:explanations?|solutions?)|` +
		`solutions?|explanations?|hints?\s*(?:and|&)\s*solutions?|` +
		`detailed\s*(?:solutions?|explanations?)|explanatory\s*answers?` +
		`)\s*[:.\-–—]?\s*$`)

	// Inline answer on its own line, immediately after a question.
	inlineAnswerRe = regexp.MustCompile(`(?i)^\s*(?:ans(?:wer)?|correct\s*(?:answer|option)|key)\s*[.:)\-–—]?\s*` +
		`\(?\s*([A-Ha-h]|[1-9]|i{1,3}|iv|vi{0,3}|ix|x)\s*\)?\s*[.:]?\s*$`)

	// Inline explanation lead-in, e.g. "Sol." / "Solution:" / "Explanation -".
	inlineSolutionRe = regexp.MustCompile(`(?i)^\s*(?:sol(?:ution)?|exp(?:lanation|l)?|hint|reason)\s*[.:)\-–—]\s*(.*)$`)

	// A directions block that prints the range it covers, in any of the spellings
	// papers actually use: "Directions (90-95)", "Directions for questions 12 to
	// 16", "Direction: Q. Nos. 5-9", "Instructions for the following Qs 21-25".
	//
	// The middle is deliberately loose rather than a list of accepted phrasings,
	// because requiring the literal word "questions" is what made the common
	// "Directions (90-95)" form invisible, and an invisible directions block gets
	// swallowed by whatever question precedes it.
	directionsRe = regexp.MustCompile(`(?i)^\s*(?:directions?|instructions?)\b` +
		`[^0-9\n]{0,40}?(\d{1,4})\s*(?:to|[-–—])\s*(\d{1,4})`)

	// A bare directions/passage lead-in with no explicit range. "In the following
	// questions…" is included because papers use it constantly as a lead-in, and
	// treating it as directions stops it running on into the previous option.
	bareDirectionsRe = regexp.MustCompile(`(?i)^\s*(?:directions?|instructions?|read\s+the\s+following|` +
		`study\s+the\s+following|consider\s+the\s+following\s+passage|` +
		`in\s+the\s+following\s+(?:questions?|sentences?|passages?))\b`)
)

// regions marks where the question body ends and the answer material begins.
type regions struct {
	questionEnd int // exclusive line index
	keyStart    int // -1 when absent
	explStart   int // -1 when absent
}

// findRegions locates the answer key and explanation blocks.
//
// A heading alone is not enough evidence: the word "Solutions" can appear in a
// question. The detector requires that a candidate heading actually be followed
// by a dense run of answer pairs, and that it sit in the back part of the
// document.
func findRegions(lines []string) regions {
	out := regions{questionEnd: len(lines), keyStart: -1, explStart: -1}
	if len(lines) == 0 {
		return out
	}
	earliest := len(lines) / 5

	type candidate struct {
		index int
		pairs int
		expl  bool
	}
	var candidates []candidate

	for i, raw := range lines {
		line := stripMarkdown(raw)
		if line == "" || !answerHeadingRe.MatchString(line) {
			continue
		}
		if !looksLikeHeading(raw) {
			continue
		}
		window := lines[i:]
		if len(window) > 400 {
			window = window[:400]
		}
		candidates = append(candidates, candidate{
			index: i,
			pairs: countAnswerPairs(window),
			expl:  explanationHeadingRe.MatchString(line),
		})
	}

	// Pick the earliest heading that is both late enough in the document and
	// backed by real answer pairs. Three is enough evidence given that the
	// heading itself already had to read as an answer-section title.
	for _, c := range candidates {
		if c.index < earliest || c.pairs < 3 {
			continue
		}
		out.keyStart = c.index
		break
	}

	// The explanation block is the first explanation-style heading at or after
	// the key. Papers that go straight to worked solutions have no separate key,
	// so fall back to searching the whole document.
	searchFrom := out.keyStart
	if searchFrom < 0 {
		searchFrom = earliest
	}
	for _, c := range candidates {
		if c.index >= searchFrom && c.expl {
			out.explStart = c.index
			break
		}
	}

	// When only worked solutions exist, treat them as the end of the questions.
	switch {
	case out.keyStart >= 0:
		out.questionEnd = out.keyStart
	case out.explStart >= 0:
		out.questionEnd = out.explStart
	}
	return out
}
