package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"mockcreator/internal/blueprint"
	"mockcreator/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListPapers returns generated papers.
func (h *Handler) ListPapers(c *gin.Context) {
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.TestPaper{}).Preload("Exam")
	query = filterQuery(c, query, map[string]string{
		"type":   "type",
		"status": "status",
	})
	if examID := uintQuery(c, "exam_id"); examID != nil {
		query = query.Where("exam_id = ?", *examID)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		query = query.Where("LOWER(title) LIKE ?", "%"+strings.ToLower(search)+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	var papers []models.TestPaper
	if err := query.Order("created_at desc").Limit(size).Offset(offset).
		Find(&papers).Error; err != nil {
		serverError(c, err)
		return
	}
	list(c, papers, makePage(page, size, total))
}

// GetPaper returns a paper with its questions in order.
func (h *Handler) GetPaper(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var paper models.TestPaper
	err := h.DB.
		Preload("Exam").
		Preload("Pattern").
		Preload("Items", func(db *gorm.DB) *gorm.DB {
			return db.Order("test_paper_items.sequence_no asc")
		}).
		Preload("Items.Subject").
		Preload("Items.Question").
		Preload("Items.Question.Options", func(db *gorm.DB) *gorm.DB {
			return db.Order("question_options.order_index asc")
		}).
		First(&paper, id).Error
	if err != nil {
		dbError(c, err, "paper")
		return
	}
	ok(c, paper)
}

// generatePayload is the paper generation request.
type generatePayload struct {
	PatternID       *uint                 `json:"pattern_id"`
	Title           string                `json:"title"`
	Type            string                `json:"type"`
	Seed            int64                 `json:"seed"`
	TotalQuestions  int                   `json:"total_questions"`
	DurationMin     int                   `json:"duration_min"`
	DifficultyMix   *models.DifficultyMix `json:"difficulty_mix"`
	IncludeBorrowed *bool                 `json:"include_borrowed"`
	OnlyApproved    *bool                 `json:"only_approved"`
	RequireAnswer   *bool                 `json:"require_answer"`
	YearFrom        *int                  `json:"year_from"`
	YearTo          *int                  `json:"year_to"`
	ExcludePaperIDs []uint                `json:"exclude_paper_ids"`
	Notes           string                `json:"notes"`
}

// toRequest converts the payload into a blueprint request.
func (p generatePayload) toRequest(examID uint) blueprint.Request {
	req := blueprint.Request{
		ExamID:          examID,
		PatternID:       p.PatternID,
		Title:           p.Title,
		Type:            models.PaperType(p.Type),
		Seed:            p.Seed,
		TotalQuestions:  p.TotalQuestions,
		DurationMin:     p.DurationMin,
		DifficultyMix:   p.DifficultyMix,
		IncludeBorrowed: true,
		RequireAnswer:   true,
		YearFrom:        p.YearFrom,
		YearTo:          p.YearTo,
		ExcludePaperIDs: p.ExcludePaperIDs,
		Notes:           p.Notes,
	}
	if p.IncludeBorrowed != nil {
		req.IncludeBorrowed = *p.IncludeBorrowed
	}
	if p.RequireAnswer != nil {
		req.RequireAnswer = *p.RequireAnswer
	}
	if p.OnlyApproved != nil {
		req.OnlyApproved = *p.OnlyApproved
	}
	return req
}

// PaperAvailability reports whether a paper can be built before building it.
//
// Worth its own endpoint: the answer tells the user exactly which subject is
// short and why, which is more useful than a thin paper with no explanation.
func (h *Handler) PaperAvailability(c *gin.Context) {
	examID, valid := idParam(c)
	if !valid {
		return
	}

	var payload generatePayload
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		badRequest(c, err)
		return
	}

	report, err := blueprint.Inspect(h.DB, payload.toRequest(examID))
	if err != nil {
		badRequest(c, err)
		return
	}
	ok(c, report)
}

// GeneratePaper queues paper assembly.
func (h *Handler) GeneratePaper(c *gin.Context) {
	examID, valid := idParam(c)
	if !valid {
		return
	}
	var exam models.Exam
	if err := h.DB.First(&exam, examID).Error; err != nil {
		dbError(c, err, "exam")
		return
	}

	var payload generatePayload
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		badRequest(c, err)
		return
	}

	req := payload.toRequest(examID)

	// Fail fast on an impossible request rather than queueing a job that cannot
	// succeed. The report tells the user what to fix.
	report, err := blueprint.Inspect(h.DB, req)
	if err != nil {
		badRequest(c, err)
		return
	}
	if report.TotalAvailable == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":        "no questions are available for this exam's pattern yet",
			"availability": report,
		})
		return
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		serverError(c, err)
		return
	}

	job := &models.Job{Type: models.JobPaperBuild, ExamID: &examID, Params: encoded}
	if err := h.Jobs.Enqueue(job); err != nil {
		serverError(c, err)
		return
	}

	accepted(c, gin.H{"job": job, "availability": report})
}

// UpdatePaper edits a paper's title or publishes it.
func (h *Handler) UpdatePaper(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var paper models.TestPaper
	if err := h.DB.First(&paper, id).Error; err != nil {
		dbError(c, err, "paper")
		return
	}

	var payload struct {
		Title       *string `json:"title"`
		Status      *string `json:"status"`
		DurationMin *int    `json:"duration_min"`
		Notes       *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	updates := map[string]any{}
	if payload.Title != nil && strings.TrimSpace(*payload.Title) != "" {
		updates["title"] = strings.TrimSpace(*payload.Title)
	}
	if payload.Status != nil {
		status := models.Status(*payload.Status)
		if status != models.StatusDraft && status != models.StatusPublished {
			badRequestf(c, "status must be draft or published")
			return
		}
		// The quality gate. Publishing is the moment a paper becomes something a
		// student sees, so it is refused until the paper passes, with the reasons
		// returned rather than a bare rejection.
		if status == models.StatusPublished && !paper.Publishable() {
			c.JSON(http.StatusConflict, gin.H{
				"error": fmt.Sprintf("this paper cannot be published: %s", paper.QAExplanation()),
				"data": gin.H{
					"quality_status": paper.QualityStatus,
					"quality_score":  paper.QualityScore,
					"blocking":       paper.Blocking(),
					"issues":         paper.Issues(),
					"next_step": "resolve the blocking issues, or re-run quality review with " +
						"POST /api/v1/papers/" + strconv.FormatUint(uint64(paper.ID), 10) + "/qa",
				},
			})
			return
		}
		updates["status"] = status
	}
	if payload.DurationMin != nil {
		updates["duration_min"] = *payload.DurationMin
	}
	if payload.Notes != nil {
		updates["notes"] = *payload.Notes
	}

	if len(updates) > 0 {
		if err := h.DB.Model(&paper).Updates(updates).Error; err != nil {
			serverError(c, err)
			return
		}
	}
	ok(c, paper)
}

// DeletePaper removes a paper. Its questions stay in the warehouse.
func (h *Handler) DeletePaper(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("test_paper_id = ?", id).
			Delete(&models.TestPaperItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.TestPaper{}, id).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// ExportPaper renders a paper as markdown, with or without the answer key.
//
// Markdown because it is readable as-is, pastes into a document, and converts to
// PDF with any standard tool, without this service growing a rendering stack.
func (h *Handler) ExportPaper(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var paper models.TestPaper
	err := h.DB.
		Preload("Exam").
		Preload("Items", func(db *gorm.DB) *gorm.DB {
			return db.Order("test_paper_items.sequence_no asc")
		}).
		Preload("Items.Question").
		Preload("Items.Question.Options", func(db *gorm.DB) *gorm.DB {
			return db.Order("question_options.order_index asc")
		}).
		First(&paper, id).Error
	if err != nil {
		dbError(c, err, "paper")
		return
	}

	// Export is the other way a paper leaves the building, so it is gated too.
	// A failed paper is refused outright; anything not yet passing can only be
	// exported when the caller explicitly asks for a draft, and then it is
	// stamped so nobody mistakes it for deliverable.
	draft := boolQuery(c, "draft", false)
	if paper.QualityStatus == models.QualityFailed {
		c.JSON(http.StatusConflict, gin.H{
			"error": "this paper failed quality review and cannot be exported",
			"data": gin.H{
				"quality_status": paper.QualityStatus,
				"blocking":       paper.Blocking(),
			},
		})
		return
	}
	if !paper.Publishable() && !draft {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("this paper is not cleared for delivery: %s", paper.QAExplanation()),
			"data": gin.H{
				"quality_status": paper.QualityStatus,
				"issues":         paper.Issues(),
				"next_step":      "resolve the issues, or add ?draft=true to export it for internal review",
			},
		})
		return
	}

	withAnswers := boolQuery(c, "answers", true)
	withExplanations := boolQuery(c, "explanations", withAnswers)

	var b strings.Builder
	examName := ""
	if paper.Exam != nil {
		examName = paper.Exam.Name
	}

	fmt.Fprintf(&b, "# %s\n\n", paper.Title)
	if !paper.Publishable() {
		// A draft export carries its status on its face. An unmarked draft that
		// escapes into a client's hands is exactly the failure this whole gate
		// exists to prevent.
		fmt.Fprintf(&b, "> **DRAFT - NOT CLEARED FOR DELIVERY.** %s\n\n", paper.QAExplanation())
	}
	if examName != "" {
		fmt.Fprintf(&b, "**Exam:** %s  \n", examName)
	}
	fmt.Fprintf(&b, "**Questions:** %d  \n", paper.TotalQuestions)
	if paper.DurationMin > 0 {
		fmt.Fprintf(&b, "**Time allowed:** %d minutes  \n", paper.DurationMin)
	}
	fmt.Fprintf(&b, "**Maximum marks:** %g  \n", paper.TotalMarks)
	if paper.NegativeMarks > 0 {
		fmt.Fprintf(&b, "**Negative marking:** %g per wrong answer  \n", paper.NegativeMarks)
	}
	b.WriteString("\n---\n\n")

	currentSection := ""
	type answerLine struct {
		number      int
		label       string
		explanation string
	}
	var answers []answerLine

	for _, item := range paper.Items {
		if item.SectionName != currentSection {
			currentSection = item.SectionName
			fmt.Fprintf(&b, "\n## %s\n\n", currentSection)
		}
		if item.Question == nil {
			continue
		}

		fmt.Fprintf(&b, "**%d.** %s\n\n", item.SequenceNo, item.Question.QuestionText)

		correct := ""
		for _, opt := range item.Question.Options {
			fmt.Fprintf(&b, "   (%s) %s\n", opt.Label, opt.Text)
			if opt.IsCorrect {
				correct = opt.Label
			}
		}
		b.WriteString("\n")

		if correct != "" || item.Question.Explanation != "" {
			answers = append(answers, answerLine{
				number:      item.SequenceNo,
				label:       correct,
				explanation: item.Question.Explanation,
			})
		}
	}

	if withAnswers && len(answers) > 0 {
		b.WriteString("\n---\n\n## Answer Key\n\n")
		for _, a := range answers {
			if a.label == "" {
				continue
			}
			fmt.Fprintf(&b, "%d. (%s)  ", a.number, a.label)
		}
		b.WriteString("\n")

		if withExplanations {
			b.WriteString("\n## Answers with Explanations\n\n")
			for _, a := range answers {
				if a.explanation == "" {
					continue
				}
				if a.label != "" {
					fmt.Fprintf(&b, "**%d.** Option (%s) is correct. %s\n\n", a.number, a.label, a.explanation)
				} else {
					fmt.Fprintf(&b, "**%d.** %s\n\n", a.number, a.explanation)
				}
			}
		}
	}

	if c.Query("format") == "json" {
		ok(c, gin.H{"markdown": b.String(), "paper": paper})
		return
	}

	filename := slug(paper.Title)
	if filename == "" {
		filename = fmt.Sprintf("paper-%d", paper.ID)
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename+".md"))
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(b.String()))
}
