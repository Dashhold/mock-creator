package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mockcreator/internal/llm"
	"mockcreator/internal/models"
	"mockcreator/internal/quality"

	"gorm.io/gorm"
)

// PaperQAParams selects which paper to review.
type PaperQAParams struct {
	PaperID uint `json:"paper_id"`
	// SkipModel runs only the structural checks.
	SkipModel bool `json:"skip_model,omitempty"`
}

// PaperQAResult is the outcome of a paper review.
type PaperQAResult struct {
	PaperID uint                 `json:"paper_id"`
	Status  models.QualityStatus `json:"status"`
	Score   float64              `json:"score"`

	Critical int `json:"critical"`
	Major    int `json:"major"`
	Minor    int `json:"minor"`

	Issues models.QualityIssues `json:"issues"`

	ModelRan      bool   `json:"model_ran"`
	ModelName     string `json:"model_name,omitempty"`
	ModelSummary  string `json:"model_summary,omitempty"`
	ModelGrounded bool   `json:"model_grounded"`
	PromptTokens  int    `json:"prompt_tokens,omitempty"`
	ReplyTokens   int    `json:"reply_tokens,omitempty"`

	Publishable bool     `json:"publishable"`
	Explanation string   `json:"explanation"`
	Warnings    []string `json:"warnings,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
}

// runPaperQA validates a complete paper and records the verdict that gates
// publication and export.
func (r *Runner) runPaperQA(ctx context.Context, job *models.Job) (any, error) {
	started := time.Now()

	var params PaperQAParams
	if err := decodeParams(job, &params); err != nil {
		return nil, err
	}
	if params.PaperID == 0 && job.PaperID != nil {
		params.PaperID = *job.PaperID
	}
	if params.PaperID == 0 {
		return nil, errors.New("paper QA job has no paper")
	}

	r.progress(job.ID, "loading paper", 10)
	result, err := r.checkPaper(ctx, params.PaperID, params.SkipModel, func(stage string, pct int) {
		r.progress(job.ID, stage, pct)
	})
	if err != nil {
		return nil, err
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// CheckPaper validates a paper and stores its verdict. Exported so a handler can
// re-run QA on demand without going through the job queue.
func (r *Runner) CheckPaper(ctx context.Context, paperID uint, skipModel bool) (*PaperQAResult, error) {
	return r.checkPaper(ctx, paperID, skipModel, nil)
}

func (r *Runner) checkPaper(
	ctx context.Context,
	paperID uint,
	skipModel bool,
	progress func(stage string, percent int),
) (*PaperQAResult, error) {
	note := func(stage string, pct int) {
		if progress != nil {
			progress(stage, pct)
		}
	}

	var paper models.TestPaper
	if err := r.db.First(&paper, paperID).Error; err != nil {
		return nil, fmt.Errorf("load paper %d: %w", paperID, err)
	}

	var items []models.TestPaperItem
	if err := r.db.
		Preload("Question.Options", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Question.Passage").
		Preload("Subject").
		Where("test_paper_id = ?", paperID).
		Order("sequence_no asc").
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("load paper items: %w", err)
	}

	opts, warnings, err := r.paperExpectations(&paper, items)
	if err != nil {
		return nil, err
	}

	useModel := r.llm.Available() && r.cfg.LLM.AuditPapers && !skipModel
	opts.ModelExpected = r.llm.Available() && r.cfg.LLM.AuditPapers
	opts.ModelRan = useModel

	note("checking structure", 35)
	checkItems := toQualityPaperItems(items)
	issues := quality.ValidatePaper(checkItems, paper, opts)

	result := &PaperQAResult{
		PaperID:   paperID,
		ModelName: r.llm.Model(),
		Warnings:  warnings,
	}

	if useModel {
		note("final model review", 65)
		audit, err := r.llm.AuditPaper(ctx, paper.Title, toLLMPaperItems(checkItems),
			structureNote(opts, len(checkItems)))
		switch {
		case err != nil:
			// A model that cannot be reached must not quietly turn into a pass.
			// The gap is recorded as a defect so the paper lands in review.
			result.Warnings = appendOnce(result.Warnings, "final model review failed: "+err.Error())
			issues = append(issues, models.QualityIssue{
				Code:     quality.CodeModelNotRun,
				Severity: models.SeverityMajor,
				Source:   models.SourceRules,
				Message:  "the final model review could not run, so only the structural checks have been applied",
				Evidence: err.Error(),
			})
		default:
			result.ModelRan = true
			result.ModelSummary = audit.Summary
			result.ModelGrounded = audit.Grounding.Verified
			result.PromptTokens = audit.Usage.PromptTokens
			result.ReplyTokens = audit.Usage.CompletionTokens
			issues = append(issues, audit.Issues...)
		}
	}

	note("recording verdict", 85)

	score := quality.PaperScore(issues, len(checkItems))
	paper.Apply(issues, score, quality.RulesVersion)
	paper.ModelChecked = result.ModelRan
	if result.ModelRan {
		paper.ModelName = r.llm.Model()
	}

	// A paper that no longer passes cannot stay published.
	status := paper.Status
	if paper.QualityStatus != models.QualityPass && status == models.StatusPublished {
		status = models.StatusDraft
		result.Warnings = appendOnce(result.Warnings,
			"this paper was published and has been returned to draft because it no longer passes QA")
	}

	if err := r.db.Model(&models.TestPaper{}).Where("id = ?", paperID).Updates(map[string]any{
		"quality_status": paper.QualityStatus,
		"quality_score":  paper.QualityScore,
		"quality_issues": paper.QualityIssues,
		"checked_at":     paper.CheckedAt,
		"rules_version":  paper.RulesVersion,
		"model_checked":  paper.ModelChecked,
		"model_name":     paper.ModelName,
		"status":         status,
	}).Error; err != nil {
		return nil, fmt.Errorf("record paper QA: %w", err)
	}

	counts := issues.Counts()
	result.Status = paper.QualityStatus
	result.Score = score
	result.Critical = counts[models.SeverityCritical]
	result.Major = counts[models.SeverityMajor]
	result.Minor = counts[models.SeverityMinor]
	result.Issues = issues
	result.Publishable = paper.QualityStatus == models.QualityPass
	result.Explanation = paper.QAExplanation()
	return result, nil
}

// paperExpectations reads what the paper was supposed to be from its pattern.
//
// Checking a paper against the pattern it was built from, rather than against a
// house standard, is what keeps this exam-agnostic: the target is whatever the
// user configured.
func (r *Runner) paperExpectations(
	paper *models.TestPaper,
	items []models.TestPaperItem,
) (quality.PaperOptions, []string, error) {
	opts := quality.PaperOptions{CountTolerance: 0}
	var warnings []string

	// Difficulty can only be checked when the questions carry a rated difficulty.
	// Extraction cannot judge hardness, so this is usually false until a reviewer
	// or a model has rated them, and saying so is better than pretending.
	rated := 0
	for _, item := range items {
		if item.Question != nil && item.Question.DifficultyConfidence >= 0.5 {
			rated++
		}
	}
	opts.DifficultyRated = len(items) > 0 && rated*2 > len(items)

	if paper.PatternID == nil {
		warnings = append(warnings,
			"this paper records no pattern, so its counts, marks and sections cannot be checked against one")
		return opts, warnings, nil
	}

	var pattern models.ExamPattern
	if err := r.db.Preload("Sections").First(&pattern, *paper.PatternID).Error; err != nil {
		warnings = append(warnings, fmt.Sprintf(
			"the pattern this paper was built from (%d) could not be loaded, so structural checks are limited",
			*paper.PatternID))
		return opts, warnings, nil
	}

	opts.ExpectedQuestions = paper.TotalQuestions
	opts.ExpectedMarks = paper.TotalMarks
	opts.ExpectedNegative = pattern.NegativeMarks
	opts.ExpectedDuration = paper.DurationMin
	opts.ExpectedDifficulty = models.DefaultMixFor(paper.Type)

	// Sections are compared by what the paper actually asked for, which is the
	// scaled plan recorded in analytics, falling back to the pattern.
	if expected := expectedSectionsFromAnalytics(paper); len(expected) > 0 {
		opts.ExpectedSections = expected
	} else {
		expected := map[string]int{}
		for _, section := range pattern.Sections {
			if section.QuestionCount > 0 {
				expected[section.Name] = section.QuestionCount
			}
		}
		opts.ExpectedSections = expected
	}

	return opts, warnings, nil
}

// expectedSectionsFromAnalytics reads the per-section request recorded when the
// paper was built, which is the honest target for a paper whose total was scaled.
func expectedSectionsFromAnalytics(paper *models.TestPaper) map[string]int {
	if len(paper.Analytics) == 0 {
		return nil
	}
	var analytics models.PaperAnalytics
	if err := json.Unmarshal(paper.Analytics, &analytics); err != nil {
		return nil
	}
	out := make(map[string]int, len(analytics.SectionFill))
	for _, fill := range analytics.SectionFill {
		if fill.Requested > 0 {
			out[fill.Section] = fill.Requested
		}
	}
	return out
}

func toQualityPaperItems(items []models.TestPaperItem) []quality.PaperItem {
	out := make([]quality.PaperItem, 0, len(items))
	for _, item := range items {
		entry := quality.PaperItem{
			SequenceNo:    item.SequenceNo,
			SectionName:   item.SectionName,
			QuestionID:    item.QuestionID,
			Marks:         item.Marks,
			NegativeMarks: item.NegativeMarks,
		}
		if item.Subject != nil {
			entry.SubjectID = item.Subject.ID
			entry.SubjectName = item.Subject.Name
		}
		if q := item.Question; q != nil {
			entry.Stem = q.QuestionText
			entry.AnswerText = q.AnswerText
			entry.Explanation = q.Explanation
			entry.Type = q.Type
			entry.Difficulty = q.Difficulty
			entry.DifficultyConfidence = q.DifficultyConfidence
			entry.QualityStatus = q.QualityStatus
			entry.HasAnswer = q.HasAnswer
			if q.PassageID != nil {
				entry.PassageID = *q.PassageID
			}
			if q.Passage != nil {
				entry.PassageText = q.Passage.Text
			}
			for _, o := range q.Options {
				entry.Options = append(entry.Options, quality.OptionInput{
					Label: o.Label, Text: o.Text, IsCorrect: o.IsCorrect,
				})
			}
		}
		out = append(out, entry)
	}
	return out
}

func toLLMPaperItems(items []quality.PaperItem) []llm.PaperItem {
	out := make([]llm.PaperItem, 0, len(items))
	for _, item := range items {
		entry := llm.PaperItem{
			SequenceNo: item.SequenceNo,
			Section:    item.SectionName,
			Subject:    item.SubjectName,
			Difficulty: string(item.Difficulty),
			Stem:       item.Stem,
			HasPassage: item.PassageID != 0,
		}
		for _, o := range item.Options {
			entry.Options = append(entry.Options, o.Text)
			if o.IsCorrect {
				entry.AnswerLabel = o.Label
			}
		}
		if entry.AnswerLabel == "" {
			entry.AnswerLabel = "none"
		}
		out = append(out, entry)
	}
	return out
}

// structureNote tells the model what has already been verified, so it spends its
// attention on judgement rather than re-counting.
func structureNote(opts quality.PaperOptions, count int) string {
	parts := []string{fmt.Sprintf("%d questions", count)}
	if opts.ExpectedMarks > 0 {
		parts = append(parts, fmt.Sprintf("%.0f marks", opts.ExpectedMarks))
	}
	if opts.ExpectedDuration > 0 {
		parts = append(parts, fmt.Sprintf("%d minutes", opts.ExpectedDuration))
	}
	if len(opts.ExpectedSections) > 0 {
		parts = append(parts, fmt.Sprintf("%d sections", len(opts.ExpectedSections)))
	}
	return strings.Join(parts, ", ") + "; counts, marks and section sizes already verified"
}
