package quality

import (
	"strings"
	"testing"

	"mockcreator/internal/models"
)

// The inputs here are the real defects found in a delivered paper, plus the
// legitimate question types that earlier versions of these checks wrongly
// rejected.
//
// Both halves matter equally. A check that misses a corrupted question ships
// broken content; a check that flags a sound one buries a reviewer in noise until
// they stop reading the queue, which ships broken content too.

func opts() Options {
	return Options{ExpectedOptions: 4, MinStemChars: 12, MinExtractionConfidence: 0.7}
}

func mcq(stem string, correct int, options ...string) Input {
	in := Input{Type: models.TypeMCQ, Stem: stem}
	labels := []string{"1", "2", "3", "4", "5", "6"}
	for i, text := range options {
		in.Options = append(in.Options, OptionInput{
			Label:     labels[i],
			Text:      text,
			IsCorrect: i == correct,
		})
	}
	return in
}

func codes(issues models.QualityIssues) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, string(issue.Severity)+":"+issue.Code)
	}
	return strings.Join(parts, " ")
}

func hasCode(issues models.QualityIssues, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func severityOf(issues models.QualityIssues, code string) models.Severity {
	for _, issue := range issues {
		if issue.Code == code {
			return issue.Severity
		}
	}
	return ""
}

// --- questions that must pass ---------------------------------------------

func TestSoundQuestionsPass(t *testing.T) {
	cases := []struct {
		name  string
		input Input
	}{
		{
			"plain mcq",
			mcq("Which pathway in cellular respiration generates the most ATP?", 2,
				"Glycolysis", "Krebs cycle", "Electron transport chain", "Fermentation"),
		},
		{
			// A completion stem ends with a colon on purpose. An earlier check read
			// that as truncation and flagged every one of them.
			"sentence completion ending in a colon",
			mcq("For a rigid body, the angular velocity of any particle about a given axis of rotation is:", 1,
				"Proportional to its distance from the axis", "The same for all particles",
				"Inversely proportional to its distance", "Always zero"),
		},
		{
			// The instruction comes first and the question last. An earlier check
			// only looked at the start of the stem and flagged these as not asking
			// anything.
			"instruction first, question last",
			mcq("Arvind started a business by investing \u20b980,000. After 4 months Bhavin joined with \u20b91,20,000. If the total profit is \u20b91,05,000, find the share of Chandan.", 3,
				"\u20b926,500", "\u20b926,000", "\u20b926,200", "\u20b926,250"),
		},
		{
			// Options that differ only in punctuation are the entire point of a
			// spot-the-difference question. Normalising punctuation away made them
			// look like duplicates.
			"options differing only by punctuation",
			mcq("Which of the following is identical to the address given: Meenal Gupta 102, Silver Oaks, Sector 12, Noida, 201301", 0,
				"Meenal Gupta 102, Silver Oaks, Sector 12, Noida, 201301",
				"Meenal Gupta 102, Silver Oaks, Sector-12, Noida, 201301",
				"Meenal Gupta 102, Silver Oaks, Sector 12, Noida 201301",
				"Meenal Gupta 102, Silver Oaks, Sector 12, Noida, 201302"),
		},
		{
			// When every choice is a bare marker the question is asking which part
			// of the sentence is wrong, and markers are the correct content.
			"locate-the-error question whose options are position markers",
			mcq("Find the part of the sentence that contains an error: That the report failed to address the root causes (1)/ of the community unrest were surprising (2)/ given the exhaustive data (3)/ compiled over several months. (4)", 1,
				"(1)", "(2)", "(3)", "(4)"),
		},
		{
			"fill in the blank",
			mcq("The startup scaled so rapidly that its infrastructure could ___________ keep pace.", 0,
				"barely", "merely", "scarcely", "all but"),
		},
		{
			// The same cloze question, with its passage attached, is answerable and
			// must pass. Only the detached version is a defect.
			"cloze question with its passage attached",
			func() Input {
				in := mcq("Select the most appropriate option to fill in blank number 5.", 1,
					"however", "therefore", "meanwhile", "because")
				in.PassageText = "Read the passage and fill each blank. The committee met on Monday " +
					"to review the proposal. The budget had already been approved, and (5) " +
					"the schedule remained unchanged despite the objections raised earlier " +
					"in the year by several of the regional offices involved in the work."
				return in
			}(),
		},
		{
			"mathematics with superscripts and symbols",
			mcq("31\u00b3 + 18\u00b3 - 37\u00b3 + 210 is equal to:", 1,
				"-36810", "-14820", "45670", "-23450"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateQuestion(tc.input, opts())
			if status := issues.Worst(); status != models.QualityPass {
				t.Errorf("expected a pass, got %s with [%s]", status, codes(issues))
			}
		})
	}
}

// --- defects that must be caught ------------------------------------------

func TestCorruptedQuestionsAreRejected(t *testing.T) {
	cases := []struct {
		name string
		code string
		want models.Severity
		in   Input
	}{
		{
			// The original defect: a page header absorbed into the last option.
			name: "page furniture inside an option",
			code: CodeForeignContent,
			want: models.SeverityCritical,
			in: mcq("Which of the following addresses are identical to each other?", 2,
				"Arjun Mehta A-101, Emerald Towers, Surat",
				"Arjun Mehta A-101, Emerald Tower, Surat",
				"Arjun M. A-101, Emerald Tower, Surat",
				"Arjun Mehta A-101, Emerald Tower, Surat 1. 1 and 2 2. 1 and 3 3. 2 and 4 4. 3 and 4"),
		},
		{
			name: "a second question embedded in an option",
			code: CodeEmbeddedQuestion,
			want: models.SeverityCritical,
			in: mcq("Read the statements about the Goncha Festival of Bastar:", 0,
				"Tupki, a mock gun made of bamboo, uses Goncha fruit as bullets.",
				"The chariot procession was started by Chalukya rulers.",
				"Rath Yatra begins on Ashadh Shukla Dashmi. Which of the above statements are correct? Only 1 and 3",
				"1, 2 and 3 are correct"),
		},
		{
			name: "unresolved converter placeholder",
			code: CodePlaceholder,
			want: models.SeverityCritical,
			in: mcq("A bicycle wheel has a radius of 35 cm. What percentage of its circumference is covered in a quarter turn? <!-- formula-not-decoded -->", 1,
				"15%", "25%", "30%", "35%"),
		},
		{
			name: "characters the source font never mapped",
			code: CodeUnreadableChars,
			want: models.SeverityCritical,
			in:   mcq("Simplify: 22 1 3 .6 \ufffd\ufffd\ufffd 1.9", 0, "4.2", "5.2", "6.2", "7.2"),
		},
		{
			name: "backslash run where the source printed a blank",
			code: CodeBackslashRun,
			want: models.SeverityCritical,
			in: mcq(`In Carnatic music, a laghu with five beats is known as \\\\\ jaati.`, 2,
				"Tishra", "Chaturashra", "Khanda", "Mishra"),
		},
		{
			name: "text decoded with the wrong encoding",
			code: CodeMojibake,
			want: models.SeverityCritical,
			in: mcq("If # = \u00c3\u2014, @ = -, $ = +, then evaluate: 9 # 2 @ 3 $ 1", 2,
				"18", "14", "16", "17"),
		},
		{
			// A stacked fraction that did not linearise leaves identical options.
			name: "identical options",
			code: CodeOptionDuplicate,
			want: models.SeverityCritical,
			in: mcq("Three pipes A, B and C fill a tank in 6, 8 and 12 hours. How much longer is needed?", 0,
				"hours", "hours", "hours", "hours"),
		},
		{
			name: "no answer recorded",
			code: CodeNoAnswer,
			want: models.SeverityCritical,
			in: Input{
				Type:    models.TypeMCQ,
				Stem:    "Which of the following is the odd one out of these four items?",
				Options: []OptionInput{{Label: "1", Text: "Dog"}, {Label: "2", Text: "Cat"}, {Label: "3", Text: "Table"}, {Label: "4", Text: "Horse"}},
			},
		},
		{
			name: "two options marked correct on a single-answer question",
			code: CodeMultipleAnswers,
			want: models.SeverityCritical,
			in: Input{
				Type: models.TypeMCQ,
				Stem: "What is the capital city of France in Western Europe?",
				Options: []OptionInput{
					{Label: "1", Text: "Paris", IsCorrect: true},
					{Label: "2", Text: "Lyon", IsCorrect: true},
					{Label: "3", Text: "Nice"},
					{Label: "4", Text: "Marseille"},
				},
			},
		},
		{
			name: "explanation argues for a different option",
			code: CodeExplanationMismatch,
			want: models.SeverityCritical,
			in: func() Input {
				in := mcq("Which pathway in cellular respiration generates the most ATP?", 2,
					"Glycolysis", "Krebs cycle", "Electron transport chain", "Fermentation")
				in.Explanation = "Option (1) is correct. Glycolysis produces the most ATP."
				return in
			}(),
		},
		{
			// The statement-list case: the choices point at numbered items the stem
			// no longer contains, so the question is unanswerable.
			name: "choices refer to a list the stem does not contain",
			code: CodeMissingEnumeration,
			want: models.SeverityCritical,
			in: mcq("Rearrange the following sentences in correct order to make a logical passage:", 0,
				"3-2-4-1", "2-3-1-4", "3-1-4-2", "4-1-2-3"),
		},
		{
			name: "an option that is only a marker while its siblings have text",
			code: CodeOptionLabelOnly,
			want: models.SeverityCritical,
			in: mcq("Select the correct option: The startup scaled so rapidly that its infrastructure could ___ keep pace.", 0,
				"barely", "merely", "(3)", "scarcely"),
		},
		{
			name: "empty stem",
			code: CodeStemEmpty,
			want: models.SeverityCritical,
			in:   mcq("", 0, "Paris", "Lyon", "Nice", "Marseille"),
		},
		{
			name: "no options at all",
			code: CodeNoOptions,
			want: models.SeverityCritical,
			in:   Input{Type: models.TypeMCQ, Stem: "In the following questions, select the related word from the alternatives."},
		},
		{
			name: "an option far longer than its siblings",
			code: CodeOptionLengthOutlier,
			want: models.SeverityMajor,
			in: mcq("Select the letter-cluster that replaces the question mark: WZWT, WVOH, WRGV, ?", 0,
				"W J Q X", "W J Q W", "W H P X",
				"W J P X and also the whole of the next question about rivers in Tibet plus a running header that reads STAFF SELECTION COMMISSION COMBINED GRADUATE LEVEL"),
		},
		{
			// A cloze question whose passage is missing. Found by running six real
			// papers through the pipeline: two of these reached a generated paper
			// with identical stems and no way for a student to tell them apart.
			name: "a stem referring to material that is not attached",
			code: CodeMissingReference,
			want: models.SeverityCritical,
			in: mcq("Select the most appropriate option to fill in blank number 5.", 1,
				"however", "therefore", "meanwhile", "because"),
		},
		{
			name: "a comprehension question with no passage",
			code: CodePassageMissing,
			want: models.SeverityCritical,
			in: func() Input {
				in := mcq("According to the passage, how is wisdom primarily acquired?", 2,
					"Through textbooks", "Through emotional detachment",
					"Through experience and reflection", "Through algorithmic thinking")
				in.Type = models.TypeComprehension
				return in
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateQuestion(tc.in, opts())
			if !hasCode(issues, tc.code) {
				t.Fatalf("expected %s, got [%s]", tc.code, codes(issues))
			}
			if got := severityOf(issues, tc.code); got != tc.want {
				t.Errorf("%s should be %s, got %s", tc.code, tc.want, got)
			}
			if tc.want == models.SeverityCritical && issues.Worst() != models.QualityFailed {
				t.Errorf("a critical defect must fail the question, got %s", issues.Worst())
			}
		})
	}
}

// A defect in an explanation must not fail the question: the question still
// works, the explanation simply must not be published as it stands.
func TestExplanationDefectsAreMajorNotCritical(t *testing.T) {
	in := mcq("Which pathway in cellular respiration generates the most ATP?", 2,
		"Glycolysis", "Krebs cycle", "Electron transport chain", "Fermentation")
	in.Explanation = "The electron transport chain \ufffd\ufffd generates the most ATP."

	issues := ValidateQuestion(in, opts())
	if got := severityOf(issues, CodeUnreadableChars); got != models.SeverityMajor {
		t.Errorf("unreadable characters in an explanation should be major, got %s", got)
	}
	if status := issues.Worst(); status != models.QualityNeedsReview {
		t.Errorf("expected review, got %s with [%s]", status, codes(issues))
	}
}

// Unchecked is not a pass. An empty issue list means checked and clean; it is the
// stored status that distinguishes the two, and the gate must never treat a
// question nobody validated as deliverable.
func TestUncheckedIsNotDeliverable(t *testing.T) {
	if models.QualityUnchecked.Deliverable() {
		t.Error("unchecked content must not be deliverable")
	}
	if models.QualityNeedsReview.Deliverable() {
		t.Error("content awaiting review must not be deliverable")
	}
	if models.QualityFailed.Deliverable() {
		t.Error("failed content must not be deliverable")
	}
	if !models.QualityPass.Deliverable() {
		t.Error("passing content should be deliverable")
	}
}

// A model review that was configured but did not run is reported, so a question
// checked by the rules alone is never mistaken for one a model also read.
func TestMissingModelReviewIsReported(t *testing.T) {
	in := mcq("Which pathway in cellular respiration generates the most ATP?", 2,
		"Glycolysis", "Krebs cycle", "Electron transport chain", "Fermentation")

	options := opts()
	options.ModelExpected = true

	issues := ValidateQuestion(in, options)
	if !hasCode(issues, CodeModelNotRun) {
		t.Fatalf("expected the missing model review to be reported, got [%s]", codes(issues))
	}
	// Minor, so it does not block an otherwise sound question.
	if got := severityOf(issues, CodeModelNotRun); got != models.SeverityMinor {
		t.Errorf("a missing model review should be minor, got %s", got)
	}
	if status := issues.Worst(); status != models.QualityPass {
		t.Errorf("a minor note must not hold a sound question back, got %s", status)
	}

	in.ModelReviewed = true
	if issues := ValidateQuestion(in, options); hasCode(issues, CodeModelNotRun) {
		t.Error("a reviewed question should not report the gap")
	}
}

// Score ranks a review queue; severity decides the gate. A critical defect must
// not be averaged away by otherwise clean fields.
func TestScoreRanksButDoesNotGate(t *testing.T) {
	clean := ValidateQuestion(mcq("What is the capital city of France in Europe?", 0,
		"Paris", "Lyon", "Nice", "Marseille"), opts())
	broken := ValidateQuestion(mcq("Simplify: 22 \ufffd 1.9", 0, "4.2", "5.2", "6.2", "7.2"), opts())

	cleanScore := Score(clean, 1.0)
	brokenScore := Score(broken, 1.0)
	if brokenScore >= cleanScore {
		t.Errorf("a broken question should score lower: %.2f vs %.2f", brokenScore, cleanScore)
	}
	if broken.Worst() != models.QualityFailed {
		t.Error("severity, not score, decides the gate")
	}
}

func TestExtractionConfidenceFloorSendsToReview(t *testing.T) {
	in := mcq("Which pathway in cellular respiration generates the most ATP?", 2,
		"Glycolysis", "Krebs cycle", "Electron transport chain", "Fermentation")
	in.ExtractionConfidence = 0.4

	issues := ValidateQuestion(in, opts())
	if !hasCode(issues, CodeLowExtraction) {
		t.Fatalf("a poorly extracted question should be held, got [%s]", codes(issues))
	}
	if status := issues.Worst(); status != models.QualityNeedsReview {
		t.Errorf("expected review, got %s", status)
	}
}
