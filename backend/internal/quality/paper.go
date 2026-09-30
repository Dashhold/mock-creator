package quality

import (
	"fmt"
	"math"
	"strings"

	"mockcreator/internal/models"
)

// Paper-level defect codes.
const (
	CodePaperEmpty             = "paper_empty"
	CodePaperCountMismatch     = "paper_question_count_mismatch"
	CodePaperMarksMismatch     = "paper_marks_mismatch"
	CodePaperNegativeMarks     = "paper_negative_marks_wrong"
	CodePaperDurationMissing   = "paper_duration_missing"
	CodePaperSectionMissing    = "paper_section_missing"
	CodePaperSectionCount      = "paper_section_count_mismatch"
	CodePaperSubjectDrift      = "paper_subject_distribution_off"
	CodePaperDifficultyDrift   = "paper_difficulty_distribution_off"
	CodePaperDuplicateQuestion = "paper_duplicate_question"
	CodePaperRepeatedStem      = "paper_repeated_stem"
	CodePaperUnvettedQuestion  = "paper_unvetted_question"
	CodePaperFailedQuestion    = "paper_failed_question"
	CodePaperMissingAnswer     = "paper_missing_answer"
	CodePaperCorruptQuestion   = "paper_corrupt_question"
	CodePaperPlaceholder       = "paper_placeholder"
	CodePaperBrokenPassage     = "paper_broken_passage"
	CodePaperAnswerMismatch    = "paper_answer_explanation_mismatch"
	CodePaperAnswerKeySkew     = "paper_answer_key_skewed"
	CodePaperSequenceGap       = "paper_sequence_gap"
	CodePaperDifficultyUnrated = "paper_difficulty_unrated"
)

// PaperOptions is the target the paper is checked against.
//
// Everything here comes from the exam's pattern, not from any exam-specific
// knowledge: the checks compare what was built against what was asked for.
type PaperOptions struct {
	ExpectedQuestions int
	ExpectedMarks     float64
	ExpectedNegative  float64
	ExpectedDuration  int
	// ExpectedSections maps a section name to the number of questions it should
	// hold. Empty skips section checks.
	ExpectedSections map[string]int
	// ExpectedDifficulty is the requested mix in percent. Zero values skip the
	// distribution check.
	ExpectedDifficulty models.DifficultyMix
	// DifficultyTolerance is how far a section may drift, in percentage points.
	DifficultyTolerance int
	// CountTolerance is how many questions a section may be short before it
	// counts as a defect rather than a note.
	CountTolerance int
	// DifficultyRated records whether the warehouse actually carries trustworthy
	// difficulty labels. When it does not, the distribution check is reported as
	// unenforceable instead of being silently declared a pass.
	DifficultyRated bool
	// ModelExpected records that a final model review was configured.
	ModelExpected bool
	// ModelRan records whether it actually ran.
	ModelRan bool
}

// WithDefaults fills sensible tolerances.
func (o PaperOptions) WithDefaults() PaperOptions {
	if o.DifficultyTolerance <= 0 {
		o.DifficultyTolerance = 15
	}
	return o
}

// PaperItem is one question as placed in the paper.
type PaperItem struct {
	SequenceNo  int
	SectionName string
	SubjectID   uint
	SubjectName string
	QuestionID  uint

	Stem        string
	Options     []OptionInput
	AnswerText  string
	Explanation string
	PassageID   uint
	PassageText string
	Type        models.QuestionType
	Difficulty  models.Difficulty
	// DifficultyConfidence is how much the difficulty label can be trusted.
	DifficultyConfidence float64

	QualityStatus models.QualityStatus
	HasAnswer     bool
	Marks         float64
	NegativeMarks float64
}

// ValidatePaper checks an assembled paper end to end.
//
// This runs over the finished paper rather than over its questions, because the
// two fail differently. Every question can be individually sound while the paper
// is still wrong: the same question placed twice, a section holding the wrong
// subject, marks that do not add up to what the exam promises. Those are the
// defects a client notices first, and none of them are visible one question at a
// time.
func ValidatePaper(items []PaperItem, paper models.TestPaper, opts PaperOptions) models.QualityIssues {
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

	if len(items) == 0 {
		add(CodePaperEmpty, models.SeverityCritical, "", "the paper contains no questions", "")
		return issues
	}

	// --- 1. correct question count ---------------------------------------
	if opts.ExpectedQuestions > 0 && len(items) != opts.ExpectedQuestions {
		severity := models.SeverityCritical
		shortfall := opts.ExpectedQuestions - len(items)
		if shortfall > 0 && shortfall <= opts.CountTolerance {
			severity = models.SeverityMajor
		}
		add(CodePaperCountMismatch, severity, "",
			fmt.Sprintf("the paper holds %d questions but the pattern asks for %d",
				len(items), opts.ExpectedQuestions), "")
	}

	// --- 2. correct sections ---------------------------------------------
	bySection := map[string]int{}
	bySubject := map[string]int{}
	sectionSubjects := map[string]map[string]int{}
	for _, item := range items {
		bySection[item.SectionName]++
		bySubject[item.SubjectName]++
		if sectionSubjects[item.SectionName] == nil {
			sectionSubjects[item.SectionName] = map[string]int{}
		}
		sectionSubjects[item.SectionName][item.SubjectName]++
	}

	for name, want := range opts.ExpectedSections {
		got := bySection[name]
		switch {
		case got == 0:
			add(CodePaperSectionMissing, models.SeverityCritical, "section:"+name,
				fmt.Sprintf("section %q is missing entirely; %d questions were expected", name, want), "")
		case got != want:
			severity := models.SeverityMajor
			if want-got > opts.CountTolerance || got > want {
				severity = models.SeverityCritical
			}
			add(CodePaperSectionCount, severity, "section:"+name,
				fmt.Sprintf("section %q holds %d questions but %d were expected", name, got, want), "")
		}
	}

	// A section drawing from more than one subject means the pattern and the
	// selection disagree about what that section is.
	for name, subjects := range sectionSubjects {
		if len(subjects) <= 1 {
			continue
		}
		names := make([]string, 0, len(subjects))
		for subject, count := range subjects {
			names = append(names, fmt.Sprintf("%s (%d)", subject, count))
		}
		add(CodePaperSubjectDrift, models.SeverityMajor, "section:"+name,
			fmt.Sprintf("section %q mixes %d subjects", name, len(subjects)),
			strings.Join(names, ", "))
	}

	// --- 3 and 4. marks and negative marking -----------------------------
	var marks, negative float64
	inconsistentNegative := map[float64]int{}
	for _, item := range items {
		marks += item.Marks
		negative += item.NegativeMarks
		inconsistentNegative[item.NegativeMarks]++
	}
	if opts.ExpectedMarks > 0 && math.Abs(marks-opts.ExpectedMarks) > 0.01 {
		add(CodePaperMarksMismatch, models.SeverityCritical, "",
			fmt.Sprintf("the questions total %.2f marks but the paper declares %.2f",
				marks, opts.ExpectedMarks), "")
	}
	if math.Abs(paper.TotalMarks-marks) > 0.01 {
		add(CodePaperMarksMismatch, models.SeverityCritical, "",
			fmt.Sprintf("the paper header says %.2f marks but its questions total %.2f",
				paper.TotalMarks, marks), "")
	}
	if opts.ExpectedNegative > 0 && len(inconsistentNegative) > 1 {
		add(CodePaperNegativeMarks, models.SeverityMajor, "",
			fmt.Sprintf("negative marking is not uniform across the paper (%d different values)",
				len(inconsistentNegative)), "")
	}
	if opts.ExpectedNegative > 0 && paper.NegativeMarks <= 0 {
		add(CodePaperNegativeMarks, models.SeverityCritical, "",
			fmt.Sprintf("the pattern applies a %.2f penalty but the paper records none",
				opts.ExpectedNegative), "")
	}

	// --- 5. duration -----------------------------------------------------
	if paper.DurationMin <= 0 {
		severity := models.SeverityMajor
		if opts.ExpectedDuration > 0 {
			severity = models.SeverityCritical
		}
		add(CodePaperDurationMissing, severity, "",
			"the paper has no duration, so it cannot be timed", "")
	} else if opts.ExpectedDuration > 0 && paper.DurationMin != opts.ExpectedDuration {
		add(CodePaperDurationMissing, models.SeverityMajor, "",
			fmt.Sprintf("the paper runs %d minutes but the pattern says %d",
				paper.DurationMin, opts.ExpectedDuration), "")
	}

	// --- 6. difficulty distribution --------------------------------------
	if opts.ExpectedDifficulty.Easy+opts.ExpectedDifficulty.Medium+opts.ExpectedDifficulty.Hard > 0 {
		if !opts.DifficultyRated {
			// Honesty about what cannot be checked. Extraction cannot judge
			// hardness, so a paper built from unrated questions has no meaningful
			// difficulty mix and saying it passed would be a lie.
			add(CodePaperDifficultyUnrated, models.SeverityMajor, "",
				"the requested difficulty mix could not be honoured or verified because the "+
					"questions carry no rated difficulty; have a reviewer or a model rate them first", "")
		} else {
			actual := map[models.Difficulty]int{}
			for _, item := range items {
				actual[item.Difficulty]++
			}
			want := opts.ExpectedDifficulty.Normalized()
			checkShare := func(label string, got, target int) {
				share := got * 100 / len(items)
				if abs(share-target) > opts.DifficultyTolerance {
					add(CodePaperDifficultyDrift, models.SeverityMajor, "",
						fmt.Sprintf("%s questions are %d%% of the paper but %d%% was requested",
							label, share, target), "")
				}
			}
			checkShare("easy", actual[models.DifficultyEasy], want.Easy)
			checkShare("medium", actual[models.DifficultyMedium], want.Medium)
			checkShare("hard", actual[models.DifficultyHard], want.Hard)
		}
	}

	// --- 7. duplicates ---------------------------------------------------
	seenQuestion := map[uint]int{}
	seenStem := map[string]int{}
	for _, item := range items {
		if first, dup := seenQuestion[item.QuestionID]; dup {
			add(CodePaperDuplicateQuestion, models.SeverityCritical,
				fmt.Sprintf("q%d", item.SequenceNo),
				fmt.Sprintf("question %d appears twice, at positions %d and %d",
					item.QuestionID, first, item.SequenceNo), trim(item.Stem, 110))
		} else {
			seenQuestion[item.QuestionID] = item.SequenceNo
		}

		// The key includes the shared passage. A cloze question's stem is
		// deliberately generic ("fill in blank number 5") and carries no meaning
		// without the passage it belongs to, so comparing stems alone reports two
		// perfectly distinct questions as the same one.
		key := normalizeForCompare(item.Stem)
		if key == "" {
			continue
		}
		if item.PassageID != 0 {
			key = fmt.Sprintf("p%d|%s", item.PassageID, key)
		} else if item.PassageText != "" {
			key = normalizeForCompare(item.PassageText) + "|" + key
		}
		if first, dup := seenStem[key]; dup {
			add(CodePaperRepeatedStem, models.SeverityCritical,
				fmt.Sprintf("q%d", item.SequenceNo),
				fmt.Sprintf("this question is worded identically to position %d", first),
				trim(item.Stem, 110))
		} else {
			seenStem[key] = item.SequenceNo
		}
	}

	// --- 8, 9, 10, 11. per-question integrity ----------------------------
	passages := map[uint]int{}
	for _, item := range items {
		field := fmt.Sprintf("q%d", item.SequenceNo)

		switch item.QualityStatus {
		case models.QualityPass:
			// nothing to say
		case models.QualityFailed:
			add(CodePaperFailedQuestion, models.SeverityCritical, field,
				"this question failed the quality checks and must not be delivered",
				trim(item.Stem, 110))
		default:
			add(CodePaperUnvettedQuestion, models.SeverityCritical, field,
				fmt.Sprintf("this question is %s, so it is not cleared for delivery", item.QualityStatus),
				trim(item.Stem, 110))
		}

		if !item.HasAnswer && len(correctIndexes(item.Options)) == 0 &&
			strings.TrimSpace(item.AnswerText) == "" {
			add(CodePaperMissingAnswer, models.SeverityCritical, field,
				"this question has no answer, so it cannot be marked", trim(item.Stem, 110))
		}

		// Corruption is re-checked at paper level. A question may have been stored
		// before a rule existed, and the paper is the last chance to catch it.
		for _, f := range inspectText(item.Stem) {
			code := CodePaperCorruptQuestion
			if f.code == CodePlaceholder {
				code = CodePaperPlaceholder
			}
			add(code, models.SeverityCritical, field,
				"the question text is corrupted: "+f.message, f.evidence)
		}
		for i, opt := range item.Options {
			for _, f := range inspectText(opt.Text) {
				add(CodePaperCorruptQuestion, models.SeverityCritical,
					fmt.Sprintf("%s option:%d", field, i+1),
					"a choice is corrupted: "+f.message, f.evidence)
			}
		}

		// Explanation must not contradict the answer.
		correct := correctIndexes(item.Options)
		if len(correct) == 1 && strings.TrimSpace(item.Explanation) != "" {
			if claimed, found := verdictFromExplanation(item.Explanation, len(item.Options)); found && claimed != correct[0] {
				add(CodePaperAnswerMismatch, models.SeverityCritical, field,
					fmt.Sprintf("the explanation argues for choice %d but choice %d is marked correct",
						claimed+1, correct[0]+1), trim(item.Explanation, 110))
			}
		}

		// Comprehension questions need their passage present.
		if item.Type == models.TypeComprehension || item.PassageID != 0 {
			if strings.TrimSpace(item.PassageText) == "" {
				add(CodePaperBrokenPassage, models.SeverityCritical, field,
					"this question refers to a passage that is not attached to the paper",
					trim(item.Stem, 110))
			} else {
				passages[item.PassageID]++
			}
		}
	}

	// --- 12. answer key shape --------------------------------------------
	if skew, label := answerKeySkew(items); skew {
		add(CodePaperAnswerKeySkew, models.SeverityMajor, "",
			"the answer key is lopsided enough for a student to guess from it", label)
	}

	// --- 13. sequence integrity ------------------------------------------
	for i, item := range items {
		if item.SequenceNo != i+1 {
			add(CodePaperSequenceGap, models.SeverityMajor, fmt.Sprintf("q%d", item.SequenceNo),
				fmt.Sprintf("question numbering jumps: position %d is numbered %d", i+1, item.SequenceNo), "")
			break
		}
	}

	// --- 14. model review gap --------------------------------------------
	if opts.ModelExpected && !opts.ModelRan {
		add(CodeModelNotRun, models.SeverityMajor, "",
			"the final model review is configured but did not run for this paper, "+
				"so only the structural checks have been applied", "")
	}

	return dedupeIssues(issues)
}

// answerKeySkew reports whether correct answers cluster on one position.
//
// A key where two thirds of the answers are option 3 is guessable, which matters
// commercially: it is the first thing a coaching institute notices.
func answerKeySkew(items []PaperItem) (bool, string) {
	counts := map[int]int{}
	total := 0
	for _, item := range items {
		correct := correctIndexes(item.Options)
		if len(correct) != 1 {
			continue
		}
		counts[correct[0]]++
		total++
	}
	// Below this there is not enough data for a share to mean anything.
	if total < 20 {
		return false, ""
	}
	positions := len(counts)
	if positions < 2 {
		return true, fmt.Sprintf("every answer is in the same position across %d questions", total)
	}
	expected := 100 / positions
	for position, count := range counts {
		share := count * 100 / total
		// Allow a generous margin: real papers are never perfectly uniform.
		if share > expected*2 || (positions >= 4 && share > 45) {
			return true, fmt.Sprintf("choice %d is the answer to %d%% of the %d questions with a single answer",
				position+1, share, total)
		}
	}
	return false, ""
}

// PaperScore turns a paper verdict into a 0..1 number for display.
func PaperScore(issues models.QualityIssues, questionCount int) float64 {
	if questionCount == 0 {
		return 0
	}
	score := 1.0
	for _, issue := range issues {
		switch issue.Severity {
		case models.SeverityCritical:
			score -= 0.2
		case models.SeverityMajor:
			score -= 0.06
		case models.SeverityMinor:
			score -= 0.015
		}
	}
	return clamp01(score)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
