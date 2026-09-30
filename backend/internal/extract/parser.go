package extract

import (
	"fmt"
	"sort"
	"strings"

	"mockcreator/internal/models"
)

// bufDest says where accumulated text is going.
type bufDest int

const (
	destStem bufDest = iota
	destOption
	destExplanation
	destContext
	// destTrailing collects text that arrived after a question was structurally
	// complete. It is kept for review rather than appended to the last option,
	// which is where such text used to end up.
	destTrailing
)

// optionOverrunFactor is how far past its siblings an option may grow before the
// extra text is treated as foreign. Real wrapped options stay close in length;
// an option that has absorbed the next question does not.
const optionOverrunFactor = 4

// overrun reports whether adding addition characters to option idx would make it
// implausibly longer than its filled siblings.
func overrun(q *ParsedQuestion, idx, addition int) bool {
	lengths := make([]int, 0, len(q.Options))
	for i, opt := range q.Options {
		if i == idx {
			continue
		}
		if n := len([]rune(strings.TrimSpace(opt.Text))); n > 0 {
			lengths = append(lengths, n)
		}
	}
	// With fewer than three siblings there is no reliable baseline to compare
	// against, so nothing is rejected.
	if len(lengths) < 3 {
		return false
	}
	sort.Ints(lengths)
	median := lengths[len(lengths)/2]
	if median < 4 {
		median = 4
	}
	current := len([]rune(strings.TrimSpace(q.Options[idx].Text)))
	return current+addition > median*optionOverrunFactor
}

// contextRange is shared preamble covering a span of question numbers.
type contextRange struct {
	from int
	to   int // 0 means open-ended until the next context or section begins
	text string
	// explicit records that the document printed the range itself, as in
	// "Directions (90-95)". An explicit range is authoritative; an inferred one
	// is capped, because guessing it too wide contaminates every later question.
	explicit bool
	capped   bool
}

// openContextCap bounds how many questions an unnumbered shared preamble may
// cover when nothing closes it.
//
// This exists because the alternative is unbounded. A single "In the following
// questions..." lead-in that never closes will otherwise attach itself to every
// remaining question in the paper, which is how a reasoning instruction ends up
// glued to the front of a physics question. Papers that really share a preamble
// across a long run print the range, and that path is not capped.
const openContextCap = 12

// Parse turns converted document text into structured questions.
//
// The document is read in four passes: locate the answer material, learn the
// document's marker conventions, walk the question body as a state machine,
// then fold answers and worked solutions back onto the questions they belong
// to. No step consults a list of known exams.
func Parse(markdown string, opts Options) ParseResult {
	opts = opts.withDefaults()
	res := ParseResult{}

	lines, pages := prepareLines(markdown)
	if len(lines) == 0 {
		res.Warnings = append(res.Warnings, "document produced no text; it may be an image-only file that needs OCR")
		return res
	}
	if len(pages) > 0 {
		res.PageCount = pages[len(pages)-1]
	}

	reg := findRegions(lines)
	body := lines[:reg.questionEnd]
	bodyPages := pages[:reg.questionEnd]

	optStyle, optCount, _ := DetectOptionStyle(body)
	qStyle, styleScore, firstNumber := DetectQuestionStyle(body, optStyle, optCount)
	opts.firstQuestion = firstNumber

	res.OptionStyle = optStyle.String()
	res.OptionExample = optStyle.Example()
	res.OptionCount = optCount
	res.QuestionStyle = qStyle.String()

	if styleScore == 0 {
		res.Warnings = append(res.Warnings,
			"could not find a question numbering pattern; the document may not be a question paper, "+
				"or the text came through too poorly to parse (try re-running with OCR forced)")
		return res
	}
	if optCount == 0 {
		res.Warnings = append(res.Warnings,
			"no answer-option pattern detected; questions will be stored without choices")
	}

	questions, sections := parseBody(body, bodyPages, qStyle, optStyle, optCount, opts)

	maxIndex := optCount
	if maxIndex <= 0 {
		maxIndex = opts.MaxOptions
	}

	// Fold in the answer key.
	if reg.keyStart >= 0 {
		end := len(lines)
		if reg.explStart > reg.keyStart {
			end = reg.explStart
		}
		key := parseAnswerKey(lines[reg.keyStart:end], maxIndex)
		if len(key) > 0 {
			res.AnswerKeyFound = true
			applyAnswerKey(questions, key)
		}
	}

	// Fold in worked solutions, which also serve as a second source of answers.
	if reg.explStart >= 0 {
		blocks := parseExplanations(lines[reg.explStart:], qStyle, maxIndex)
		if len(blocks) > 0 {
			res.ExplanationsFound = true
			applyExplanations(questions, blocks)
		}
	}

	// A key printed at the very top is unusual but happens; if nothing was found
	// behind the questions, sweep the whole document for pairs as a last resort.
	if !res.AnswerKeyFound && !res.ExplanationsFound {
		if key := parseAnswerKey(lines, maxIndex); len(key) >= len(questions)/2 && len(key) > 0 {
			res.AnswerKeyFound = true
			applyAnswerKey(questions, key)
		}
	}

	kept, dropped := finalize(questions, optCount, opts)
	res.Questions = kept
	res.DroppedCount = dropped
	res.Sections = summarizeSections(sections, kept)

	for _, q := range kept {
		if q.HasAnswer() {
			res.AnsweredCount++
		}
		if len(q.Issues) > 0 {
			res.FlaggedCount++
		}
	}
	res.Confidence = averageConfidence(kept)

	res.Warnings = append(res.Warnings, buildWarnings(res, opts)...)
	return res
}

// prepareLines splits the document into lines, removes page furniture and
// returns the page number each line sits on.
func prepareLines(markdown string) ([]string, []int) {
	raw := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	raw = dehyphenate(raw)

	lines := make([]string, 0, len(raw))
	pages := make([]int, 0, len(raw))
	page := 1

	for _, line := range raw {
		if m := pageMarkRe.FindStringSubmatch(line); m != nil {
			for _, g := range m[1:] {
				if g == "" {
					continue
				}
				if n := atoiSafe(g); n > 0 {
					page = n
				}
			}
			continue
		}
		if pageBreakRe.MatchString(line) {
			page++
			continue
		}
		if strings.TrimSpace(line) == "" {
			// Keep blank lines: they separate stems from options.
			lines = append(lines, "")
			pages = append(pages, page)
			continue
		}
		lines = append(lines, line)
		pages = append(pages, page)
	}
	return lines, pages
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 100000 {
			return 0
		}
	}
	return n
}

// parseBody walks the question region and assembles questions.
func parseBody(
	lines []string,
	pages []int,
	qStyle QuestionStyle,
	optStyle OptionStyle,
	optCount int,
	opts Options,
) ([]ParsedQuestion, []ParsedSection) {
	var (
		out      []ParsedQuestion
		sections []ParsedSection
		contexts []contextRange

		cur       *ParsedQuestion
		curOptIdx = -1
		dest      = destStem
		buf       strings.Builder

		lastQNum int

		sectionLabel   string
		sectionSubject *SubjectRef

		pendingCtx *contextRange

		// afterBlank tracks whether a paragraph break separates the current line
		// from the previous one. A complete option set followed by a blank line
		// is the end of the question; ignoring that is what let one option
		// swallow the next question and the running page header with it.
		afterBlank bool

		// lastComplete records whether the question most recently filed had a full
		// set of choices, which is the evidence that lets the numbering survive a
		// heading or a directions block in between.
		lastComplete bool
	)

	optTarget := optCount
	if optTarget <= 0 || optTarget > opts.MaxOptions {
		optTarget = opts.MaxOptions
	}

	// commit writes the accumulated text to wherever it belongs.
	commit := func() {
		text := collapse(buf.String())
		buf.Reset()
		if text == "" {
			return
		}
		switch dest {
		case destContext:
			if pendingCtx != nil {
				if pendingCtx.text != "" {
					pendingCtx.text += " "
				}
				pendingCtx.text += text
			}
		case destExplanation:
			if cur != nil {
				if cur.Explanation != "" {
					cur.Explanation += " "
				}
				cur.Explanation += text
			}
		case destTrailing:
			if cur != nil {
				if cur.Trailing != "" {
					cur.Trailing += " "
				}
				cur.Trailing += text
			}
		case destOption:
			if cur == nil || curOptIdx < 0 || curOptIdx >= len(cur.Options) {
				return
			}
			option := &cur.Options[curOptIdx]
			// An option that has already outgrown its siblings by this much is no
			// longer wrapping its own text; it is collecting somebody else's.
			if overrun(cur, curOptIdx, len(text)) {
				cur.AddIssue(IssueOptionOverrun, "text after the last option was set aside")
				if cur.Trailing != "" {
					cur.Trailing += " "
				}
				cur.Trailing += text
				return
			}
			if option.Text != "" {
				option.Text += " "
			}
			option.Text += text
		default:
			if cur != nil {
				if cur.Text != "" {
					cur.Text += " "
				}
				cur.Text += text
			}
		}
	}

	closeQuestion := func() {
		commit()
		if cur != nil {
			// Remember whether the question just filed looked complete. Sequence
			// acceptance needs this: after a heading or a directions block the
			// current question is nil, and without it a paper would have to number
			// every question consecutively with nothing ever interrupting, which no
			// real paper does.
			lastComplete = cur.FilledOptions() >= opts.MinOptions
			out = append(out, *cur)
			cur = nil
		}
		curOptIdx = -1
		dest = destStem
	}

	// sealOpenContexts closes every still-open inferred range at a boundary:
	// another directions block, a section heading, or the end of the document.
	sealOpenContexts := func(upto int) {
		for i := range contexts {
			if contexts[i].to == 0 && contexts[i].from <= upto {
				contexts[i].to = upto
			}
		}
	}

	closeContext := func(nextQ int) {
		commit()
		if pendingCtx == nil {
			return
		}
		if pendingCtx.from == 0 {
			pendingCtx.from = nextQ
		}
		if pendingCtx.text != "" {
			if pendingCtx.from > 1 {
				sealOpenContexts(pendingCtx.from - 1)
			}
			contexts = append(contexts, *pendingCtx)
		}
		pendingCtx = nil
	}

	appendText := func(s string) {
		if s == "" {
			return
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(s)
	}

	ensureOptions := func(q *ParsedQuestion, upto int) {
		for len(q.Options) <= upto {
			q.Options = append(q.Options, ParsedOption{
				Label: labelText(optStyle.Kind, len(q.Options)),
			})
		}
	}

	startSection := func(label string, subject *SubjectRef) {
		closeQuestion()
		// A new section is a hard boundary for shared material. Instructions for
		// a reasoning section do not carry into the general knowledge section.
		closeContext(lastQNum + 1)
		sealOpenContexts(lastQNum)
		sectionLabel = label
		sectionSubject = subject
		sections = append(sections, ParsedSection{
			Label:         label,
			Subject:       subject,
			OrderIndex:    len(sections),
			FirstQuestion: 0,
		})
	}

	for i, rawLine := range lines {
		// A section banner spanning a multi-column page arrives as a one-row table
		// repeating itself across cells. Flatten it back to plain text so heading
		// detection can see it.
		if banner := tableHeadingText(rawLine); banner != "" {
			rawLine = banner
		}

		line := stripMarkdown(rawLine)
		if line == "" {
			// A blank line ends the current text run but keeps the destination, so
			// a stem split across a paragraph break still joins up.
			commit()
			afterBlank = true
			continue
		}
		if isTableDivider(rawLine) {
			continue
		}
		page := 1
		if i < len(pages) {
			page = pages[i]
		}

		// A complete option set followed by a paragraph break ends the question.
		// Anything after it is set aside for review instead of extending the last
		// choice, which is how page furniture and whole neighbouring questions
		// used to end up inside an option.
		if afterBlank && dest == destOption && cur != nil && curOptIdx+1 >= optTarget {
			commit()
			dest = destTrailing
		}
		afterBlank = false

		if cur != nil {
			cur.SourceLastLine = i
		}

		// --- Shared directions / comprehension passages -------------------
		// A printed range is authoritative, so this form is honoured even in the
		// middle of a question: the document is telling us exactly what it covers.
		if m := directionsRe.FindStringSubmatch(line); m != nil {
			from, to := atoiSafe(m[1]), atoiSafe(m[2])
			if plausibleRange(from, to) {
				closeQuestion()
				closeContext(0)
				pendingCtx = &contextRange{from: from, to: to, explicit: true}
				dest = destContext
				appendText(line)
				continue
			}
		}
		// Without a printed range, the lead-in is only shared material when it sits
		// between questions: either none is open, or the one in hand already has
		// its full set of choices. The same wording part-way through a stem is
		// that stem's own text, and treating it as a shared preamble is what used
		// to truncate the stem and then paste the lead-in onto every question
		// that followed.
		betweenQuestions := cur == nil || (curOptIdx+1 >= optTarget && cur.FilledOptions() >= optTarget)
		if betweenQuestions && bareDirectionsRe.MatchString(line) && !qStyleMatches(qStyle, line) {
			closeQuestion()
			closeContext(0)
			pendingCtx = &contextRange{}
			dest = destContext
			appendText(line)
			continue
		}

		// --- Question marker ----------------------------------------------
		if n, rest, hit := qStyle.match(line); hit {
			claimedByOption := false

			// The hard case: options labelled with digits look exactly like
			// question numbers. Inside an open question, the next digit in the
			// option sequence belongs to the options.
			if optStyle.Numeric() && cur != nil {
				wantIdx := curOptIdx + 1
				if wantIdx < optTarget && n-1 == wantIdx {
					claimedByOption = true
				}
			}

			complete := lastComplete
			if cur != nil {
				complete = cur.FilledOptions() >= opts.MinOptions
			}
			if !claimedByOption && acceptQuestionNumber(n, lastQNum, complete, qStyle, opts) {
				closeContext(n)
				closeQuestion()
				cur = &ParsedQuestion{
					Number:          n,
					PageNo:          page,
					SectionLabel:    sectionLabel,
					Subject:         sectionSubject,
					SourceFirstLine: i,
					SourceLastLine:  i,
				}
				if len(sections) > 0 && sections[len(sections)-1].FirstQuestion == 0 {
					sections[len(sections)-1].FirstQuestion = n
				}
				lastQNum = n
				curOptIdx = -1
				dest = destStem

				// The stem may continue on the same line, and the options may even
				// be packed in behind it.
				if prefix, inline := splitInlineOptions(optStyle, rest, 0); inline != nil && len(inline) >= 2 {
					appendText(prefix)
					commit()
					for idx, o := range inline {
						if idx >= optTarget {
							break
						}
						ensureOptions(cur, idx)
						cur.Options[idx].Text = o.Text
						if o.Label != "" {
							cur.Options[idx].Label = o.Label
						}
						curOptIdx = idx
					}
					dest = destOption
				} else {
					appendText(rest)
				}
				continue
			}
		}

		// --- Options ------------------------------------------------------
		if cur != nil && optStyle.anchored != nil {
			// Several options on one line.
			if prefix, inline := splitInlineOptions(optStyle, line, curOptIdx+1); inline != nil && len(inline) >= 2 {
				first := labelIndex(optStyle.Kind, inline[0].Label)
				if first == curOptIdx+1 || first == 0 {
					commit()
					if prefix != "" && curOptIdx < 0 {
						// Text before the first option still belongs to the stem.
						appendText(prefix)
						commit()
					}
					base := first
					if base < 0 {
						base = 0
					}
					// A full option row restarting at the first label means the
					// earlier row was a numbered statement list, not the choices.
					// The later row wins because the choices are always printed
					// last, but the substitution is recorded: the statements it
					// displaced are no longer in the stem, so the question is
					// incomplete and a reviewer has to see that.
					if base == 0 && curOptIdx+1 >= optTarget {
						cur.AddIssue(IssueOptionsReplaced,
							"a second option row replaced the first; the earlier row was probably a statement list")
						for k := range cur.Options {
							cur.Options[k].Text = ""
						}
					}
					for k, o := range inline {
						idx := base + k
						if idx >= optTarget {
							break
						}
						ensureOptions(cur, idx)
						if cur.Options[idx].Text != "" {
							cur.Options[idx].Text += " "
						}
						cur.Options[idx].Text += o.Text
						if o.Label != "" {
							cur.Options[idx].Label = o.Label
						}
						curOptIdx = idx
					}
					dest = destOption
					continue
				}
			}

			// One option per line.
			if m := optStyle.anchored.FindStringSubmatch(line); m != nil {
				idx := labelIndex(optStyle.Kind, m[1])
				if idx >= 0 && idx == curOptIdx+1 && idx < optTarget {
					commit()
					curOptIdx = idx
					dest = destOption
					ensureOptions(cur, idx)
					cur.Options[idx].Label = strings.TrimSpace(m[1])
					appendText(strings.TrimSpace(m[2]))
					continue
				}
			}
		}

		// --- Inline answer and solution lines -----------------------------
		if cur != nil {
			if m := inlineAnswerRe.FindStringSubmatch(line); m != nil {
				commit()
				maxIdx := optTarget
				if idx, valid := resolveLabel(m[1], maxIdx); valid {
					ensureOptions(cur, idx)
					for k := range cur.Options {
						cur.Options[k].IsCorrect = k == idx
					}
				} else {
					cur.AnswerText = collapse(m[1])
				}
				dest = destStem
				continue
			}
			if m := inlineSolutionRe.FindStringSubmatch(line); m != nil {
				commit()
				dest = destExplanation
				if idx, found := findVerdict(line, optTarget); found {
					ensureOptions(cur, idx)
					hasCorrect := false
					for _, o := range cur.Options {
						if o.IsCorrect {
							hasCorrect = true
							break
						}
					}
					if !hasCorrect {
						for k := range cur.Options {
							cur.Options[k].IsCorrect = k == idx
						}
					}
				}
				appendText(collapse(m[1]))
				continue
			}
		}

		// --- Section headings ---------------------------------------------
		// A section never begins part-way through a question's answer choices. A
		// line that reads as a heading between option 2 and option 3 is an option
		// whose text happens to name a subject, and acting on it would end the
		// question and desynchronise the numbering for everything after it. Once
		// the choices are complete, a heading there is believable again.
		midOptionRun := curOptIdx >= 0 && curOptIdx+1 < optTarget
		if !midOptionRun {
			if subject, hit := opts.Subjects.Match(line); hit {
				startSection(collapse(stripMarkdown(line)), &subject)
				continue
			}
			if looksLikeHeading(rawLine) && headingPrefixRe.MatchString(line) && cur == nil {
				startSection(collapse(stripMarkdown(line)), nil)
				continue
			}
		}

		// --- Everything else is body text ---------------------------------
		appendText(line)
	}

	closeContext(lastQNum + 1)
	closeQuestion()

	applyContexts(out, contexts, lastQNum)
	return out, sections
}

// plausibleRange rejects number pairs that are not a question range, which is
// what keeps "Directions: the 1990-1995 reforms" from being read as one.
func plausibleRange(from, to int) bool {
	return from >= 1 && to >= from && to-from < 200 && to <= 2000
}

// qStyleMatches reports whether a line looks like a question marker, used to
// keep a "Directions" lead-in from swallowing a numbered question.
func qStyleMatches(style QuestionStyle, line string) bool {
	_, _, hit := style.match(line)
	return hit
}

// acceptQuestionNumber decides whether a numeric marker really starts the next
// question.
//
// Explicitly prefixed markers ("Q12.") are always accepted because nothing else
// looks like them. Bare numbers must move forward by a small step, and the
// question before the gap must have looked complete, which is what distinguishes
// missing source numbering from a misread line.
//
// previousComplete refers to the last question that was being collected, whether
// it is still open or has just been filed. Judging only the open question was
// wrong: a heading or a directions block between two questions closes the open
// one, and then every later number looked like a misread line.
func acceptQuestionNumber(n, lastQNum int, previousComplete bool, style QuestionStyle, opts Options) bool {
	if n < 1 {
		return false
	}
	if style.Explicit() {
		return true
	}

	// The first question is the one the style detector found the document's
	// numbering run starting at. A paper split by shift or by section starts at
	// 51 or 76 rather than 1, and rejecting those loses the whole document.
	if lastQNum == 0 {
		if opts.firstQuestion > 0 {
			return n == opts.firstQuestion
		}
		return n <= 50
	}
	if n <= lastQNum {
		return false
	}

	step := n - lastQNum
	if step == 1 {
		return true
	}
	return step <= maxNumberGap && previousComplete
}

// maxNumberGap is how far the printed numbering may jump forward and still be
// believed. Papers skip numbers where a question was withdrawn, and extraction
// loses one occasionally; beyond this a jump is far more likely to be a figure in
// the text than a question marker.
const maxNumberGap = 12

// applyContexts attaches shared passages to the questions they cover.
//
// An open-ended range printed by the document ("Directions (90-95)") is honoured
// exactly. An open-ended range we inferred is capped, because an inferred range
// that runs to the end of the paper attaches a reasoning instruction to a physics
// question and there is no way for a reader to tell it was never meant to be
// there.
func applyContexts(questions []ParsedQuestion, contexts []contextRange, lastQNum int) {
	if len(contexts) == 0 {
		return
	}
	for i := range contexts {
		if contexts[i].to != 0 {
			continue
		}
		if contexts[i].explicit {
			contexts[i].to = lastQNum
			continue
		}
		limit := contexts[i].from + openContextCap - 1
		if limit >= lastQNum {
			contexts[i].to = lastQNum
		} else {
			contexts[i].to = limit
			contexts[i].capped = true
		}
	}

	for qi := range questions {
		num := questions[qi].Number
		for ci := range contexts {
			ctx := &contexts[ci]
			if num < ctx.from || num > ctx.to {
				continue
			}
			questions[qi].Context = ctx.text
			questions[qi].ContextKey = contextKey(ctx.text)
			if ctx.capped {
				questions[qi].AddIssue(IssueContextCapped,
					fmt.Sprintf("shared preamble had no printed range; assumed questions %d-%d", ctx.from, ctx.to))
			}
			break
		}
	}
}

// contextKey fingerprints a shared passage so questions citing the same one can
// be linked to a single stored copy.
func contextKey(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return ContentHash(text, nil)
}

// applyAnswerKey marks the correct option for every question the key covers.
func applyAnswerKey(questions []ParsedQuestion, key AnswerKey) {
	for i := range questions {
		idx, found := key[questions[i].Number]
		if !found {
			continue
		}
		if idx < 0 || idx >= len(questions[i].Options) {
			continue
		}
		for k := range questions[i].Options {
			questions[i].Options[k].IsCorrect = k == idx
		}
	}
}

// ParseAnswerKeyDocument reads a standalone answer key: a document that holds
// only answers, uploaded separately from the paper it belongs to. It returns a
// map of question number to zero-based correct option index.
//
// Worked solutions are also scanned, so a "solutions" booklet supplies answers
// just as well as a bare key.
func ParseAnswerKeyDocument(markdown string, maxOptions int) (AnswerKey, map[int]string) {
	if maxOptions <= 0 {
		maxOptions = 6
	}
	lines, _ := prepareLines(markdown)

	key := parseAnswerKey(lines, maxOptions)
	explanations := map[int]string{}

	for _, block := range parseExplanations(lines, newQuestionStyle("", WrapDot), maxOptions) {
		if block.text != "" {
			explanations[block.number] = block.text
		}
		if _, known := key[block.number]; !known && block.answer >= 0 {
			key[block.number] = block.answer
		}
	}
	return key, explanations
}

// applyExplanations attaches worked solutions, filling in any answer the key
// did not provide.
func applyExplanations(questions []ParsedQuestion, blocks []explanationBlock) {
	byNum := make(map[int]*ParsedQuestion, len(questions))
	for i := range questions {
		byNum[questions[i].Number] = &questions[i]
	}
	for _, b := range blocks {
		q, found := byNum[b.number]
		if !found {
			continue
		}
		if b.text != "" && q.Explanation == "" {
			q.Explanation = b.text
		}
		if b.answer < 0 || b.answer >= len(q.Options) {
			continue
		}
		hasCorrect := false
		for _, o := range q.Options {
			if o.IsCorrect {
				hasCorrect = true
				break
			}
		}
		if !hasCorrect {
			for k := range q.Options {
				q.Options[k].IsCorrect = k == b.answer
			}
		}
	}
}

// finalize trims empty options, classifies each question and records what is
// wrong with it.
//
// Only genuinely empty fragments are discarded. A question that came out short,
// or without choices, is kept and flagged, because a broken question a reviewer
// can see is worth more than a silent drop: the drop looks like the paper simply
// had fewer questions.
func finalize(questions []ParsedQuestion, optCount int, opts Options) ([]ParsedQuestion, int) {
	kept := make([]ParsedQuestion, 0, len(questions))
	dropped := 0

	for _, q := range questions {
		q.Text = collapse(q.Text)
		q.Context = collapse(q.Context)
		q.Explanation = collapse(q.Explanation)
		q.Trailing = collapse(q.Trailing)

		// Drop trailing options the layout created but never filled.
		for len(q.Options) > 0 && strings.TrimSpace(q.Options[len(q.Options)-1].Text) == "" {
			q.Options = q.Options[:len(q.Options)-1]
		}
		for k := range q.Options {
			q.Options[k].Text = collapse(q.Options[k].Text)
			if q.Options[k].Label == "" {
				q.Options[k].Label = labelText(LabelUpperAlpha, k)
			}
		}

		filled := q.FilledOptions()
		if q.Text == "" && filled == 0 {
			// Nothing at all was recovered, so there is nothing to review.
			dropped++
			continue
		}

		if filled == 0 {
			q.AddIssue(IssueNoOptions, "no answer choices were found for this question")
		} else if optCount > 0 && filled < optCount {
			q.AddIssue(IssueIncompleteOptions,
				fmt.Sprintf("found %d of the %d choices this paper prints", filled, optCount))
		}
		if len([]rune(q.Text)) < opts.MinQuestionChars {
			q.AddIssue(IssueStemTooShort,
				fmt.Sprintf("stem is %d characters, below the %d minimum", len([]rune(q.Text)), opts.MinQuestionChars))
		}
		if q.Trailing != "" && !q.HasIssue(IssueOptionOverrun) {
			q.AddIssue(IssueTrailingText, "text followed a complete option set and was set aside")
		}
		if strings.ContainsRune(q.Text, unreadableRune) || optionsContainRune(q.Options, unreadableRune) {
			q.AddIssue(IssueUnreadableSource,
				"the source font left characters unmapped, so part of this question is missing")
		}

		q.Type = classify(q, opts)
		q.Confidence = questionConfidence(q, optCount)
		kept = append(kept, q)
	}
	return kept, dropped
}

// unreadableRune is what the converter substitutes for a character the source
// fonts never mapped to Unicode.
const unreadableRune = '\uFFFD'

func optionsContainRune(options []ParsedOption, r rune) bool {
	for _, o := range options {
		if strings.ContainsRune(o.Text, r) {
			return true
		}
	}
	return false
}

// questionConfidence scores how cleanly one question came out of the document.
//
// This is the parser's own opinion about its work, separate from whether the
// content is any good. It exists so the pipeline can rank what a human should
// look at first instead of presenting a thousand questions as equally sound.
func questionConfidence(q ParsedQuestion, optCount int) float64 {
	score := 1.0
	for _, issue := range q.Issues {
		switch issue.Code {
		case IssueUnreadableSource:
			score -= 0.5
		case IssueNoOptions:
			score -= 0.45
		case IssueOptionsReplaced:
			score -= 0.35
		case IssueOptionOverrun:
			score -= 0.3
		case IssueStemTooShort:
			score -= 0.3
		case IssueIncompleteOptions:
			score -= 0.2
		case IssueTrailingText:
			score -= 0.15
		case IssueContextCapped:
			score -= 0.1
		}
	}
	if optCount > 0 && q.FilledOptions() == optCount && len([]rune(q.Text)) > 25 {
		// A question that matches the paper's own convention exactly is as good
		// as extraction gets.
		score += 0.05
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}

// classify infers the structural type of a question from what was parsed. It
// never guesses difficulty: that needs judgement the parser does not have.
func classify(q ParsedQuestion, opts Options) models.QuestionType {
	filled := q.FilledOptions()
	correct := 0
	for _, o := range q.Options {
		if o.IsCorrect {
			correct++
		}
	}

	switch {
	case filled >= opts.MinOptions && correct > 1:
		return models.TypeMultiSelect
	case filled == 2 && looksTrueFalse(q.Options):
		return models.TypeTrueFalse
	case filled >= opts.MinOptions:
		if q.Context != "" {
			return models.TypeComprehension
		}
		if isAssertionReason(q) {
			return models.TypeAssertionReason
		}
		if isMatchColumns(q) {
			return models.TypeMatchColumns
		}
		return models.TypeMCQ
	case strings.Contains(q.Text, "____") || strings.Contains(q.Text, "..."):
		return models.TypeFillBlank
	default:
		return models.TypeDescriptive
	}
}

func looksTrueFalse(options []ParsedOption) bool {
	if len(options) != 2 {
		return false
	}
	pair := strings.ToLower(collapse(options[0].Text) + "|" + collapse(options[1].Text))
	switch pair {
	case "true|false", "false|true", "yes|no", "no|yes", "correct|incorrect", "incorrect|correct":
		return true
	}
	return false
}

func isAssertionReason(q ParsedQuestion) bool {
	text := strings.ToLower(q.Text)
	return (strings.Contains(text, "assertion") && strings.Contains(text, "reason")) ||
		(strings.Contains(text, "statement i") && strings.Contains(text, "statement ii"))
}

func isMatchColumns(q ParsedQuestion) bool {
	text := strings.ToLower(q.Text)
	return strings.Contains(text, "match the") &&
		(strings.Contains(text, "column") || strings.Contains(text, "list"))
}

// summarizeSections fills in question counts and ranges per detected section.
func summarizeSections(sections []ParsedSection, questions []ParsedQuestion) []ParsedSection {
	if len(questions) == 0 {
		return nil
	}
	// Group by the label each question recorded, preserving document order.
	type acc struct {
		section ParsedSection
		order   int
	}
	order := map[string]int{}
	group := map[string]*acc{}
	next := 0

	for _, s := range sections {
		if _, seen := order[s.Label]; !seen {
			order[s.Label] = next
			group[s.Label] = &acc{section: ParsedSection{
				Label:      s.Label,
				Subject:    s.Subject,
				OrderIndex: next,
			}, order: next}
			next++
		}
	}

	for _, q := range questions {
		label := q.SectionLabel
		entry, seen := group[label]
		if !seen {
			order[label] = next
			entry = &acc{section: ParsedSection{
				Label:      label,
				Subject:    q.Subject,
				OrderIndex: next,
			}, order: next}
			group[label] = entry
			next++
		}
		if entry.section.FirstQuestion == 0 || q.Number < entry.section.FirstQuestion {
			entry.section.FirstQuestion = q.Number
		}
		if q.Number > entry.section.LastQuestion {
			entry.section.LastQuestion = q.Number
		}
		entry.section.QuestionCount++
		if entry.section.Subject == nil && q.Subject != nil {
			entry.section.Subject = q.Subject
		}
	}

	out := make([]ParsedSection, 0, len(group))
	for _, entry := range group {
		if entry.section.QuestionCount == 0 {
			continue
		}
		out = append(out, entry.section)
	}
	// Sort by document order.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].OrderIndex < out[j-1].OrderIndex; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	for i := range out {
		out[i].OrderIndex = i
	}
	return out
}

// averageConfidence is the mean per-question parse confidence.
func averageConfidence(questions []ParsedQuestion) float64 {
	if len(questions) == 0 {
		return 0
	}
	total := 0.0
	for _, q := range questions {
		total += q.Confidence
	}
	return total / float64(len(questions))
}

// buildWarnings turns parse statistics into advice the UI can show.
func buildWarnings(res ParseResult, opts Options) []string {
	var out []string
	total := len(res.Questions)
	if total == 0 {
		out = append(out, "no questions were recovered; check that this document is a question paper "+
			"and that its text extracted cleanly")
		return out
	}
	if !res.AnswerKeyFound && !res.ExplanationsFound {
		out = append(out, "no answer key was found in this document; upload the key separately "+
			"or these questions will be held out of generated papers")
	}
	if res.AnsweredCount < total {
		out = append(out, fmt.Sprintf("%d of %d questions have no known answer", total-res.AnsweredCount, total))
	}
	if res.OptionCount > 0 {
		thin := 0
		for _, q := range res.Questions {
			if q.FilledOptions() < res.OptionCount {
				thin++
			}
		}
		if thin*4 > total {
			out = append(out, fmt.Sprintf("%d questions have fewer than %d options, "+
				"which usually means the layout confused the parser", thin, res.OptionCount))
		}
	}
	if opts.ExpectedCount > 0 {
		diff := opts.ExpectedCount - total
		if diff > opts.ExpectedCount/10 {
			out = append(out, fmt.Sprintf("the exam pattern expects %d questions but %d were recovered",
				opts.ExpectedCount, total))
		}
	}
	if res.DroppedCount > total/5 && res.DroppedCount > 3 {
		out = append(out, fmt.Sprintf("%d fragments were discarded as incomplete", res.DroppedCount))
	}
	if res.FlaggedCount > 0 {
		out = append(out, fmt.Sprintf("%d of %d questions came out with a parse problem and are held for review",
			res.FlaggedCount, total))
	}
	return out
}
