package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"mockcreator/internal/llm"
	"mockcreator/internal/models"
	"mockcreator/internal/quality"

	"gorm.io/gorm"
)

// AuditParams selects which questions to re-check.
type AuditParams struct {
	// DocumentID limits the run to one document's questions.
	DocumentID uint `json:"document_id,omitempty"`
	// ExamID limits the run to questions linked to one exam.
	ExamID uint `json:"exam_id,omitempty"`
	// QuestionIDs limits the run to specific questions.
	QuestionIDs []uint `json:"question_ids,omitempty"`
	// IncludePassing re-checks questions that already pass. Off by default,
	// because the point of a re-run is usually to clear the backlog.
	IncludePassing bool `json:"include_passing,omitempty"`
	// SkipModel runs only the deterministic checks, which is what you want after
	// changing a rule.
	SkipModel bool `json:"skip_model,omitempty"`
	// Limit bounds one run so a first audit of a large warehouse does not become
	// an open-ended model bill.
	Limit int `json:"limit,omitempty"`
}

// AuditResult summarises an audit run.
type AuditResult struct {
	Checked int `json:"checked"`
	Passed  int `json:"passed"`
	Review  int `json:"needs_review"`
	Failed  int `json:"failed"`
	Changed int `json:"changed"`

	ModelRan       bool   `json:"model_ran"`
	ModelName      string `json:"model_name,omitempty"`
	ModelReviewed  int    `json:"model_reviewed"`
	ModelSkipped   int    `json:"model_skipped"`
	ModelRejected  int    `json:"model_rejected_ungrounded"`
	ModelErrors    int    `json:"model_errors"`
	PromptTokens   int    `json:"prompt_tokens,omitempty"`
	ReplyTokens    int    `json:"reply_tokens,omitempty"`
	DuplicateFound int    `json:"duplicates_found"`

	Warnings   []string `json:"warnings,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

// auditBatchSize bounds how many questions are held in memory at once.
const auditBatchSize = 200

// defaultAuditLimit caps an unbounded audit. A warehouse can hold tens of
// thousands of questions and a first run should not silently become a very large
// model bill; the job simply reports that more remain.
const defaultAuditLimit = 2000

func auditParamsFor(documentID uint) []byte {
	raw, _ := json.Marshal(AuditParams{DocumentID: documentID})
	return raw
}

// runQuestionAudit re-validates questions and optionally has a model review them.
//
// The deterministic checks always run, because they are cheap and are what the
// gate depends on. The model runs only where it adds something the rules cannot
// judge, and only on questions that are structurally sound enough to be worth
// asking about: there is no point asking a model whether a question with no
// options is answerable.
func (r *Runner) runQuestionAudit(ctx context.Context, job *models.Job) (any, error) {
	started := time.Now()

	var params AuditParams
	if err := decodeParams(job, &params); err != nil {
		return nil, err
	}
	if params.DocumentID == 0 && job.DocumentID != nil {
		params.DocumentID = *job.DocumentID
	}
	if params.ExamID == 0 && job.ExamID != nil {
		params.ExamID = *job.ExamID
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultAuditLimit
	}

	useModel := r.llm.Available() && r.cfg.LLM.AuditQuestions && !params.SkipModel
	result := AuditResult{ModelRan: useModel, ModelName: r.llm.Model()}

	checks := quality.Options{
		UnsortedSubjectCode:     unsortedSubjectCode,
		MinExtractionConfidence: r.cfg.Converter.MinExtractionConfidence,
		ModelExpected:           r.llm.Available() && r.cfg.LLM.AuditQuestions,
	}

	query := r.db.Model(&models.Question{}).
		Preload("Options", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Subject").
		Preload("Passage")

	switch {
	case len(params.QuestionIDs) > 0:
		query = query.Where("id IN ?", params.QuestionIDs)
	case params.DocumentID != 0:
		query = query.Where("document_id = ?", params.DocumentID)
	case params.ExamID != 0:
		query = query.Where("id IN (SELECT question_id FROM question_exam_links WHERE exam_id = ?)", params.ExamID)
	}
	if !params.IncludePassing {
		query = query.Where("quality_status <> ? OR model_checked = ?", models.QualityPass, false)
	}

	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count questions to audit: %w", err)
	}
	if total > int64(limit) {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d questions match but this run is capped at %d; run it again to continue",
			total, limit))
		total = int64(limit)
	}

	processed := 0
	var batch []models.Question

	rows := query.Order("id asc").Limit(limit).Session(&gorm.Session{})
	if err := rows.FindInBatches(&batch, auditBatchSize, func(tx *gorm.DB, _ int) error {
		for i := range batch {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			question := &batch[i]
			r.auditOne(ctx, question, checks, useModel, &result)
			processed++
			r.counters(job.ID, processed, int(total))
		}
		return nil
	}).Error; err != nil && !errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("audit questions: %w", err)
	}

	result.Checked = processed
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// auditOne re-checks a single question and writes its verdict.
func (r *Runner) auditOne(
	ctx context.Context,
	question *models.Question,
	checks quality.Options,
	useModel bool,
	result *AuditResult,
) {
	previous := question.QualityStatus

	subjectCode := ""
	if question.Subject != nil {
		subjectCode = question.Subject.Code
	}
	passageText := ""
	if question.Passage != nil {
		passageText = question.Passage.Text
	}

	options := make([]quality.OptionInput, 0, len(question.Options))
	for _, o := range question.Options {
		options = append(options, quality.OptionInput{
			Label: o.Label, Text: o.Text, IsCorrect: o.IsCorrect,
		})
	}

	checks.ModelExpected = r.llm.Available() && r.cfg.LLM.AuditQuestions
	input := quality.Input{
		Type:                 question.Type,
		Stem:                 question.QuestionText,
		Options:              options,
		AnswerText:           question.AnswerText,
		Explanation:          question.Explanation,
		PassageText:          passageText,
		TrailingText:         question.TrailingText,
		SubjectCode:          subjectCode,
		ExtractionConfidence: question.ExtractionConfidence,
		ModelReviewed:        question.ModelChecked,
	}

	issues := quality.ValidateQuestion(input, checks)

	// Duplicate detection needs the warehouse, so it happens here rather than in
	// the pure rule layer.
	if matches, err := quality.Duplicates(
		r.db, question.ID, question.SubjectID, question.ContentHash, question.QuestionText,
	); err != nil {
		result.Warnings = appendOnce(result.Warnings, "duplicate detection is degraded: "+err.Error())
	} else if len(matches) > 0 {
		matches = r.confirmDuplicates(ctx, question, matches, useModel, result)
		if len(matches) > 0 {
			result.DuplicateFound += len(matches)
			issues = append(issues, quality.DuplicateIssues(matches)...)
		}
	}

	modelChecked := question.ModelChecked
	modelName := question.ModelName

	// Only ask the model about questions that are structurally worth asking
	// about. A question already missing its options does not need a model to
	// confirm it is unanswerable, and asking wastes the budget that should go to
	// the ones where judgement is the deciding factor.
	if useModel && worthModelReview(issues, input) {
		audit, err := r.llm.AuditQuestion(ctx, llm.AuditQuestionInput{
			Subject:     subjectCode,
			Type:        question.Type,
			Stem:        question.QuestionText,
			Options:     toAuditOptions(question.Options),
			AnswerText:  question.AnswerText,
			Explanation: question.Explanation,
			Passage:     passageText,
		})
		switch {
		case err != nil:
			result.ModelErrors++
			result.Warnings = appendOnce(result.Warnings, "model review failed: "+err.Error())
		default:
			result.ModelReviewed++
			result.PromptTokens += audit.Usage.PromptTokens
			result.ReplyTokens += audit.Usage.CompletionTokens
			if !audit.Grounding.Verified {
				result.ModelRejected++
			}
			issues = append(issues, audit.Issues...)
			modelChecked = true
			modelName = r.llm.Model()
			r.recordModelReview(question, audit)
		}
	} else if useModel {
		result.ModelSkipped++
	}

	question.Apply(issues, quality.Score(issues, question.ExtractionConfidence), quality.RulesVersion)
	question.ModelChecked = modelChecked
	question.ModelName = modelName

	// Approval follows the gate. A question that used to be approved and now
	// fails must lose that approval, or the gate is decorative.
	status := question.Status
	switch question.QualityStatus {
	case models.QualityPass:
		if r.cfg.Review.AutoApproveExtracted && status == models.StatusPending {
			status = models.StatusApproved
		}
	default:
		if status == models.StatusApproved {
			status = models.StatusPending
		}
	}

	if err := r.db.Model(&models.Question{}).Where("id = ?", question.ID).Updates(map[string]any{
		"quality_status": question.QualityStatus,
		"quality_score":  question.QualityScore,
		"quality_issues": question.QualityIssues,
		"checked_at":     question.CheckedAt,
		"rules_version":  question.RulesVersion,
		"model_checked":  question.ModelChecked,
		"model_name":     question.ModelName,
		"status":         status,
	}).Error; err != nil {
		log.Printf("pipeline: could not record audit for question %d: %v", question.ID, err)
		return
	}

	switch question.QualityStatus {
	case models.QualityPass:
		result.Passed++
	case models.QualityFailed:
		result.Failed++
	default:
		result.Review++
	}
	if question.QualityStatus != previous {
		result.Changed++
	}
}

// worthModelReview decides whether a model's judgement can still change the
// outcome for this question.
func worthModelReview(issues models.QualityIssues, input quality.Input) bool {
	// Already conclusively broken: the rules found something a model cannot
	// argue away, so spending a call on it buys nothing.
	for _, issue := range issues {
		switch issue.Code {
		case quality.CodeStemEmpty, quality.CodeNoOptions, quality.CodeTooFewOptions,
			quality.CodeStemTooShort, quality.CodeUnreadableChars, quality.CodeOptionEmpty:
			return false
		}
	}
	// Nothing to read means nothing to judge.
	return len(input.Stem) > 20 && len(input.Options) >= 2
}

// confirmDuplicates asks the model whether near-duplicates really are the same
// question, and drops the ones it says are not.
//
// Trigram similarity cannot tell "a train travels 240 km" from "a train travels
// 260 km", and suppressing one of those as a duplicate would remove a perfectly
// good question. Exact hash matches are not second-guessed.
func (r *Runner) confirmDuplicates(
	ctx context.Context,
	question *models.Question,
	matches []quality.DuplicateMatch,
	useModel bool,
	result *AuditResult,
) []quality.DuplicateMatch {
	if !useModel || !r.cfg.LLM.JudgeDuplicates {
		return matches
	}
	kept := make([]quality.DuplicateMatch, 0, len(matches))
	for _, match := range matches {
		if match.Exact {
			kept = append(kept, match)
			continue
		}
		verdict, err := r.llm.JudgeDuplicate(ctx, question.QuestionText, match.Stem)
		if err != nil {
			// Unable to ask, so the rule's suspicion stands and a human decides.
			result.ModelErrors++
			kept = append(kept, match)
			continue
		}
		result.PromptTokens += verdict.Usage.PromptTokens
		result.ReplyTokens += verdict.Usage.CompletionTokens
		if verdict.Same {
			kept = append(kept, match)
		}
	}
	return kept
}

// recordModelReview stores the model's findings as a review row, so what it said
// stays visible and a human can disagree with it on the record.
func (r *Runner) recordModelReview(question *models.Question, audit *llm.QuestionAudit) {
	issues, _ := json.Marshal(audit.Issues)
	verdict := models.StatusApproved
	if audit.Issues.Worst() != models.QualityPass {
		verdict = models.StatusPending
	}

	review := models.QualityReview{
		QuestionID:       question.ID,
		ReviewerType:     "model",
		ReviewerName:     r.llm.Model(),
		Issues:           issues,
		Grounded:         audit.Grounding.Verified,
		PromptTokens:     audit.Usage.PromptTokens,
		CompletionTokens: audit.Usage.CompletionTokens,
		ClarityScore:     audit.Clarity,
		IsAmbiguous:      hasIssueCode(audit.Issues, llm.CodeAmbiguous),
		IsDuplicate:      false,
		Verdict:          verdict,
		Notes:            audit.Notes,
	}
	// One model review per question per run: replace rather than accumulate, or
	// the review list becomes unreadable after a few re-runs.
	if err := r.db.Where("question_id = ? AND reviewer_type = ?", question.ID, "model").
		Unscoped().Delete(&models.QualityReview{}).Error; err != nil {
		log.Printf("pipeline: could not clear prior model review for question %d: %v", question.ID, err)
	}
	if err := r.db.Create(&review).Error; err != nil {
		log.Printf("pipeline: could not store model review for question %d: %v", question.ID, err)
	}
}

func toAuditOptions(options []models.QuestionOption) []llm.AuditOption {
	out := make([]llm.AuditOption, 0, len(options))
	for _, o := range options {
		out = append(out, llm.AuditOption{Label: o.Label, Text: o.Text, IsCorrect: o.IsCorrect})
	}
	return out
}

func hasIssueCode(list models.QualityIssues, code string) bool {
	for _, issue := range list {
		if issue.Code == code {
			return true
		}
	}
	return false
}

// appendOnce adds a warning if it is not already present, so one repeated failure
// does not produce a thousand identical lines.
func appendOnce(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	if len(list) >= 20 {
		return list
	}
	return append(list, value)
}
