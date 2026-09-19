package quality

import (
	"fmt"
	"regexp"
	"strings"

	"mockcreator/internal/extract"
	"mockcreator/internal/models"
)

// Question-level defect codes.
const (
	CodeStemEmpty           = "stem_empty"
	CodeStemTooShort        = "stem_too_short"
	CodeStemTruncated       = "stem_truncated"
	CodeStemNotAQuestion    = "stem_not_a_question"
	CodeNoOptions           = "no_options"
	CodeTooFewOptions       = "too_few_options"
	CodeOptionCountMismatch = "option_count_mismatch"
	CodeOptionEmpty         = "option_empty"
	CodeOptionDuplicate     = "option_duplicate"
	CodeOptionLabelOnly     = "option_label_only"
	CodeOptionLengthOutlier = "option_length_outlier"
	CodeNoAnswer            = "no_answer"
	CodeMultipleAnswers     = "multiple_answers"
	CodeAnswerNotAnOption   = "answer_not_an_option"
	CodeExplanationMismatch = "explanation_mismatch"
	CodeMissingEnumeration  = "missing_enumeration"
	CodeMissingReference    = "missing_reference"
	CodeTrailingText        = "trailing_text"
	CodeUnclassifiedSubject = "unclassified_subject"
	CodeLowExtraction       = "low_extraction_confidence"
	CodePassageMissing      = "passage_missing"
	CodePassageTooShort     = "passage_too_short"
	CodeDuplicateQuestion   = "duplicate_question"
	CodeNearDuplicate       = "near_duplicate_question"
	CodeModelNotRun         = "model_review_not_run"
)

// Options configures the question checks.
type Options struct {
	// ExpectedOptions is how many choices this document's or exam's convention
	// calls for. Zero means "no expectation", and then only the floor applies.
	ExpectedOptions int
	// MinOptions is the fewest choices an objective question may carry.
	MinOptions int
	// MinStemChars is the shortest a stem may be.
	MinStemChars int
	// UnsortedSubjectCode is the catch-all subject. A question filed there was
	// never classified, which is a defect in its own right.
	UnsortedSubjectCode string
	// MinExtractionConfidence is the parser confidence below which a question is
	// sent to review regardless of how it otherwise reads.
	MinExtractionConfidence float64
	// ModelExpected records that a model review was configured. When it is true
	// and no model reviewed this question, that gap is reported rather than
	// passed over in silence.
	ModelExpected bool
}

// WithDefaults fills in sensible floors.
func (o Options) WithDefaults() Options {
	if o.MinOptions <= 0 {
		o.MinOptions = 2
	}
	if o.MinStemChars <= 0 {
		o.MinStemChars = 12
	}
	if o.UnsortedSubjectCode == "" {
		o.UnsortedSubjectCode = "unsorted"
	}
	if o.MinExtractionConfidence <= 0 {
		o.MinExtractionConfidence = 0.7
	}
	return o
}

// Input is everything the checks need about one question.
//
// It is a plain struct rather than a models.Question so the same rules can be
// applied to a question that has just been parsed and to one already in the
// warehouse, without either caller having to fake the other's shape.
type Input struct {
	Type        models.QuestionType
	Stem        string
	Options     []OptionInput
	AnswerText  string
	Explanation string

	// PassageText is the shared material this question refers to, when any.
	PassageText string
	// TrailingText is material the parser set aside after a complete question.
	TrailingText string

	SubjectCode string
	// ExtractIssues are what the parser recorded while reading this question.
	ExtractIssues        []extract.ExtractIssue
	ExtractionConfidence float64
	// ModelReviewed records that a language model has audited this question.
	ModelReviewed bool
}

// OptionInput is one answer choice.
type OptionInput struct {
	Label     string
	Text      string
	IsCorrect bool
}

var (
	// A stem that ends mid-word or mid-clause. Conjunctions and articles at the
	// end are the reliable signal that the text was cut off.
	truncatedTailRe = regexp.MustCompile(`(?i)\b(?:and|or|but|the|a|an|of|in|to|for|with|that|which|is|are|was|were|from|by|as|at|on)$`)

	// A stem that reads as a question, an instruction or a sentence to complete.
	// Papers phrase stems every one of those ways, so the test is deliberately
	// permissive: its job is to catch fragments that ask nothing at all, not to
	// enforce a house style.
	//
	// The verb list is searched anywhere in the stem rather than only at the
	// start, because a real stem routinely sets up the situation first and asks
	// the question last: "Arvind started a business ... find the share of
	// Chandan."
	interrogativeRe = regexp.MustCompile(`(?i)\?|` +
		`\b(?:select|choose|find|identify|which|what|who|whom|whose|when|where|why|how|` +
		`arrange|rearrange|match|complete|fill|read|consider|calculate|evaluate|simplify|solve|` +
		`convert|change|spot|pick|state|name|give|compute|determine|examine|compare|` +
		`correct|incorrect|true|false|appropriate|suitable|meaning|synonym|antonym|` +
		`value of|following|directions?)\b|` +
		`_{3,}|` + // a fill-in-the-blank stem need not be interrogative
		`:\s*$`) // a sentence-completion stem ends with a colon

	// "Option (3) is correct", "Ans. 2", "correct answer is B" inside an
	// explanation. Used to check the explanation agrees with the marked answer.
	explanationVerdictRe = regexp.MustCompile(`(?i)\b(?:option|ans(?:wer)?|choice)\b[^0-9a-h]{0,12}\(?([1-9]|[a-h])\)?\b`)
)

// ValidateQuestion applies every deterministic check and returns what it found.
//
// The checks correspond one to one with what a paper has to guarantee: the
// question is complete, its choices are usable, its answer exists and matches a
// choice, its explanation does not contradict that answer, nothing is corrupt,
// nothing foreign leaked in, and it was actually classified. Anything the rules
// cannot settle is reported as major so it reaches a human rather than being
// resolved by guesswork.
func ValidateQuestion(in Input, opts Options) models.QualityIssues {
	opts = opts.WithDefaults()
	var issues models.QualityIssues

	add := func(code string, severity models.Severity, field, message, evidence string) {
		issues = append(issues, models.QualityIssue{
			Code:     code,
			Severity: severity,
			Source:   models.SourceRules,
			Field:    field,
			Message:  message,
			Evidence: evidence,
		})
	}

	stem := strings.TrimSpace(in.Stem)
	filled := filledOptions(in.Options)

	// --- 1. a complete question ------------------------------------------
	switch {
	case stem == "":
		add(CodeStemEmpty, models.SeverityCritical, "stem",
			"the question has no text at all", "")
	case len([]rune(stem)) < opts.MinStemChars:
		add(CodeStemTooShort, models.SeverityCritical, "stem",
			fmt.Sprintf("the stem is %d characters, too short to be a question", len([]rune(stem))),
			trim(stem, 80))
	default:
		// A stem that deliberately trails off is not truncated: papers end
		// sentence-completion stems with a colon, a blank or an ellipsis, and the
		// choices finish the sentence. Only an abrupt stop counts.
		if !endsDeliberately(stem) && truncatedTailRe.MatchString(strings.TrimRight(stem, " .;")) {
			add(CodeStemTruncated, models.SeverityMajor, "stem",
				"the stem ends mid-sentence, so it was probably cut off", trim(lastWords(stem, 8), 80))
		}
		if !interrogativeRe.MatchString(stem) {
			add(CodeStemNotAQuestion, models.SeverityMajor, "stem",
				"the stem does not read as a question or an instruction", trim(stem, 90))
		}
	}

	// --- 2. valid options ------------------------------------------------
	objective := isObjective(in.Type)
	if objective {
		switch {
		case filled == 0:
			add(CodeNoOptions, models.SeverityCritical, "options",
				"no answer choices were found", "")
		case filled < opts.MinOptions:
			add(CodeTooFewOptions, models.SeverityCritical, "options",
				fmt.Sprintf("only %d choice(s) were found; at least %d are needed", filled, opts.MinOptions), "")
		case opts.ExpectedOptions > 0 && filled != opts.ExpectedOptions:
			add(CodeOptionCountMismatch, models.SeverityMajor, "options",
				fmt.Sprintf("found %d choices where this paper prints %d", filled, opts.ExpectedOptions), "")
		}
	}

	// When every choice is a bare marker the question is asking which *part* of
	// the stem is at fault ("... were surprising (2)/ ..."), and markers are the
	// correct content. Only a mixture is a defect, because then one choice lost
	// its text while its siblings kept theirs.
	labelOnly := labelOnlyCount(in.Options)
	markersAreTheAnswer := filled >= 2 && labelOnly == filled

	seen := map[string]int{}
	for i, opt := range in.Options {
		field := fmt.Sprintf("option:%d", i+1)
		text := strings.TrimSpace(opt.Text)
		if text == "" {
			if i < filled {
				add(CodeOptionEmpty, models.SeverityCritical, field,
					"this choice has no text", "")
			}
			continue
		}
		if !markersAreTheAnswer && isLabelOnly(text) {
			add(CodeOptionLabelOnly, models.SeverityCritical, field,
				"this choice contains only a label, not an answer", trim(text, 40))
		}
		// Comparison keeps punctuation and spacing. Stripping them would call
		// "Sector-12" and "Sector 12" the same choice, and telling those apart is
		// the entire point of a spot-the-difference question.
		key := strings.ToLower(strings.Join(strings.Fields(text), " "))
		if first, dup := seen[key]; dup {
			add(CodeOptionDuplicate, models.SeverityCritical, field,
				fmt.Sprintf("this choice repeats choice %d word for word", first), trim(text, 80))
		} else {
			seen[key] = i + 1
		}
	}

	// A choice far longer than its siblings has usually absorbed something.
	if outlier, idx := lengthOutlier(in.Options); outlier {
		add(CodeOptionLengthOutlier, models.SeverityMajor, fmt.Sprintf("option:%d", idx+1),
			"this choice is far longer than the others, which usually means it absorbed neighbouring text",
			trim(in.Options[idx].Text, 110))
	}

	// --- 3 and 4. a valid answer that matches a choice -------------------
	correct := correctIndexes(in.Options)
	answerText := strings.TrimSpace(in.AnswerText)

	switch {
	case objective && len(correct) == 0 && answerText == "":
		add(CodeNoAnswer, models.SeverityCritical, "answer",
			"no correct choice is marked and no answer is recorded", "")
	case objective && len(correct) > 1 && in.Type != models.TypeMultiSelect:
		add(CodeMultipleAnswers, models.SeverityCritical, "answer",
			fmt.Sprintf("%d choices are marked correct for a single-answer question", len(correct)), "")
	case !objective && answerText == "" && len(correct) == 0:
		add(CodeNoAnswer, models.SeverityCritical, "answer",
			"this question has no recorded answer", "")
	}

	// When an answer string and choices both exist, the string has to point at
	// one of them. Otherwise the paper and its key disagree.
	if answerText != "" && filled > 0 && len(correct) == 0 {
		if !answerMatchesOption(answerText, in.Options) {
			add(CodeAnswerNotAnOption, models.SeverityCritical, "answer",
				"the recorded answer does not match any of the choices", trim(answerText, 80))
		}
	}

	// --- 5. explanation consistent with the answer -----------------------
	if explanation := strings.TrimSpace(in.Explanation); explanation != "" && len(correct) == 1 {
		if claimed, found := verdictFromExplanation(explanation, len(in.Options)); found && claimed != correct[0] {
			add(CodeExplanationMismatch, models.SeverityCritical, "explanation",
				fmt.Sprintf("the explanation says choice %d is correct but choice %d is marked",
					claimed+1, correct[0]+1),
				trim(explanation, 110))
		}
	}

	// --- 6, 7. placeholders and corrupted formatting ---------------------
	// A defect in the stem or a choice is critical because a student reads them.
	// The same defect in an explanation is major: the question still works.
	for _, f := range inspectText(stem) {
		issues = append(issues, gradeCorruption(f, "stem", models.SeverityCritical))
	}
	for i, opt := range in.Options {
		field := fmt.Sprintf("option:%d", i+1)
		for _, f := range inspectText(opt.Text) {
			issues = append(issues, gradeCorruption(f, field, models.SeverityCritical))
		}
	}
	for _, f := range inspectText(in.Explanation) {
		issues = append(issues, gradeCorruption(f, "explanation", models.SeverityMajor))
	}
	for _, f := range inspectText(in.PassageText) {
		issues = append(issues, gradeCorruption(f, "passage", models.SeverityCritical))
	}

	// --- 8. no unrelated text --------------------------------------------
	for i, opt := range in.Options {
		field := fmt.Sprintf("option:%d", i+1)
		if evidence, bad := hasForeignContent(opt.Text); bad {
			add(CodeForeignContent, models.SeverityCritical, field,
				"this choice contains what looks like other choices or another question", evidence)
		}
		if evidence, bad := hasEmbeddedQuestion(opt.Text); bad {
			add(CodeEmbeddedQuestion, models.SeverityCritical, field,
				"this choice contains a complete second question", evidence)
		}
	}
	if trailing := strings.TrimSpace(in.TrailingText); trailing != "" {
		add(CodeTrailingText, models.SeverityMajor, "",
			"text followed this question and could not be placed; check nothing is missing",
			trim(trailing, 130))
	}

	// A question whose choices point into a list the stem does not contain is
	// unanswerable, however clean it otherwise looks.
	if referencesEnumerationSet(in.Options) && !stemHasEnumeration(stem) && in.PassageText == "" {
		add(CodeMissingEnumeration, models.SeverityCritical, "stem",
			"the choices refer to numbered items that are not present in the question", trim(stem, 110))
	}

	// The same failure from the other direction: a stem that names an indexed item
	// living in material it does not have. A cloze question asking for "blank
	// number 5" cannot be answered without the passage that holds blank 5, and it
	// is also indistinguishable from every other question in its set.
	if strings.TrimSpace(in.PassageText) == "" {
		if evidence, external := referencesExternalMaterial(stem); external &&
			!strings.Contains(stem, "___") && !stemHasEnumeration(stem) {
			add(CodeMissingReference, models.SeverityCritical, "stem",
				"the question refers to material that is not attached to it", evidence)
		}
	}

	// --- 10. classification ----------------------------------------------
	if in.SubjectCode != "" && strings.EqualFold(in.SubjectCode, opts.UnsortedSubjectCode) {
		add(CodeUnclassifiedSubject, models.SeverityMajor, "subject",
			"this question was never matched to a subject; add the section heading as a subject alias and re-parse", "")
	}

	// --- 11. extraction confidence and parser findings -------------------
	if in.ExtractionConfidence > 0 && in.ExtractionConfidence < opts.MinExtractionConfidence {
		add(CodeLowExtraction, models.SeverityMajor, "",
			fmt.Sprintf("the extractor scored this question %.2f, below the %.2f bar",
				in.ExtractionConfidence, opts.MinExtractionConfidence), "")
	}
	issues = append(issues, fromExtractIssues(in.ExtractIssues)...)

	// --- comprehension questions need their passage ----------------------
	if in.Type == models.TypeComprehension {
		passage := strings.TrimSpace(in.PassageText)
		switch {
		case passage == "":
			add(CodePassageMissing, models.SeverityCritical, "passage",
				"this is a comprehension question but its passage is missing", "")
		case wordCount(passage) < 30:
			add(CodePassageTooShort, models.SeverityMajor, "passage",
				fmt.Sprintf("the passage is only %d words, which is too short to answer from", wordCount(passage)), "")
		}
	}

	// --- model review gap -------------------------------------------------
	if opts.ModelExpected && !in.ModelReviewed {
		add(CodeModelNotRun, models.SeverityMinor, "",
			"model review is configured but has not run for this question", "")
	}

	return dedupeIssues(issues)
}

// gradeCorruption turns a text finding into a graded issue.
//
// Severity comes from which field is affected, not from the kind of corruption.
// Anything a student reads - the stem, the choices, the passage - is critical,
// because a hole in it makes the question wrong. The same defect in an
// explanation is major: the question still works, but that explanation must not
// be published as it stands.
func gradeCorruption(f finding, field string, severity models.Severity) models.QualityIssue {
	return models.QualityIssue{
		Code:     f.code,
		Severity: severity,
		Source:   models.SourceRules,
		Field:    field,
		Message:  f.message,
		Evidence: f.evidence,
	}
}

// fromExtractIssues lifts the parser's findings into the same vocabulary, so a
// reviewer sees one list rather than two.
func fromExtractIssues(list []extract.ExtractIssue) models.QualityIssues {
	var out models.QualityIssues
	for _, issue := range list {
		severity := models.SeverityMajor
		switch issue.Code {
		case extract.IssueUnreadableSource, extract.IssueNoOptions:
			severity = models.SeverityCritical
		case extract.IssueContextCapped:
			severity = models.SeverityMinor
		}
		message := issue.Detail
		if message == "" {
			message = "the extractor recorded: " + issue.Code
		}
		out = append(out, models.QualityIssue{
			Code:     issue.Code,
			Severity: severity,
			Source:   models.SourceExtraction,
			Message:  message,
		})
	}
	return out
}

// dedupeIssues collapses repeats of the same code and field, keeping the most
// severe. Several checks can notice the same underlying defect.
func dedupeIssues(list models.QualityIssues) models.QualityIssues {
	type key struct{ code, field string }
	index := map[key]int{}
	out := make(models.QualityIssues, 0, len(list))
	for _, issue := range list {
		k := key{issue.Code, issue.Field}
		if at, seen := index[k]; seen {
			if issue.Severity.Rank() < out[at].Severity.Rank() {
				out[at] = issue
			}
			continue
		}
		index[k] = len(out)
		out = append(out, issue)
	}
	return out
}

// Score turns a verdict into a 0..1 ranking number for the review queue. It is
// deliberately not what decides the gate: severity does, because a single
// critical defect cannot be averaged away by ten clean fields.
func Score(issues models.QualityIssues, extractionConfidence float64) float64 {
	score := 1.0
	if extractionConfidence > 0 {
		// Extraction quality is a floor on content quality, weighted lightly.
		score = 0.8 + 0.2*clamp01(extractionConfidence)
	}
	for _, issue := range issues {
		switch issue.Severity {
		case models.SeverityCritical:
			score -= 0.34
		case models.SeverityMajor:
			score -= 0.12
		case models.SeverityMinor:
			score -= 0.03
		}
	}
	return clamp01(score)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// --- helpers --------------------------------------------------------------

func isObjective(t models.QuestionType) bool {
	switch t {
	case models.TypeDescriptive, models.TypeNumeric:
		return false
	}
	return true
}

func filledOptions(options []OptionInput) int {
	n := 0
	for _, o := range options {
		if strings.TrimSpace(o.Text) != "" {
			n++
		}
	}
	return n
}

func correctIndexes(options []OptionInput) []int {
	var out []int
	for i, o := range options {
		if o.IsCorrect && strings.TrimSpace(o.Text) != "" {
			out = append(out, i)
		}
	}
	return out
}

// isLabelOnly reports whether a choice is nothing but a marker, such as "(3)"
// or "b.".
func isLabelOnly(text string) bool {
	trimmed := strings.TrimSpace(text)
	stripped := strings.Trim(trimmed, "().[]{} \t")
	if stripped == "" {
		return true
	}
	if len([]rune(stripped)) > 2 {
		return false
	}
	for _, r := range stripped {
		if !isASCIIAlnum(r) {
			return false
		}
	}
	// Wrapping punctuation is what makes it a marker rather than a short answer:
	// "(3)" is a label, "3" may well be the value the question asks for.
	return len([]rune(trimmed)) != len([]rune(stripped))
}

// labelOnlyCount counts non-empty choices that are nothing but markers.
func labelOnlyCount(options []OptionInput) int {
	n := 0
	for _, o := range options {
		if strings.TrimSpace(o.Text) == "" {
			continue
		}
		if isLabelOnly(o.Text) {
			n++
		}
	}
	return n
}

// endsDeliberately reports whether a stem trails off on purpose, inviting the
// choices to finish it.
func endsDeliberately(stem string) bool {
	trimmed := strings.TrimRight(stem, " \t")
	if trimmed == "" {
		return false
	}
	for _, suffix := range []string{":", "?", "\u2026", "...", "_", "-", ",", "="} {
		if strings.HasSuffix(trimmed, suffix) {
			return true
		}
	}
	return false
}

func isASCIIAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// lengthOutlier finds a choice implausibly longer than its siblings.
func lengthOutlier(options []OptionInput) (bool, int) {
	lengths := make([]int, 0, len(options))
	for _, o := range options {
		if n := len([]rune(strings.TrimSpace(o.Text))); n > 0 {
			lengths = append(lengths, n)
		}
	}
	if len(lengths) < 3 {
		return false, 0
	}
	sorted := append([]int(nil), lengths...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	median := sorted[len(sorted)/2]
	if median < 8 {
		median = 8
	}
	for i, o := range options {
		n := len([]rune(strings.TrimSpace(o.Text)))
		if n > median*4 && n-median > 60 {
			return true, i
		}
	}
	return false, 0
}

// answerMatchesOption reports whether an answer string identifies a choice,
// either by repeating its text or by naming its label.
func answerMatchesOption(answer string, options []OptionInput) bool {
	want := normalizeForCompare(answer)
	if want == "" {
		return true
	}
	for _, o := range options {
		if normalizeForCompare(o.Text) == want {
			return true
		}
		if label := normalizeForCompare(o.Label); label != "" && label == want {
			return true
		}
	}
	// A bare index such as "3" or "C" also identifies a choice.
	if len(want) == 1 {
		r := rune(want[0])
		switch {
		case r >= '1' && r <= '9':
			return int(r-'1') < len(options)
		case r >= 'a' && r <= 'h':
			return int(r-'a') < len(options)
		}
	}
	return false
}

// verdictFromExplanation extracts which choice an explanation claims is correct.
func verdictFromExplanation(explanation string, optionCount int) (int, bool) {
	m := explanationVerdictRe.FindStringSubmatch(explanation)
	if m == nil {
		return 0, false
	}
	token := strings.ToLower(m[1])
	r := rune(token[0])
	var idx int
	switch {
	case r >= '1' && r <= '9':
		idx = int(r - '1')
	case r >= 'a' && r <= 'h':
		idx = int(r - 'a')
	default:
		return 0, false
	}
	if idx < 0 || idx >= optionCount {
		return 0, false
	}
	return idx, true
}

// referencesEnumerationSet reports whether most choices point into a list.
//
// Requiring a majority rather than a single match matters: one option reading
// "1 and 2" can be a coincidence, but three of four is the signature of a
// statement-based question whose statements went missing.
func referencesEnumerationSet(options []OptionInput) bool {
	filled, hits := 0, 0
	for _, o := range options {
		text := strings.TrimSpace(o.Text)
		if text == "" {
			continue
		}
		filled++
		if referencesEnumeration(text) {
			hits++
		}
	}
	return filled >= 3 && hits*2 > filled
}

// lastWords returns the final n words of a string.
func lastWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) <= n {
		return s
	}
	return strings.Join(fields[len(fields)-n:], " ")
}
