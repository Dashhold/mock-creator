package extract

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LabelKind is the alphabet a marker counts in.
type LabelKind string

const (
	LabelNumeric    LabelKind = "numeric"     // 1 2 3 4
	LabelLowerAlpha LabelKind = "lower_alpha" // a b c d
	LabelUpperAlpha LabelKind = "upper_alpha" // A B C D
	LabelLowerRoman LabelKind = "lower_roman" // i ii iii iv
	LabelUpperRoman LabelKind = "upper_roman" // I II III IV
)

// WrapKind is the punctuation around a marker.
type WrapKind string

const (
	WrapParen   WrapKind = "paren"   // (a)
	WrapTrail   WrapKind = "trail"   // a)
	WrapDot     WrapKind = "dot"     // a.
	WrapBracket WrapKind = "bracket" // [a]
	WrapColon   WrapKind = "colon"   // a:
	WrapBare    WrapKind = "bare"    // a
)

var romanNumerals = []string{"i", "ii", "iii", "iv", "v", "vi", "vii", "viii", "ix", "x", "xi", "xii"}

// labelIndex converts a marker's text to a zero-based position in its alphabet,
// or -1 when it does not belong.
func labelIndex(kind LabelKind, raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return -1
	}
	switch kind {
	case LabelNumeric:
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return -1
		}
		return n - 1
	case LabelLowerAlpha:
		if len(s) != 1 || s[0] < 'a' || s[0] > 'z' {
			return -1
		}
		return int(s[0] - 'a')
	case LabelUpperAlpha:
		if len(s) != 1 || s[0] < 'A' || s[0] > 'Z' {
			return -1
		}
		return int(s[0] - 'A')
	case LabelLowerRoman, LabelUpperRoman:
		low := strings.ToLower(s)
		for i, r := range romanNumerals {
			if low == r {
				return i
			}
		}
		return -1
	}
	return -1
}

// labelText renders the marker for a zero-based position.
func labelText(kind LabelKind, idx int) string {
	if idx < 0 {
		return ""
	}
	switch kind {
	case LabelNumeric:
		return strconv.Itoa(idx + 1)
	case LabelLowerAlpha:
		if idx > 25 {
			return ""
		}
		return string(rune('a' + idx))
	case LabelUpperAlpha:
		if idx > 25 {
			return ""
		}
		return string(rune('A' + idx))
	case LabelLowerRoman:
		if idx >= len(romanNumerals) {
			return ""
		}
		return romanNumerals[idx]
	case LabelUpperRoman:
		if idx >= len(romanNumerals) {
			return ""
		}
		return strings.ToUpper(romanNumerals[idx])
	}
	return ""
}

// classPattern is the regex fragment matching one marker of a kind.
func classPattern(kind LabelKind) string {
	switch kind {
	case LabelNumeric:
		return `([1-9][0-9]?)`
	case LabelLowerAlpha:
		return `([a-h])`
	case LabelUpperAlpha:
		return `([A-H])`
	case LabelLowerRoman:
		return `(i{1,3}|iv|vi{0,3}|ix|xi{0,2}|v|x)`
	case LabelUpperRoman:
		return `(I{1,3}|IV|VI{0,3}|IX|XI{0,2}|V|X)`
	}
	return ``
}

// wrapPattern wraps a marker fragment in its punctuation.
func wrapPattern(wrap WrapKind, inner string) string {
	switch wrap {
	case WrapParen:
		return `\(\s*` + inner + `\s*\)`
	case WrapTrail:
		return inner + `\s*\)`
	case WrapDot:
		return inner + `\s*\.`
	case WrapBracket:
		return `\[\s*` + inner + `\s*\]`
	case WrapColon:
		return inner + `\s*:`
	case WrapBare:
		return inner
	}
	return inner
}

// OptionStyle describes how one document labels its answer choices.
type OptionStyle struct {
	Kind LabelKind `json:"kind"`
	Wrap WrapKind  `json:"wrap"`

	// anchored matches a marker at the very start of a line, capturing the
	// marker and the rest of the line.
	anchored *regexp.Regexp
	// inline matches a marker anywhere a word boundary allows, used to split
	// options that the converter emitted on a single line.
	inline *regexp.Regexp
}

// String renders the style for diagnostics, e.g. "lower_alpha/paren".
func (s OptionStyle) String() string {
	if s.Kind == "" {
		return "unknown"
	}
	return fmt.Sprintf("%s/%s", s.Kind, s.Wrap)
}

// Numeric reports whether this style counts in digits, which is the case that
// collides with question numbering and needs sequence disambiguation.
func (s OptionStyle) Numeric() bool { return s.Kind == LabelNumeric }

// Example renders what the first two markers look like, for the UI.
func (s OptionStyle) Example() string {
	if s.Kind == "" {
		return ""
	}
	render := func(i int) string {
		l := labelText(s.Kind, i)
		switch s.Wrap {
		case WrapParen:
			return "(" + l + ")"
		case WrapTrail:
			return l + ")"
		case WrapDot:
			return l + "."
		case WrapBracket:
			return "[" + l + "]"
		case WrapColon:
			return l + ":"
		}
		return l
	}
	return render(0) + " " + render(1)
}

func newOptionStyle(kind LabelKind, wrap WrapKind) OptionStyle {
	inner := classPattern(kind)
	marked := wrapPattern(wrap, inner)
	return OptionStyle{
		Kind:     kind,
		Wrap:     wrap,
		anchored: regexp.MustCompile(`^\s*` + marked + `\s*(.*)$`),
		// A marker is only inline if it follows whitespace or a line start and is
		// followed by whitespace, which keeps "e.g." and "No.5" from matching.
		inline: regexp.MustCompile(`(?:^|[\s;,])` + marked + `\s+`),
	}
}

// candidateOptionStyles is the full search space the detector scores. Order
// matters only for tie-breaking: alphabetic styles come first because they are
// unambiguous against question numbering.
func candidateOptionStyles() []OptionStyle {
	kinds := []LabelKind{LabelLowerAlpha, LabelUpperAlpha, LabelLowerRoman, LabelUpperRoman, LabelNumeric}
	wraps := []WrapKind{WrapParen, WrapTrail, WrapDot, WrapBracket}
	out := make([]OptionStyle, 0, len(kinds)*len(wraps))
	for _, k := range kinds {
		for _, w := range wraps {
			out = append(out, newOptionStyle(k, w))
		}
	}
	return out
}

// StyleScore is the evidence gathered for one candidate style.
type StyleScore struct {
	Style OptionStyle
	// Runs is how many sequences of two or more consecutive markers were seen.
	Runs int
	// Markers is the total number of markers inside those runs.
	Markers int
	// ModalLength is the most common run length, which is the document's option
	// count per question.
	ModalLength int
	// InlineRuns counts runs found within a single line.
	InlineRuns int
}

// DetectOptionStyle samples the document and returns the option style with the
// strongest evidence, along with the option count per question.
//
// Evidence is a "run": markers appearing in order from the first label of the
// alphabet, either as consecutive line prefixes or inside one line. A document
// numbering options 1..4 produces runs of 4; stray numbers scattered through
// prose do not, which is what separates real markers from noise.
func DetectOptionStyle(lines []string) (OptionStyle, int, []StyleScore) {
	scores := make([]StyleScore, 0, 20)

	for _, style := range candidateOptionStyles() {
		score := StyleScore{Style: style}
		lengths := map[int]int{}

		// Runs spread over consecutive lines.
		run := 0
		for _, raw := range lines {
			line := stripMarkdown(raw)
			if line == "" {
				continue
			}
			m := style.anchored.FindStringSubmatch(line)
			if m == nil {
				if run >= 2 {
					score.Runs++
					score.Markers += run
					lengths[run]++
				}
				run = 0
				continue
			}
			idx := labelIndex(style.Kind, m[1])
			switch {
			case idx == 0:
				if run >= 2 {
					score.Runs++
					score.Markers += run
					lengths[run]++
				}
				run = 1
			case idx == run && run > 0:
				run++
			default:
				if run >= 2 {
					score.Runs++
					score.Markers += run
					lengths[run]++
				}
				run = 0
			}
		}
		if run >= 2 {
			score.Runs++
			score.Markers += run
			lengths[run]++
		}

		// Runs packed into one line.
		for _, raw := range lines {
			line := stripMarkdown(raw)
			if line == "" {
				continue
			}
			if n := inlineRunLength(style, line); n >= 2 {
				score.InlineRuns++
				score.Runs++
				score.Markers += n
				lengths[n]++
			}
		}

		best, bestCount := 0, 0
		for length, count := range lengths {
			if count > bestCount || (count == bestCount && length > best) {
				best, bestCount = length, count
			}
		}
		score.ModalLength = best

		// How many choices a question really offers is the length of a complete
		// label cycle, not the length of a run that happens to fit on one line. A
		// paper printing four options as two rows of two yields fragment runs of
		// two, and reading that as "two options per question" makes the parser
		// stop at the second choice and hand the rest to the previous option.
		if cycle := modalCycleLength(style, lines); cycle >= 2 && cycle > score.ModalLength {
			score.ModalLength = cycle
		}
		if score.Runs > 0 {
			scores = append(scores, score)
		}
	}

	if len(scores) == 0 {
		return OptionStyle{}, 0, nil
	}

	best := scores[0]
	for _, s := range scores[1:] {
		if betterStyle(s, best) {
			best = s
		}
	}
	count := best.ModalLength
	if count < 2 {
		count = 0
	}
	return best.Style, count, scores
}

// betterStyle decides between two candidates. Marker volume wins first; a
// numeric style must clear a non-numeric one by a wide margin because numeric
// option labels are indistinguishable from question numbers and misreading the
// style corrupts the whole parse.
func betterStyle(candidate, current StyleScore) bool {
	cNum, curNum := candidate.Style.Numeric(), current.Style.Numeric()
	switch {
	case cNum && !curNum:
		return candidate.Markers > current.Markers*2
	case !cNum && curNum:
		return candidate.Markers*2 >= current.Markers
	}
	if candidate.Markers != current.Markers {
		return candidate.Markers > current.Markers
	}
	return candidate.Runs > current.Runs
}

// modalCycleLength returns the commonest number of choices a question offers,
// counted as a complete label cycle across however many lines it spans.
//
// A cycle runs from a "first label" to the next one. Its peak is how many
// choices that question actually printed, regardless of how they were laid out,
// which is the number the parser needs in order to know when a question's
// options are finished.
func modalCycleLength(style OptionStyle, lines []string) int {
	counts := map[int]int{}
	cursor, peak := 0, 0

	closeCycle := func() {
		if peak >= 2 {
			counts[peak]++
		}
		cursor, peak = 0, 0
	}

	for _, raw := range lines {
		line := stripMarkdown(raw)
		if line == "" {
			continue
		}
		for _, idx := range labelSequence(style, line) {
			switch {
			case idx == cursor:
				cursor++
				if cursor > peak {
					peak = cursor
				}
			case idx == 0:
				// A label restarting at the first choice ends the previous cycle.
				closeCycle()
				cursor, peak = 1, 1
			}
			// Anything out of sequence is noise and is ignored, so a stray figure
			// neither extends nor breaks the cycle.
		}
	}
	closeCycle()

	best, bestCount := 0, 0
	for length, count := range counts {
		if count > bestCount || (count == bestCount && length > best) {
			best, bestCount = length, count
		}
	}
	return best
}

// labelSequence returns the option label indexes found on one line, in order.
func labelSequence(style OptionStyle, line string) []int {
	if style.inline != nil {
		matches := style.inline.FindAllStringSubmatchIndex(line, -1)
		if len(matches) > 1 {
			out := make([]int, 0, len(matches))
			for _, m := range matches {
				if idx := labelIndex(style.Kind, line[m[2]:m[3]]); idx >= 0 {
					out = append(out, idx)
				}
			}
			return out
		}
	}
	if style.anchored == nil {
		return nil
	}
	if m := style.anchored.FindStringSubmatch(line); m != nil {
		if idx := labelIndex(style.Kind, m[1]); idx >= 0 {
			return []int{idx}
		}
	}
	return nil
}

// inlineRunLength returns how many sequential markers of a style appear in one
// line starting from the first label, e.g. "(a) red (b) blue (c) green" is 3.
func inlineRunLength(style OptionStyle, line string) int {
	if style.inline == nil {
		return 0
	}
	matches := style.inline.FindAllStringSubmatchIndex(line, -1)
	if len(matches) < 2 {
		return 0
	}
	expected := 0
	best := 0
	for _, m := range matches {
		label := line[m[2]:m[3]]
		idx := labelIndex(style.Kind, label)
		if idx != expected {
			if expected > best {
				best = expected
			}
			if idx == 0 {
				expected = 1
			} else {
				expected = 0
			}
			continue
		}
		expected++
	}
	if expected > best {
		best = expected
	}
	return best
}

// splitInlineOptions breaks a line holding several options into the text before
// the first marker plus one entry per option. It returns nil when the line does
// not carry a usable run.
//
// wantFirst is the label index the caller expects the run to start at, which is
// the next unfilled option of the question in hand. Papers commonly print four
// choices as two rows of two, so the second row starts at label 3, not label 1;
// only accepting runs that start at 1 silently merged that row's two choices
// into one.
//
// A run starting at label 1 is also accepted regardless, because that is how a
// fresh option block begins and how a statement list is followed by the real
// choices.
func splitInlineOptions(
	style OptionStyle, line string, wantFirst int,
) (prefix string, opts []ParsedOption) {
	if style.inline == nil || line == "" {
		return "", nil
	}
	matches := style.inline.FindAllStringSubmatchIndex(line, -1)
	if len(matches) < 2 {
		return "", nil
	}

	type hit struct {
		start, labelStart, labelEnd, end int
		idx                              int
	}

	// Collect every maximal ascending run of labels on the line.
	var runs [][]hit
	var current []hit
	expected := -1
	for _, m := range matches {
		idx := labelIndex(style.Kind, line[m[2]:m[3]])
		if idx < 0 {
			continue
		}
		entry := hit{start: m[0], labelStart: m[2], labelEnd: m[3], end: m[1], idx: idx}
		if len(current) > 0 && idx == expected {
			current = append(current, entry)
			expected++
			continue
		}
		if len(current) > 1 {
			runs = append(runs, current)
		}
		current = []hit{entry}
		expected = idx + 1
	}
	if len(current) > 1 {
		runs = append(runs, current)
	}
	if len(runs) == 0 {
		return "", nil
	}

	// Prefer the run that continues this question's options, then a run that
	// starts a fresh block, then simply the longest. Later runs win ties because
	// the choices are printed after anything they refer to.
	best := -1
	rank := func(run []hit) int {
		switch run[0].idx {
		case wantFirst:
			return 3
		case 0:
			return 2
		default:
			return 1
		}
	}
	for i, run := range runs {
		if best < 0 {
			best = i
			continue
		}
		br, cr := rank(runs[best]), rank(run)
		if cr > br || (cr == br && len(run) >= len(runs[best])) {
			best = i
		}
	}
	seq := runs[best]

	prefix = collapse(line[:seq[0].start])
	for i, h := range seq {
		end := len(line)
		if i+1 < len(seq) {
			end = seq[i+1].start
		}
		opts = append(opts, ParsedOption{
			Label: strings.TrimSpace(line[h.labelStart:h.labelEnd]),
			Text:  collapse(line[h.end:end]),
		})
	}
	return prefix, opts
}

// QuestionStyle describes how a document numbers its questions.
type QuestionStyle struct {
	// Prefix is a literal lead-in such as "Q" or "Question", possibly empty.
	Prefix string   `json:"prefix"`
	Wrap   WrapKind `json:"wrap"`

	pattern *regexp.Regexp
	// explicit is true when the style carries a "Q"-style prefix, which makes
	// question markers unmistakable and switches off sequence guessing.
	explicit bool
}

// Explicit reports whether question markers carry a textual prefix.
func (s QuestionStyle) Explicit() bool { return s.explicit }

// String renders the style for diagnostics.
func (s QuestionStyle) String() string {
	if s.pattern == nil {
		return "unknown"
	}
	if s.Prefix == "" {
		return fmt.Sprintf("number/%s", s.Wrap)
	}
	return fmt.Sprintf("%s/%s", s.Prefix, s.Wrap)
}

// Example renders what the first marker looks like.
func (s QuestionStyle) Example() string {
	if s.pattern == nil {
		return ""
	}
	switch s.Wrap {
	case WrapParen:
		return s.Prefix + "(1)"
	case WrapBracket:
		return s.Prefix + "[1]"
	case WrapTrail:
		return s.Prefix + "1)"
	case WrapColon:
		return s.Prefix + "1:"
	case WrapDot:
		if s.Prefix != "" {
			return s.Prefix + ".1"
		}
		return "1."
	}
	return s.Prefix + "1"
}

// match extracts the question number and the remainder of a line.
func (s QuestionStyle) match(line string) (int, string, bool) {
	if s.pattern == nil {
		return 0, "", false
	}
	m := s.pattern.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 0, "", false
	}
	return n, strings.TrimSpace(m[2]), true
}

func newQuestionStyle(prefix string, wrap WrapKind) QuestionStyle {
	lead := ``
	explicit := false
	if prefix != "" {
		// Accept "Q1", "Q.1", "Q 1", "Q-1" and the spelled-out form.
		lead = `(?i:` + regexp.QuoteMeta(prefix) + `)\s*\.?\s*`
		explicit = true
	}
	num := `([0-9]{1,4})`
	var marked string
	switch wrap {
	case WrapParen:
		marked = `\(\s*` + num + `\s*\)`
	case WrapBracket:
		marked = `\[\s*` + num + `\s*\]`
	case WrapTrail:
		marked = num + `\s*\)`
	case WrapDot:
		marked = num + `\s*\.`
	case WrapColon:
		marked = num + `\s*[:\-–—]`
	default:
		marked = num
	}
	// The trailing group captures the rest of the line, which may be empty when
	// the stem starts on the following line.
	return QuestionStyle{
		Prefix:   prefix,
		Wrap:     wrap,
		explicit: explicit,
		pattern:  regexp.MustCompile(`^\s*` + lead + marked + `\s*(.*)$`),
	}
}

// candidateQuestionStyles lists every numbering convention the detector tries.
func candidateQuestionStyles() []QuestionStyle {
	out := []QuestionStyle{}
	for _, prefix := range []string{"Question", "Ques", "Q", ""} {
		wraps := []WrapKind{WrapDot, WrapTrail, WrapParen, WrapBracket, WrapColon}
		if prefix != "" {
			// A prefixed marker may carry no punctuation at all: "Q1 What is..."
			wraps = append(wraps, WrapBare)
		}
		for _, w := range wraps {
			out = append(out, newQuestionStyle(prefix, w))
		}
	}
	return out
}

// DetectQuestionStyle picks the numbering convention that yields the longest
// ascending run of question numbers.
//
// The detector cannot simply skip lines the option style also matches, because
// in the common case where options are numbered 1..4 a question marker and an
// option marker are textually identical. It instead replays the same
// question/option state machine the parser uses: an ascending number opens a
// question, and the digits that follow it fill that question's options until the
// option count is reached. Whichever candidate convention produces the longest
// run of ascending question numbers under those rules wins.
// It also reports the number the winning run started at. A document does not
// have to begin at question 1: a single shift, or one section split out of a
// paper, legitimately starts at 51 or 76. Deriving the start from the document
// rather than capping it at an arbitrary value is what lets those parse at all.
func DetectQuestionStyle(lines []string, opt OptionStyle, optionCount int) (QuestionStyle, int, int) {
	var (
		best      QuestionStyle
		bestScore int
		bestFirst int
	)

	for _, style := range candidateQuestionStyles() {
		var (
			lastQ     int
			firstQ    int
			optIdx    = -1
			ascending int
			seen      int
		)

		for _, raw := range lines {
			line := stripMarkdown(raw)
			if line == "" {
				continue
			}

			// Option markers in a different alphabet than question numbers are
			// unambiguous, so consume them to keep the option cursor in step.
			if opt.anchored != nil && !opt.Numeric() {
				if m := opt.anchored.FindStringSubmatch(line); m != nil {
					if idx := labelIndex(opt.Kind, m[1]); idx >= 0 {
						if idx == 0 || idx == optIdx+1 {
							optIdx = idx
						}
						continue
					}
				}
			}

			n, _, hit := style.match(line)
			if !hit {
				continue
			}
			seen++

			// Digit-labelled options look exactly like question numbers. Inside an
			// open question, the next digit in sequence belongs to the options.
			if opt.Numeric() && optionCount > 0 && lastQ > 0 {
				want := optIdx + 1
				if want < optionCount && n-1 == want {
					optIdx = want
					continue
				}
			}

			switch {
			case lastQ == 0 && n >= 1 && n <= maxQuestionNumber:
				// The first marker under this style opens the run, wherever it
				// starts.
				ascending++
				firstQ, lastQ = n, n
				optIdx = -1
			case n == lastQ+1:
				ascending++
				lastQ = n
				optIdx = -1
			case ascending <= 1 && n == 1 && lastQ != 1:
				// A "1." turning up after a run of one means the first marker was a
				// stray figure in prose and the real numbering starts here. Only 1
				// restarts a run: any other number is far more likely to be an
				// option marker or a figure inside a stem.
				firstQ, lastQ = 1, 1
				optIdx = -1
			}
		}

		// Ascending hits are the real signal; raw hits only break ties.
		score := ascending*10 + seen
		if style.explicit && ascending > 0 {
			// A "Q"-prefixed marker cannot be confused with anything else, so it
			// wins whenever it has any ascending evidence at all.
			score += 500
		}
		if score > bestScore {
			best, bestScore, bestFirst = style, score, firstQ
		}
	}

	if bestScore == 0 {
		return QuestionStyle{}, 0, 0
	}
	return best, bestScore, bestFirst
}

// maxQuestionNumber bounds what can be read as a question number, so a year or a
// page reference cannot open a run.
const maxQuestionNumber = 2000
