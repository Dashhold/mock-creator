package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mockcreator/internal/models"
	"mockcreator/internal/pipeline"
	"mockcreator/internal/quality"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ReviewQueue lists content that is not cleared for delivery.
//
// This is the endpoint that makes requirement "uncertain content must be easy to
// find" real. The default ordering is deliberate: worst verdict first, then
// lowest extraction confidence, so the questions most likely to be genuinely
// broken surface before the borderline ones.
func (h *Handler) ReviewQueue(c *gin.Context) {
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.Question{}).
		Preload("Subject").
		Preload("Options", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Passage").
		Preload("Document")

	// Default to everything that is not passing. A caller can ask for one status.
	if status := strings.TrimSpace(c.Query("quality_status")); status != "" && status != "all" {
		if !models.QualityStatus(status).Valid() {
			badRequestf(c, "quality_status must be one of unchecked, pass, review, failed")
			return
		}
		query = query.Where("quality_status = ?", status)
	} else {
		query = query.Where("quality_status <> ?", models.QualityPass)
	}

	if subject := uintQuery(c, "subject_id"); subject != nil {
		query = query.Where("subject_id = ?", *subject)
	}
	if document := uintQuery(c, "document_id"); document != nil {
		query = query.Where("document_id = ?", *document)
	}
	if exam := uintQuery(c, "exam_id"); exam != nil {
		query = query.Where("id IN (SELECT question_id FROM question_exam_links WHERE exam_id = ?)", *exam)
	}
	// Filtering by defect code is how a reviewer works through one class of
	// problem at a time, which is far faster than case by case.
	if code := strings.TrimSpace(c.Query("issue_code")); code != "" {
		query = query.Where("quality_issues::text LIKE ?", "%\"code\":\""+code+"\"%")
	}
	if severity := strings.TrimSpace(c.Query("severity")); severity != "" {
		query = query.Where("quality_issues::text LIKE ?", "%\"severity\":\""+severity+"\"%")
	}
	if boolQuery(c, "model_checked_only", false) {
		query = query.Where("model_checked = ?", true)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	var questions []models.Question
	if err := query.
		Order("CASE quality_status WHEN 'failed' THEN 0 WHEN 'review' THEN 1 WHEN 'unchecked' THEN 2 ELSE 3 END").
		Order("extraction_confidence asc").
		Order("id asc").
		Limit(size).Offset(offset).
		Find(&questions).Error; err != nil {
		serverError(c, err)
		return
	}

	list(c, questions, gin.H{
		"page":    makePage(page, size, total),
		"summary": h.reviewSummary(),
	})
}

// ReviewSummary reports how much is waiting, grouped so a reviewer can see where
// the work is rather than only how much of it there is.
func (h *Handler) ReviewSummary(c *gin.Context) {
	ok(c, h.reviewSummary())
}

func (h *Handler) reviewSummary() gin.H {
	type statusRow struct {
		QualityStatus string `json:"quality_status"`
		Count         int64  `json:"count"`
	}
	var byStatus []statusRow
	h.DB.Model(&models.Question{}).
		Select("quality_status, COUNT(*) AS count").
		Group("quality_status").
		Scan(&byStatus)

	type subjectRow struct {
		SubjectID uint   `json:"subject_id"`
		Name      string `json:"name"`
		Failed    int64  `json:"failed"`
		Review    int64  `json:"review"`
		Unchecked int64  `json:"unchecked"`
		Pass      int64  `json:"pass"`
	}
	var bySubject []subjectRow
	h.DB.Table("questions AS q").
		Select(`q.subject_id, s.name,
		        COUNT(*) FILTER (WHERE q.quality_status = 'failed')    AS failed,
		        COUNT(*) FILTER (WHERE q.quality_status = 'review')    AS review,
		        COUNT(*) FILTER (WHERE q.quality_status = 'unchecked') AS unchecked,
		        COUNT(*) FILTER (WHERE q.quality_status = 'pass')      AS pass`).
		Joins("JOIN subjects s ON s.id = q.subject_id").
		Where("q.deleted_at IS NULL").
		Group("q.subject_id, s.name").
		Order("failed DESC, review DESC").
		Scan(&bySubject)

	// Counting defects by code needs the JSON expanded, which is what turns a
	// review backlog from a list into a work plan: fix the commonest cause first.
	type codeRow struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Source   string `json:"source"`
		Count    int64  `json:"count"`
	}
	var byCode []codeRow
	h.DB.Raw(`
		SELECT issue->>'code' AS code,
		       issue->>'severity' AS severity,
		       issue->>'source' AS source,
		       COUNT(*) AS count
		FROM questions q
		CROSS JOIN LATERAL jsonb_array_elements(q.quality_issues) AS issue
		WHERE q.deleted_at IS NULL
		  AND q.quality_issues IS NOT NULL
		GROUP BY 1, 2, 3
		ORDER BY count DESC
		LIMIT 40`).Scan(&byCode)

	statuses := map[string]int64{}
	for _, row := range byStatus {
		statuses[row.QualityStatus] = row.Count
	}

	var papers []statusRow
	h.DB.Model(&models.TestPaper{}).
		Select("quality_status, COUNT(*) AS count").
		Group("quality_status").
		Scan(&papers)
	paperStatuses := map[string]int64{}
	for _, row := range papers {
		paperStatuses[row.QualityStatus] = row.Count
	}

	return gin.H{
		"questions_by_status": statuses,
		"papers_by_status":    paperStatuses,
		"by_subject":          bySubject,
		"by_issue":            byCode,
		"rules_version":       quality.RulesVersion,
		"model": gin.H{
			"enabled": h.Cfg.LLM.Enabled(),
			"name":    h.Cfg.LLM.Model,
		},
	}
}

// QuestionProvenance returns everything known about where a question came from
// and how it was processed.
//
// A reviewer looking at a suspect question needs to know whether the extractor
// or the source is at fault, and that is not answerable from the question alone.
// This returns the source lines it was read from alongside the verdicts, so the
// judgement can be made in one place.
func (h *Handler) QuestionProvenance(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var question models.Question
	err := h.DB.
		Preload("Subject").
		Preload("Topic").
		Preload("Passage").
		Preload("Options", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Reviews", func(db *gorm.DB) *gorm.DB { return db.Order("created_at desc") }).
		Preload("ExamLinks.Exam").
		First(&question, id).Error
	if err != nil {
		dbError(c, err, "question")
		return
	}

	payload := gin.H{
		"question": question,
		"issues":   question.Issues(),
		"quality": gin.H{
			"status":        question.QualityStatus,
			"score":         question.QualityScore,
			"checked_at":    question.CheckedAt,
			"rules_version": question.RulesVersion,
			"model_checked": question.ModelChecked,
			"model_name":    question.ModelName,
			"blocking":      question.Blocking(),
			"deliverable":   question.QualityStatus.Deliverable(),
		},
		"extraction": gin.H{
			"engine":            question.ExtractEngine,
			"confidence":        question.ExtractionConfidence,
			"page_no":           question.PageNo,
			"question_number":   question.QuestionNumber,
			"source_first_line": question.SourceFirstLine,
			"source_last_line":  question.SourceLastLine,
			"trailing_text":     question.TrailingText,
		},
	}

	// The source window: the exact lines the parser read. This is what makes a
	// disagreement with the extractor settleable.
	if question.DocumentID != nil {
		var document models.Document
		if err := h.DB.First(&document, *question.DocumentID).Error; err == nil {
			payload["document"] = gin.H{
				"id":                    document.ID,
				"title":                 document.Title,
				"filename":              document.Filename,
				"engine":                document.Engine,
				"extraction_confidence": document.ExtractionConfidence,
				"warnings":              decodeJSONStrings(document.Warnings),
			}
		}
		var conversion models.DocumentConversion
		if err := h.DB.Select("markdown").
			Where("document_id = ?", *question.DocumentID).
			First(&conversion).Error; err == nil {
			payload["source_window"] = sourceWindow(
				conversion.Markdown, question.SourceFirstLine, question.SourceLastLine, 3)
		}
	}

	// Near-duplicates are computed on demand rather than stored, so the answer
	// reflects the warehouse as it is now.
	if matches, err := quality.Duplicates(
		h.DB, question.ID, question.SubjectID, question.ContentHash, question.QuestionText,
	); err == nil && len(matches) > 0 {
		payload["duplicates"] = matches
	}

	ok(c, payload)
}

// sourceWindow returns the lines a question was read from, with context.
func sourceWindow(markdown string, first, last, context int) gin.H {
	if strings.TrimSpace(markdown) == "" || last < first {
		return gin.H{"available": false}
	}
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	from := first - context
	if from < 0 {
		from = 0
	}
	to := last + context + 1
	if to > len(lines) {
		to = len(lines)
	}
	if from >= to {
		return gin.H{"available": false}
	}
	return gin.H{
		"available":   true,
		"first_line":  from,
		"last_line":   to - 1,
		"text":        strings.Join(lines[from:to], "\n"),
		"question_at": gin.H{"first": first, "last": last},
	}
}

// RequalifyQuestions re-runs the quality checks.
//
// It exists because rules change. A question validated by an older generation of
// the checks is not the same as one validated by the current generation, and the
// stored rules version makes the difference visible; this is how the backlog gets
// brought up to date without re-ingesting anything.
func (h *Handler) RequalifyQuestions(c *gin.Context) {
	var payload pipeline.AuditParams
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		badRequest(c, err)
		return
	}

	// A model review can take a while over thousands of questions, so this always
	// goes through the job queue where it can be watched and cancelled.
	job := &models.Job{Type: models.JobQuestionAudit}
	if payload.DocumentID != 0 {
		job.DocumentID = &payload.DocumentID
	}
	if payload.ExamID != 0 {
		job.ExamID = &payload.ExamID
	}
	params, err := encodeParams(payload)
	if err != nil {
		serverError(c, err)
		return
	}
	job.Params = params

	if err := h.Jobs.Enqueue(job); err != nil {
		serverError(c, err)
		return
	}
	accepted(c, gin.H{
		"job":            job,
		"model_will_run": h.Cfg.LLM.Enabled() && h.Cfg.LLM.AuditQuestions && !payload.SkipModel,
	})
}

// PaperQA returns a paper's quality report.
func (h *Handler) PaperQA(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var paper models.TestPaper
	if err := h.DB.Preload("Exam").First(&paper, id).Error; err != nil {
		dbError(c, err, "paper")
		return
	}

	issues := paper.Issues()
	counts := issues.Counts()

	ok(c, gin.H{
		"paper_id":       paper.ID,
		"title":          paper.Title,
		"status":         paper.Status,
		"quality_status": paper.QualityStatus,
		"quality_score":  paper.QualityScore,
		"checked_at":     paper.CheckedAt,
		"rules_version":  paper.RulesVersion,
		"model_checked":  paper.ModelChecked,
		"model_name":     paper.ModelName,
		"publishable":    paper.Publishable(),
		"explanation":    paper.QAExplanation(),
		"counts": gin.H{
			"critical": counts[models.SeverityCritical],
			"major":    counts[models.SeverityMajor],
			"minor":    counts[models.SeverityMinor],
		},
		"blocking": paper.Blocking(),
		"issues":   issues,
	})
}

// RunPaperQA re-runs a paper's quality review.
//
// Synchronous when no model is involved, because the structural checks take
// milliseconds and a reviewer clicking "re-check" should see the answer. With a
// model configured it goes to the queue, because that can take minutes.
func (h *Handler) RunPaperQA(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var paper models.TestPaper
	if err := h.DB.First(&paper, id).Error; err != nil {
		dbError(c, err, "paper")
		return
	}

	skipModel := boolQuery(c, "skip_model", false)
	useModel := h.Cfg.LLM.Enabled() && h.Cfg.LLM.AuditPapers && !skipModel

	if useModel {
		job := &models.Job{Type: models.JobPaperQA, PaperID: &paper.ID, ExamID: &paper.ExamID}
		params, err := encodeParams(pipeline.PaperQAParams{PaperID: paper.ID})
		if err != nil {
			serverError(c, err)
			return
		}
		job.Params = params
		if err := h.Jobs.Enqueue(job); err != nil {
			serverError(c, err)
			return
		}
		accepted(c, gin.H{"job": job, "model_will_run": true})
		return
	}

	ctx, cancel := contextWithTimeout(c, 60*time.Second)
	defer cancel()

	result, err := h.Jobs.CheckPaper(ctx, paper.ID, true)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, result)
}

// ResolveIssue records a human decision about a question's quality.
//
// A reviewer can accept a question despite its flags, or reject it. Either way
// the decision is stored as a review row with the reviewer's name, so the audit
// trail shows a person made the call rather than the gate quietly changing its
// mind.
func (h *Handler) ResolveIssue(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var payload struct {
		Decision string `json:"decision" binding:"required"` // accept | reject | recheck
		Reviewer string `json:"reviewer"`
		Notes    string `json:"notes"`
		// Difficulty lets a reviewer rate hardness in the same action, which is
		// the only way the difficulty mix ever becomes meaningful.
		Difficulty string `json:"difficulty"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	var question models.Question
	if err := h.DB.
		Preload("Options", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Subject").Preload("Passage").
		First(&question, id).Error; err != nil {
		dbError(c, err, "question")
		return
	}

	reviewer := strings.TrimSpace(payload.Reviewer)
	if reviewer == "" {
		reviewer = "reviewer"
	}

	switch strings.ToLower(strings.TrimSpace(payload.Decision)) {
	case "accept":
		// Accepting overrides the gate, which is legitimate: a person may know the
		// flag is a false positive. It is recorded as an override, not as the
		// question having passed the checks.
		issues := question.Issues()
		issues = append(issues, models.QualityIssue{
			Code:     "human_override",
			Severity: models.SeverityMinor,
			Source:   models.SourceHuman,
			Message: fmt.Sprintf("%s accepted this question despite %d automated finding(s)",
				reviewer, len(issues)),
			Evidence: trimText(payload.Notes, 200),
		})
		now := time.Now()
		updates := map[string]any{
			"quality_status": models.QualityPass,
			"quality_issues": issues.JSON(),
			"checked_at":     &now,
			"status":         models.StatusApproved,
		}
		if difficulty := models.Difficulty(strings.ToLower(payload.Difficulty)); difficulty.Valid() {
			updates["difficulty"] = difficulty
			// A human rating is trustworthy, which is what lets the paper-level
			// difficulty check actually run.
			updates["difficulty_confidence"] = 1.0
		}
		if err := h.DB.Model(&question).Updates(updates).Error; err != nil {
			serverError(c, err)
			return
		}
		h.recordHumanReview(question.ID, reviewer, payload.Notes, models.StatusApproved, payload.Difficulty)

	case "reject":
		issues := append(question.Issues(), models.QualityIssue{
			Code:     "human_rejected",
			Severity: models.SeverityCritical,
			Source:   models.SourceHuman,
			Message:  fmt.Sprintf("%s rejected this question", reviewer),
			Evidence: trimText(payload.Notes, 200),
		})
		now := time.Now()
		if err := h.DB.Model(&question).Updates(map[string]any{
			"quality_status": models.QualityFailed,
			"quality_issues": issues.JSON(),
			"checked_at":     &now,
			"status":         models.StatusRejected,
		}).Error; err != nil {
			serverError(c, err)
			return
		}
		h.recordHumanReview(question.ID, reviewer, payload.Notes, models.StatusRejected, "")

	case "recheck":
		// Re-run the rules on the current text, which is what a reviewer wants
		// after editing the question.
		input := quality.Input{
			Type:                 question.Type,
			Stem:                 question.QuestionText,
			AnswerText:           question.AnswerText,
			Explanation:          question.Explanation,
			TrailingText:         question.TrailingText,
			ExtractionConfidence: question.ExtractionConfidence,
			ModelReviewed:        question.ModelChecked,
		}
		if question.Subject != nil {
			input.SubjectCode = question.Subject.Code
		}
		if question.Passage != nil {
			input.PassageText = question.Passage.Text
		}
		for _, o := range question.Options {
			input.Options = append(input.Options, quality.OptionInput{
				Label: o.Label, Text: o.Text, IsCorrect: o.IsCorrect,
			})
		}
		issues := quality.ValidateQuestion(input, quality.Options{
			MinExtractionConfidence: h.Cfg.Converter.MinExtractionConfidence,
			ModelExpected:           h.Cfg.LLM.Enabled() && h.Cfg.LLM.AuditQuestions,
		})
		question.Apply(issues, quality.Score(issues, question.ExtractionConfidence), quality.RulesVersion)
		status := question.Status
		if question.QualityStatus == models.QualityPass {
			if status == models.StatusPending {
				status = models.StatusApproved
			}
		} else if status == models.StatusApproved {
			status = models.StatusPending
		}
		if err := h.DB.Model(&question).Updates(map[string]any{
			"quality_status": question.QualityStatus,
			"quality_score":  question.QualityScore,
			"quality_issues": question.QualityIssues,
			"checked_at":     question.CheckedAt,
			"rules_version":  question.RulesVersion,
			"status":         status,
		}).Error; err != nil {
			serverError(c, err)
			return
		}

	default:
		badRequestf(c, "decision must be accept, reject or recheck")
		return
	}

	var refreshed models.Question
	if err := h.DB.
		Preload("Options", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Reviews", func(db *gorm.DB) *gorm.DB { return db.Order("created_at desc") }).
		First(&refreshed, id).Error; err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"question": refreshed, "issues": refreshed.Issues()})
}

func (h *Handler) recordHumanReview(questionID uint, reviewer, notes string, verdict models.Status, difficulty string) {
	review := models.QualityReview{
		QuestionID:   questionID,
		ReviewerType: "human",
		ReviewerName: reviewer,
		Grounded:     true,
		Verdict:      verdict,
		Notes:        notes,
	}
	if d := models.Difficulty(strings.ToLower(difficulty)); d.Valid() {
		review.SetDifficulty = d
	}
	if err := h.DB.Create(&review).Error; err != nil {
		// The decision is already recorded on the question; losing the audit row
		// is worth logging but not worth failing the request.
		fmt.Printf("handler: could not store review for question %d: %v\n", questionID, err)
	}
}

// ModelStatus reports whether model review is configured and reachable.
//
// Worth its own endpoint because "the model is not configured" and "the model is
// configured but unreachable" produce identical-looking papers, and only the
// second is a problem to fix.
func (h *Handler) ModelStatus(c *gin.Context) {
	payload := gin.H{
		"configured": h.Cfg.LLM.Enabled(),
		"model":      h.Cfg.LLM.Model,
		"base_url":   h.Cfg.LLM.BaseURL,
		"tasks": gin.H{
			"audit_questions":   h.Cfg.LLM.AuditQuestions,
			"audit_papers":      h.Cfg.LLM.AuditPapers,
			"repair_boundaries": h.Cfg.LLM.RepairBoundaries,
			"judge_duplicates":  h.Cfg.LLM.JudgeDuplicates,
		},
	}
	if !h.Cfg.LLM.Enabled() {
		payload["reachable"] = false
		payload["note"] = "no model is configured; the deterministic checks still run and " +
			"model-only findings are reported as not run rather than as passes"
		ok(c, payload)
		return
	}

	ctx, cancel := contextWithTimeout(c, 30*time.Second)
	defer cancel()

	if err := h.Jobs.LLM().Ping(ctx); err != nil {
		payload["reachable"] = false
		payload["error"] = err.Error()
		c.JSON(http.StatusServiceUnavailable, gin.H{"data": payload})
		return
	}
	payload["reachable"] = true
	ok(c, payload)
}

func trimText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "\u2026"
}

// encodeParams serialises job parameters.
func encodeParams(value any) ([]byte, error) {
	return json.Marshal(value)
}

// decodeJSONStrings reads a stored JSON array of strings.
func decodeJSONStrings(raw datatypes.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
