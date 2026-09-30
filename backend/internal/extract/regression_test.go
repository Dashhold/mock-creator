package extract

import (
	"strings"
	"testing"
)

// These tests are the failure cases from a real broken paper, each reduced to the
// smallest document that reproduces it.
//
// Every one of them was a defect a customer would have seen: a reasoning
// instruction glued to the front of a physics question, a 2,000-character
// passage repeated into eleven stems, a page header sitting inside an answer
// choice. They are kept as tests because all of them were caused by rules that
// looked reasonable in isolation, and the next plausible-looking rule can bring
// them back.
//
// The source excerpts are the geometric extractor's real output for cgl-1.pdf,
// so the fixtures exercise the same text the pipeline actually parses.

// A bare "In the following questions..." lead-in belongs to the question that
// prints it, not to the rest of the paper.
//
// Original defect: one such line was treated as an open-ended shared preamble
// and prepended to roughly ninety later stems, including questions on other
// subjects entirely.
func TestBareDirectionsDoNotLeakIntoLaterQuestions(t *testing.T) {
	const doc = `
1. Select the letter-cluster that replaces the question mark: WZWT, WVOH, ?
1. W J Q X 2. W J Q W 3. W H P X 4. W J P X
2. In the following questions, select the related word from
the given alternatives.
Mekong : Tibet :: Amazon : ?
1. Chile 2. Peru 3. Colombia 4. Ecuador
3. Which pathway in cellular respiration generates the most ATP?
1. Glycolysis 2. Krebs cycle 3. Electron transport chain 4. Fermentation
4. Argon is produced in Earth's crust via?
1. Decay of K-40 2. Volcanoes 3. Photosynthesis 4. Cosmic rays
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 4)

	// The lead-in is part of question 2's own stem.
	if !strings.Contains(byNumber[2].Text, "In the following questions") {
		t.Errorf("question 2 should keep its own lead-in, got %q", byNumber[2].Text)
	}

	// And it must not appear anywhere else, in a stem or as shared context.
	for _, number := range []int{1, 3, 4} {
		q := byNumber[number]
		if strings.Contains(q.Text, "In the following questions") {
			t.Errorf("question %d was contaminated with question 2's lead-in: %q", number, q.Text)
		}
		if strings.Contains(q.Context, "In the following questions") {
			t.Errorf("question %d picked up question 2's lead-in as shared context: %q", number, q.Context)
		}
	}
}

// A directions block that prints its own range covers exactly that range.
//
// Original defect: the pattern required the literal word "questions", so
// "Directions (90-95)" never matched at all. The passage was swallowed by the
// preceding question's last option, and every question after it lost its
// passage.
func TestDirectionsWithPrintedRangeCoversExactlyThatRange(t *testing.T) {
	const doc = `
88. Convert the following from active to passive: They have been neglecting the archives.
1. Maintenance had been neglected 2. Maintenance is being neglected
3. Maintenance was being neglected 4. Maintenance has been being neglected
89. Convert the sentence below from passive to active voice:
It was being suggested by multiple sources that the operation had been compromised.
1. Multiple sources suggested the operation was compromised internally.
2. The operation was compromised, multiple sources suggested.
3. The sources were suggesting an operation compromise.
4. The operation had compromised multiple internal sources.
Directions (90-92): Read the following passage and answer the questions based on the passage:
While education and wisdom are often conflated in colloquial discourse, a discerning
mind perceives a fundamental divergence between the two. Education is the formal
acquisition of knowledge, often measured through degrees and academic accolades.
90. According to the passage, how is wisdom primarily acquired?
1. Through textbooks 2. Through emotional detachment
3. Through experience and reflection 4. Through algorithmic thinking
91. What does the author mean by wisdom enriching the soul?
1. It enhances academic success 2. It fosters deeper moral insight
3. It improves verbal expression 4. It sharpens mathematical skills
92. Who, according to the author, can be wise despite no formal education?
1. Scientists 2. Farmers 3. Professors 4. Engineers
93. Choose the most suitable option to replace the highlighted part of the sentence:
She has the reputation to be a kind woman.
1. to have kindness 2. of being a kind woman
3. of being the kind woman 4. to be kind-hearted
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 90, 91, 92, 93, 89)

	for _, number := range []int{90, 91, 92} {
		q := byNumber[number]
		if !strings.Contains(q.Context, "conflated in colloquial discourse") {
			t.Errorf("question %d should carry the passage as context, got %q", number, q.Context)
		}
		// The passage must be referenced, never copied into the stem.
		if strings.Contains(q.Text, "conflated in colloquial discourse") {
			t.Errorf("question %d has the passage inlined into its stem: %q", number, q.Text)
		}
		if q.ContextKey == "" {
			t.Errorf("question %d has context but no key to store it under", number)
		}
	}

	// Question 93 is outside the printed range, so it gets nothing.
	if byNumber[93].Context != "" {
		t.Errorf("question 93 is outside the (90-92) range but received context: %q",
			byNumber[93].Context)
	}

	// And the question before the block keeps its own fourth option.
	if got := byNumber[89].FilledOptions(); got != 4 {
		t.Errorf("question 89 should still have 4 options, got %d", got)
	}
	if strings.Contains(byNumber[89].Options[3].Text, "Directions") {
		t.Errorf("the directions block leaked into question 89's last option: %q",
			byNumber[89].Options[3].Text)
	}

	// All questions sharing one passage share one key, so it is stored once.
	if byNumber[90].ContextKey != byNumber[92].ContextKey {
		t.Error("questions sharing a passage must share a context key so it is stored once")
	}
}

// Options printed two to a row must be read as four options, not two.
//
// Original defect: only runs starting at label 1 were recognised, so the second
// row merged into the third option: "Pressure 4. Force".
func TestOptionRowsSplitAcrossTwoLines(t *testing.T) {
	const doc = `
1. Watt : Power :: Pascal : ?
1. Energy 2. Temperature
3. Pressure 4. Force
2. Identify the odd one: 2, 3, 5, 7, 11, 13, 17, 20
1. 17
2. 13
3. 20
4. 11
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 1, 2)

	want := []string{"Energy", "Temperature", "Pressure", "Force"}
	got := byNumber[1].Options
	if len(got) != 4 {
		t.Fatalf("expected 4 options, got %d: %+v", len(got), optionTexts(got))
	}
	for i, expected := range want {
		if strings.TrimSpace(got[i].Text) != expected {
			t.Errorf("option %d: got %q, want %q", i+1, got[i].Text, expected)
		}
	}

	// One per line still works.
	if n := byNumber[2].FilledOptions(); n != 4 {
		t.Errorf("one option per line should still give 4, got %d: %+v",
			n, optionTexts(byNumber[2].Options))
	}
}

// A running page header must never end up inside an answer choice.
//
// Original defect: the last option was unbounded, so it absorbed everything up
// to the next question marker, including "STAFF SELECTION COMMISSION ... Max
// Marks: 200".
func TestPageFurnitureNeverEntersAnOption(t *testing.T) {
	const doc = `
10. Which of the following addresses are identical to each other?
1. Arjun Mehta A-101, Emerald Towers, Surat
2. Arjun Mehta A-101, Emerald Tower, Surat
3. Arjun M. A-101, Emerald Tower, Surat
4. Arjun Mehta A-101, Emerald Tower, Surat

STAFF SELECTION COMMISSION COMBINED GRADUATE LEVEL (TIER-I) SOLVED PAPER
(12th September 2025: Shift-1) Max Marks: 200

11. Which of the following is identical to the address given: Meenal Gupta 102, Noida
1. Meenal Gupta 102, Silver Oaks, Sector 12, Noida, 201301
2. Meenal Gupta 102, Silver Oaks, Sector-12, Noida, 201301
3. Meenal Gupta 102, Silver Oaks, Sector 12, Noida 201301
4. Meenal Gupta 102, Silver Oaks, Sector 12, Noida, 201302
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 10, 11)

	for _, option := range byNumber[10].Options {
		if strings.Contains(option.Text, "STAFF SELECTION") ||
			strings.Contains(option.Text, "Max Marks") {
			t.Errorf("page furniture leaked into an option of question 10: %q", option.Text)
		}
	}
	// Set aside, not silently dropped: a reviewer has to be able to see it.
	if byNumber[10].Trailing == "" {
		t.Error("the page header should be recorded as trailing text, not discarded")
	}
	if !byNumber[10].HasIssue(IssueTrailingText) && !byNumber[10].HasIssue(IssueOptionOverrun) {
		t.Error("setting text aside must be flagged so the question reaches review")
	}

	// Question 11's options differ only by punctuation, and all four must survive
	// distinctly: telling them apart is the whole question.
	if n := byNumber[11].FilledOptions(); n != 4 {
		t.Fatalf("question 11 should have 4 options, got %d", n)
	}
	seen := map[string]bool{}
	for _, option := range byNumber[11].Options {
		if seen[option.Text] {
			t.Errorf("question 11 lost a distinction between options: %q repeated", option.Text)
		}
		seen[option.Text] = true
	}
}

// A question whose options were replaced by a second row is flagged, because its
// statement list is now missing from the stem.
func TestStatementListReplacementIsFlagged(t *testing.T) {
	const doc = `
1. Which of the following addresses are identical to each other?
1. Arjun Mehta A-101, Emerald Towers, Surat, 395001
2. Arjun Mehta A-101, Emerald Tower, Surat, 395001
3. Arjun M. A-101, Emerald Tower, Surat, 395001
4. Arjun Mehta A-101, Emerald Tower, Surat, 395001
1. 1 and 2 2. 1 and 3 3. 2 and 4 4. 3 and 4
2. What is the capital of France?
1. Paris 2. Lyon 3. Nice 4. Marseille
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 1, 2)

	if !byNumber[1].HasIssue(IssueOptionsReplaced) {
		t.Error("a replaced option row must be flagged: the statements it displaced are gone")
	}
	// The later row is the real set of choices.
	if got := strings.TrimSpace(byNumber[1].Options[0].Text); got != "1 and 2" {
		t.Errorf("the final option row should win, got %q", got)
	}
	// A normal question in the same document stays clean.
	if len(byNumber[2].Issues) != 0 {
		t.Errorf("question 2 should be clean, got %+v", byNumber[2].Issues)
	}
}

// Characters the source font never mapped are marked unreadable and the question
// is flagged, rather than the sentinel being deleted or passed through silently.
func TestUnreadableCharactersFlagTheQuestion(t *testing.T) {
	const doc = "\n" +
		"1. Simplify: 22 1 3 .6 \ufffd\ufffd\ufffd 1.9\n" +
		"1. 4.2 2. 5.2 3. 6.2 4. 7.2\n" +
		"2. What is 15% of 200?\n" +
		"1. 25 2. 30 3. 35 4. 40\n"

	result := Parse(doc, Options{})
	byNumber := index(t, result, 1, 2)

	if !byNumber[1].HasIssue(IssueUnreadableSource) {
		t.Error("a question containing an unreadable character must be flagged")
	}
	if byNumber[1].Confidence >= byNumber[2].Confidence {
		t.Errorf("the damaged question should score lower: %.2f vs %.2f",
			byNumber[1].Confidence, byNumber[2].Confidence)
	}
	if byNumber[2].HasIssue(IssueUnreadableSource) {
		t.Error("the undamaged question must not be flagged")
	}
}

// An unnumbered shared preamble is bounded. Left open it attaches itself to the
// whole remainder of a paper, which is the original contamination bug in its
// most general form.
func TestInferredContextRangeIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("\nDirections: Read the following statements carefully before answering.\n")
	// Far more questions than the cap, so an unbounded range would reach them all.
	for i := 1; i <= 40; i++ {
		b.WriteString(sprintQuestion(i))
	}

	result := Parse(b.String(), Options{})
	if len(result.Questions) < 30 {
		t.Fatalf("expected the questions to parse, got %d", len(result.Questions))
	}

	withContext := 0
	for _, q := range result.Questions {
		if q.Context != "" {
			withContext++
		}
	}
	if withContext == 0 {
		t.Fatal("the preamble should apply to the questions that follow it")
	}
	if withContext > openContextCap {
		t.Errorf("an inferred range reached %d questions; the cap is %d", withContext, openContextCap)
	}

	// And the questions it did reach say so, because the range was a guess.
	flagged := false
	for _, q := range result.Questions {
		if q.HasIssue(IssueContextCapped) {
			flagged = true
			break
		}
	}
	if !flagged {
		t.Error("a capped range must be recorded on the affected questions")
	}
}

// A section heading resets shared material: instructions for one section do not
// carry into the next.
func TestSectionHeadingSealsSharedMaterial(t *testing.T) {
	const doc = `
## General Intelligence and Reasoning

Directions: Study the following letter series carefully.

1. Find the next term: A, C, E, ?
1. F 2. G 3. H 4. I
2. Find the next term: B, D, F, ?
1. G 2. H 3. I 4. J

## General Awareness

3. Who wrote the Indian national anthem?
1. Tagore 2. Bankim Chandra 3. Nehru 4. Gandhi
4. Argon is produced in Earth's crust via?
1. Decay of K-40 2. Volcanoes 3. Photosynthesis 4. Cosmic rays
`

	result := Parse(doc, Options{Subjects: matcher()})
	byNumber := index(t, result, 1, 2, 3, 4)

	for _, number := range []int{3, 4} {
		if strings.Contains(byNumber[number].Context, "letter series") {
			t.Errorf("reasoning instructions carried into question %d: %q",
				number, byNumber[number].Context)
		}
	}
	// Sections were recognised, which is what makes the boundary meaningful.
	if len(result.Sections) < 2 {
		t.Errorf("expected two sections, got %d", len(result.Sections))
	}
}

// Nothing recoverable is thrown away. A question that came out incomplete is
// kept and flagged, because a silent drop looks like the paper simply had fewer
// questions.
func TestBrokenQuestionsAreKeptAndFlagged(t *testing.T) {
	const doc = `
1. What is the capital of France?
1. Paris 2. Lyon 3. Nice 4. Marseille
2. A question with no choices printed beneath it at all
3. Another complete question about geography?
1. Delhi 2. Mumbai 3. Chennai 4. Kolkata
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 1, 2, 3)

	if result.DroppedCount != 0 {
		t.Errorf("nothing recoverable should be dropped, got %d", result.DroppedCount)
	}
	if !byNumber[2].HasIssue(IssueNoOptions) {
		t.Error("a question with no choices must be flagged, not discarded")
	}
	if result.FlaggedCount == 0 {
		t.Error("the result should report how many questions carry a problem")
	}
	for _, number := range []int{1, 3} {
		if len(byNumber[number].Issues) != 0 {
			t.Errorf("question %d should be clean, got %+v", number, byNumber[number].Issues)
		}
	}
}

// Every question records where it came from, so a reviewer can be shown the
// source rather than asked to trust the parser.
func TestProvenanceIsRecorded(t *testing.T) {
	const doc = `[page] 1

1. What is the capital of France?
1. Paris 2. Lyon 3. Nice 4. Marseille

[page] 2

2. What is the capital of Japan?
1. Tokyo 2. Osaka 3. Kyoto 4. Nagoya
`

	result := Parse(doc, Options{})
	byNumber := index(t, result, 1, 2)

	if byNumber[1].PageNo != 1 {
		t.Errorf("question 1 should be on page 1, got %d", byNumber[1].PageNo)
	}
	if byNumber[2].PageNo != 2 {
		t.Errorf("question 2 should be on page 2, got %d", byNumber[2].PageNo)
	}
	for _, number := range []int{1, 2} {
		q := byNumber[number]
		if q.SourceLastLine < q.SourceFirstLine {
			t.Errorf("question %d has an invalid source line span %d-%d",
				number, q.SourceFirstLine, q.SourceLastLine)
		}
	}
}

// --- helpers --------------------------------------------------------------

// index looks up questions by their printed number and fails the test when any
// of the expected ones are missing.
func index(t *testing.T, result ParseResult, want ...int) map[int]ParsedQuestion {
	t.Helper()
	byNumber := make(map[int]ParsedQuestion, len(result.Questions))
	for _, q := range result.Questions {
		byNumber[q.Number] = q
	}
	for _, number := range want {
		if _, found := byNumber[number]; !found {
			numbers := make([]int, 0, len(byNumber))
			for n := range byNumber {
				numbers = append(numbers, n)
			}
			t.Fatalf("question %d was not extracted; found %v (warnings: %v)",
				number, numbers, result.Warnings)
		}
	}
	return byNumber
}

func optionTexts(options []ParsedOption) []string {
	out := make([]string, 0, len(options))
	for _, o := range options {
		out = append(out, o.Text)
	}
	return out
}

// sprintQuestion renders a simple four-option question, for tests that need a
// document longer than a fixture can reasonably spell out.
func sprintQuestion(n int) string {
	var b strings.Builder
	b.WriteString(itoa(n))
	b.WriteString(". Question number ")
	b.WriteString(itoa(n))
	b.WriteString(" asks something specific about a topic?\n1. First 2. Second 3. Third 4. Fourth\n")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [8]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
