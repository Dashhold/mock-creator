package extract

import (
	"regexp"
	"strconv"
	"strings"
)

// Answer-key grammars. Real keys are printed every imaginable way, so two
// patterns cover the ground: one where the answer is bracketed (unambiguous)
// and one where a separator stands between the question number and a bare
// answer label (ambiguous, so it demands the separator).
var (
	labelClass = `[A-Ha-h]|[1-9]|i{1,3}|iv|vi{0,3}|ix|x|I{1,3}|IV|VI{0,3}|IX|X`

	wrappedPairRe = regexp.MustCompile(`(?i)(?:^|[\s|,;])(?:q\s*\.?\s*)?(\d{1,4})\s*[.):\-–—=]?\s*` +
		`[\(\[]\s*(` + labelClass + `)\s*[\)\]]`)

	barePairRe = regexp.MustCompile(`(?i)(?:^|[\s|,;])(?:q\s*\.?\s*)?(\d{1,4})\s*[.):\-–—=]+\s*` +
		`(` + labelClass + `)(?:[\s.,;|]|$)`)

	// "Option (3) is correct", "Correct answer is B", "Ans. (c)".
	verdictRe = regexp.MustCompile(`(?i)\b(?:ans(?:wer)?|option|choice|alternative|correct\s*(?:answer|option|choice))\b` +
		`[^A-Za-z0-9]{0,12}[\(\[]?\s*(` + labelClass + `)\s*[\)\]]?`)

	// A bare "(c) is correct" with no lead-in word.
	verdictTrailRe = regexp.MustCompile(`(?i)[\(\[]\s*(` + labelClass + `)\s*[\)\]]\s*(?:is|as)\s+(?:the\s+)?correct`)
)

// resolveLabel maps an answer label to a zero-based option index, trying each
// alphabet. maxIndex bounds the result so stray numbers cannot produce an
// option that does not exist.
func resolveLabel(raw string, maxIndex int) (int, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	if maxIndex <= 0 {
		maxIndex = 6
	}

	// Multi-character values can only be roman numerals.
	if len(s) > 1 {
		if idx := labelIndex(LabelLowerRoman, s); idx >= 0 && idx < maxIndex {
			return idx, true
		}
		return 0, false
	}

	if s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx < maxIndex {
			return idx, true
		}
		return 0, false
	}
	// A single "i" or "I" is both a letter and a roman numeral; both resolve to
	// index 0, so there is nothing to disambiguate.
	if s[0] >= 'a' && s[0] <= 'z' {
		idx := int(s[0] - 'a')
		if idx < maxIndex {
			return idx, true
		}
		return 0, false
	}
	if s[0] >= 'A' && s[0] <= 'Z' {
		idx := int(s[0] - 'A')
		if idx < maxIndex {
			return idx, true
		}
		return 0, false
	}
	return 0, false
}

// countAnswerPairs estimates how many question/answer pairs a block of lines
// holds. Used to confirm a heading really introduces an answer key.
func countAnswerPairs(lines []string) int {
	count := 0
	for _, raw := range lines {
		line := stripMarkdown(raw)
		if line == "" {
			continue
		}
		count += len(wrappedPairRe.FindAllStringSubmatchIndex(line, -1))
		count += len(barePairRe.FindAllStringSubmatchIndex(line, -1))
		if isTableRow(raw) {
			count += len(tablePairs(raw, 6))
		}
	}
	return count
}

// tablePairs reads question/answer pairs out of a markdown table row, which is
// how converters render the grid-style keys most papers print.
func tablePairs(raw string, maxIndex int) map[int]int {
	out := map[int]int{}
	cells := tableCells(raw)
	for i := 0; i+1 < len(cells); i++ {
		numCell := strings.Trim(strings.TrimSpace(cells[i]), ".)")
		num, err := strconv.Atoi(numCell)
		if err != nil || num < 1 {
			continue
		}
		labelCell := strings.Trim(strings.TrimSpace(cells[i+1]), "().[]")
		idx, valid := resolveLabel(labelCell, maxIndex)
		if !valid {
			continue
		}
		out[num] = idx
		// Skip the label cell so "1 | A | 2 | B" is read as two pairs.
		i++
	}
	return out
}

// AnswerKey maps question numbers to zero-based correct option indexes.
type AnswerKey map[int]int

// parseAnswerKey reads a key region. Bracketed pairs are trusted over bare
// ones, and the first value for a question wins so a key repeated in a summary
// table cannot overwrite the detailed listing.
func parseAnswerKey(lines []string, maxIndex int) AnswerKey {
	key := AnswerKey{}
	set := func(num, idx int) {
		if num < 1 {
			return
		}
		if _, exists := key[num]; !exists {
			key[num] = idx
		}
	}

	// Pass one: tables and bracketed pairs, which cannot be misread.
	for _, raw := range lines {
		if isTableRow(raw) && !isTableDivider(raw) {
			for num, idx := range tablePairs(raw, maxIndex) {
				set(num, idx)
			}
		}
		line := stripMarkdown(raw)
		if line == "" {
			continue
		}
		for _, m := range wrappedPairRe.FindAllStringSubmatch(line, -1) {
			num, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if idx, valid := resolveLabel(m[2], maxIndex); valid {
				set(num, idx)
			}
		}
	}

	// Pass two: bare pairs fill gaps the bracketed pass left.
	for _, raw := range lines {
		line := stripMarkdown(raw)
		if line == "" || isTableRow(raw) {
			continue
		}
		for _, m := range barePairRe.FindAllStringSubmatch(line, -1) {
			num, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if idx, valid := resolveLabel(m[2], maxIndex); valid {
				set(num, idx)
			}
		}
	}

	return key
}

// explanationBlock is a worked solution recovered from the back of a document.
type explanationBlock struct {
	number int
	text   string
	// answer is the option the solution says is correct, -1 when it does not say.
	answer int
}

// parseExplanations splits a solutions region into per-question blocks.
//
// Blocks are delimited by question numbers, which must ascend. A number that
// jumps backwards is treated as ordinary text inside the current explanation,
// which keeps arithmetic like "= 24. 5 times" from starting a new block.
func parseExplanations(lines []string, style QuestionStyle, maxIndex int) []explanationBlock {
	if len(lines) == 0 {
		return nil
	}

	// Inside a solutions region the numbering is usually plain "12." even when
	// the question paper used "Q12.", so try the document's style first and fall
	// back to a plain numeric marker.
	fallback := newQuestionStyle("", WrapDot)
	styles := []QuestionStyle{}
	if style.pattern != nil {
		styles = append(styles, style)
	}
	styles = append(styles, fallback, newQuestionStyle("", WrapTrail), newQuestionStyle("Q", WrapDot))

	var (
		blocks  []explanationBlock
		current *explanationBlock
		buf     strings.Builder
		last    int
	)

	flush := func() {
		if current == nil {
			return
		}
		current.text = collapse(buf.String())
		buf.Reset()
		if current.text != "" || current.answer >= 0 {
			blocks = append(blocks, *current)
		}
		current = nil
	}

	for _, raw := range lines {
		line := stripMarkdown(raw)
		if line == "" {
			continue
		}
		// Skip the region's own heading lines.
		if answerHeadingRe.MatchString(line) {
			continue
		}

		matchedNum, rest := 0, ""
		for _, s := range styles {
			if n, r, hit := s.match(line); hit {
				matchedNum, rest = n, r
				break
			}
		}

		// Only a forward step in numbering opens a new block.
		if matchedNum > 0 && matchedNum > last && matchedNum <= last+60 {
			flush()
			last = matchedNum
			current = &explanationBlock{number: matchedNum, answer: -1}
			line = rest
		}

		if current == nil {
			continue
		}
		if current.answer < 0 {
			if idx, found := findVerdict(line, maxIndex); found {
				current.answer = idx
			}
		}
		if line != "" {
			if buf.Len() > 0 {
				buf.WriteByte(' ')
			}
			buf.WriteString(line)
		}
	}
	flush()

	return blocks
}

// findVerdict pulls the correct option out of prose such as "Option (3) is
// correct" or "Hence the answer is B".
func findVerdict(line string, maxIndex int) (int, bool) {
	if m := verdictTrailRe.FindStringSubmatch(line); m != nil {
		if idx, valid := resolveLabel(m[1], maxIndex); valid {
			return idx, true
		}
	}
	if m := verdictRe.FindStringSubmatch(line); m != nil {
		if idx, valid := resolveLabel(m[1], maxIndex); valid {
			return idx, true
		}
	}
	return 0, false
}
