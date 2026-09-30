package quality

import (
	"fmt"
	"testing"

	"mockcreator/internal/models"
)

// These tests cover the defects a paper can have even when every question in it
// is individually sound: the same question twice, a section holding the wrong
// subject, marks that do not add up, an answer key a student could guess from.
// None of them are visible one question at a time, which is why the paper is
// checked as a whole before it can be delivered.

func paperItem(seq int, section, subject string, stem string, correct int) PaperItem {
	item := PaperItem{
		SequenceNo:           seq,
		SectionName:          section,
		SubjectName:          subject,
		SubjectID:            1,
		QuestionID:           uint(1000 + seq),
		Stem:                 stem,
		Type:                 models.TypeMCQ,
		Difficulty:           models.DifficultyMedium,
		DifficultyConfidence: 1,
		QualityStatus:        models.QualityPass,
		HasAnswer:            true,
		Marks:                2,
		NegativeMarks:        0.5,
	}
	labels := []string{"1", "2", "3", "4"}
	for i := range labels {
		item.Options = append(item.Options, OptionInput{
			Label:     labels[i],
			Text:      fmt.Sprintf("%s choice %d", stem[:6], i+1),
			IsCorrect: i == correct,
		})
	}
	return item
}

// soundPaper builds a paper that should pass every structural check, with the
// answer key spread across all four positions.
func soundPaper(n int) ([]PaperItem, models.TestPaper, PaperOptions) {
	items := make([]PaperItem, 0, n)
	for i := 1; i <= n; i++ {
		section := "Reasoning"
		subject := "Reasoning"
		if i > n/2 {
			section = "Quantitative Aptitude"
			subject = "Quantitative Aptitude"
		}
		items = append(items, paperItem(i, section, subject,
			fmt.Sprintf("Question %02d asks something specific and answerable?", i), i%4))
	}
	paper := models.TestPaper{
		Title:          "Mock Paper 1",
		DurationMin:    60,
		TotalQuestions: n,
		TotalMarks:     float64(n) * 2,
		NegativeMarks:  0.5,
	}
	opts := PaperOptions{
		ExpectedQuestions: n,
		ExpectedMarks:     float64(n) * 2,
		ExpectedNegative:  0.5,
		ExpectedDuration:  60,
		ExpectedSections: map[string]int{
			"Reasoning":             n / 2,
			"Quantitative Aptitude": n - n/2,
		},
		DifficultyRated: true,
	}
	return items, paper, opts
}

func TestSoundPaperPasses(t *testing.T) {
	items, paper, opts := soundPaper(40)
	issues := ValidatePaper(items, paper, opts)
	if status := issues.Worst(); status != models.QualityPass {
		t.Errorf("expected a pass, got %s with [%s]", status, codes(issues))
	}
	if !paperWith(issues).Publishable() {
		t.Error("a clean paper should be publishable")
	}
}

func paperWith(issues models.QualityIssues) models.TestPaper {
	var paper models.TestPaper
	paper.Apply(issues, PaperScore(issues, 40), RulesVersion)
	return paper
}

func TestPaperDefectsBlockPublication(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		mutate func(*[]PaperItem, *models.TestPaper, *PaperOptions)
	}{
		{
			name: "the same question placed twice",
			code: CodePaperDuplicateQuestion,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[10].QuestionID = (*items)[3].QuestionID
			},
		},
		{
			name: "two questions worded identically",
			code: CodePaperRepeatedStem,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[12].Stem = (*items)[5].Stem
			},
		},
		{
			name: "a question that has not passed its own checks",
			code: CodePaperUnvettedQuestion,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[7].QualityStatus = models.QualityNeedsReview
			},
		},
		{
			name: "a question that failed its own checks",
			code: CodePaperFailedQuestion,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[7].QualityStatus = models.QualityFailed
			},
		},
		{
			name: "a question nobody validated at all",
			code: CodePaperUnvettedQuestion,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[2].QualityStatus = models.QualityUnchecked
			},
		},
		{
			name: "a question with no answer",
			code: CodePaperMissingAnswer,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[4].HasAnswer = false
				for i := range (*items)[4].Options {
					(*items)[4].Options[i].IsCorrect = false
				}
			},
		},
		{
			name: "marks that do not add up",
			code: CodePaperMarksMismatch,
			mutate: func(_ *[]PaperItem, paper *models.TestPaper, _ *PaperOptions) {
				paper.TotalMarks = 999
			},
		},
		{
			name: "fewer questions than the pattern asks for",
			code: CodePaperCountMismatch,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				*items = (*items)[:30]
			},
		},
		{
			name: "a missing section",
			code: CodePaperSectionMissing,
			mutate: func(_ *[]PaperItem, _ *models.TestPaper, opts *PaperOptions) {
				opts.ExpectedSections["English"] = 25
			},
		},
		{
			name: "corrupted question text",
			code: CodePaperCorruptQuestion,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[9].Stem = "Simplify: 22 \ufffd 1.9 and then say what the answer is?"
			},
		},
		{
			name: "an unresolved placeholder",
			code: CodePaperPlaceholder,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[9].Stem = "What percentage is covered? <!-- formula-not-decoded -->"
			},
		},
		{
			name: "a passage the paper does not carry",
			code: CodePaperBrokenPassage,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[15].Type = models.TypeComprehension
				(*items)[15].PassageID = 77
				(*items)[15].PassageText = ""
			},
		},
		{
			name: "an explanation that contradicts the marked answer",
			code: CodePaperAnswerMismatch,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				(*items)[6].Explanation = "Option (1) is correct because of the reasoning above."
				for i := range (*items)[6].Options {
					(*items)[6].Options[i].IsCorrect = i == 2
				}
			},
		},
		{
			name: "an empty paper",
			code: CodePaperEmpty,
			mutate: func(items *[]PaperItem, _ *models.TestPaper, _ *PaperOptions) {
				*items = nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, paper, opts := soundPaper(40)
			tc.mutate(&items, &paper, &opts)

			issues := ValidatePaper(items, paper, opts)
			if !hasCode(issues, tc.code) {
				t.Fatalf("expected %s, got [%s]", tc.code, codes(issues))
			}
			if severityOf(issues, tc.code) != models.SeverityCritical {
				t.Errorf("%s should be critical, got %s", tc.code, severityOf(issues, tc.code))
			}
			if issues.Worst() != models.QualityFailed {
				t.Errorf("a critical paper defect must fail the paper, got %s", issues.Worst())
			}
			if paperWith(issues).Publishable() {
				t.Error("a failed paper must not be publishable")
			}
		})
	}
}

// A lopsided answer key is the first thing a coaching institute notices, so it is
// reported even though every question is individually fine.
func TestLopsidedAnswerKeyIsReported(t *testing.T) {
	items, paper, opts := soundPaper(40)
	// Put almost every answer in the same position.
	for i := range items {
		for j := range items[i].Options {
			items[i].Options[j].IsCorrect = j == 2
		}
	}

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodePaperAnswerKeySkew) {
		t.Fatalf("a one-position answer key should be reported, got [%s]", codes(issues))
	}
	if status := issues.Worst(); status != models.QualityNeedsReview {
		t.Errorf("expected review, got %s", status)
	}
}

// A short paper cannot support a meaningful claim about answer-key balance, so
// the check stays quiet rather than guessing.
func TestAnswerKeySkewNeedsEnoughQuestions(t *testing.T) {
	items, paper, opts := soundPaper(8)
	opts.ExpectedQuestions = 8
	opts.ExpectedMarks = 16
	opts.ExpectedSections = map[string]int{"Reasoning": 4, "Quantitative Aptitude": 4}
	paper.TotalQuestions = 8
	paper.TotalMarks = 16

	for i := range items {
		for j := range items[i].Options {
			items[i].Options[j].IsCorrect = j == 0
		}
	}

	if issues := ValidatePaper(items, paper, opts); hasCode(issues, CodePaperAnswerKeySkew) {
		t.Error("eight questions are too few to judge answer-key balance")
	}
}

// A requested difficulty mix cannot be verified when the questions carry no rated
// difficulty. Saying so is the honest outcome; silently passing would claim a
// guarantee the data cannot support.
func TestUnratedDifficultyIsReportedNotFaked(t *testing.T) {
	items, paper, opts := soundPaper(40)
	for i := range items {
		items[i].DifficultyConfidence = 0
	}
	opts.DifficultyRated = false
	opts.ExpectedDifficulty = models.DifficultyMix{Easy: 30, Medium: 50, Hard: 20}

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodePaperDifficultyUnrated) {
		t.Fatalf("an unverifiable difficulty mix must be reported, got [%s]", codes(issues))
	}
	if issues.Worst() == models.QualityPass {
		t.Error("a paper whose difficulty mix could not be honoured should not pass silently")
	}
}

// A difficulty mix that is rated and badly wrong is reported as drift.
func TestDifficultyDriftIsReported(t *testing.T) {
	items, paper, opts := soundPaper(40)
	for i := range items {
		items[i].Difficulty = models.DifficultyHard
	}
	opts.ExpectedDifficulty = models.DifficultyMix{Easy: 30, Medium: 50, Hard: 20}

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodePaperDifficultyDrift) {
		t.Fatalf("an all-hard paper should not satisfy a balanced mix, got [%s]", codes(issues))
	}
}

// A section drawing from two subjects means the pattern and the selection
// disagree about what that section is.
func TestSectionMixingSubjectsIsReported(t *testing.T) {
	items, paper, opts := soundPaper(40)
	items[3].SubjectName = "English"

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodePaperSubjectDrift) {
		t.Fatalf("a section mixing subjects should be reported, got [%s]", codes(issues))
	}
}

// A configured final review that did not run is reported, so a paper checked only
// by the rules is never presented as one a model also read.
func TestMissingPaperModelReviewIsReported(t *testing.T) {
	items, paper, opts := soundPaper(40)
	opts.ModelExpected = true
	opts.ModelRan = false

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodeModelNotRun) {
		t.Fatalf("the missing final review must be reported, got [%s]", codes(issues))
	}
	if issues.Worst() != models.QualityNeedsReview {
		t.Errorf("expected review, got %s", issues.Worst())
	}

	opts.ModelRan = true
	if issues := ValidatePaper(items, paper, opts); hasCode(issues, CodeModelNotRun) {
		t.Error("a reviewed paper should not report the gap")
	}
}

// A paper with no duration cannot be timed, and one that disagrees with its
// pattern is worth reporting.
func TestDurationIsChecked(t *testing.T) {
	items, paper, opts := soundPaper(40)
	paper.DurationMin = 0

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodePaperDurationMissing) {
		t.Fatalf("a paper with no duration must be reported, got [%s]", codes(issues))
	}
	if severityOf(issues, CodePaperDurationMissing) != models.SeverityCritical {
		t.Error("a pattern that specifies a duration makes a missing one critical")
	}
}

// Negative marking must be uniform and must be present when the pattern applies
// a penalty, or the paper cannot be marked consistently.
func TestNegativeMarkingIsChecked(t *testing.T) {
	items, paper, opts := soundPaper(40)
	items[5].NegativeMarks = 0.25

	issues := ValidatePaper(items, paper, opts)
	if !hasCode(issues, CodePaperNegativeMarks) {
		t.Fatalf("uneven negative marking must be reported, got [%s]", codes(issues))
	}

	items, paper, opts = soundPaper(40)
	paper.NegativeMarks = 0
	issues = ValidatePaper(items, paper, opts)
	if severityOf(issues, CodePaperNegativeMarks) != models.SeverityCritical {
		t.Error("a pattern with a penalty and a paper without one is a critical mismatch")
	}
}
