package handler

import (
	"errors"
	"strings"

	"mockcreator/internal/models"
	"mockcreator/internal/pipeline"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// examPayload is the create/update body for an exam.
type examPayload struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Status      string `json:"status"`
}

// ListExams returns exams with a summary of what each one holds.
func (h *Handler) ListExams(c *gin.Context) {
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.Exam{})
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(code) LIKE ?", like, like)
	}
	query = filterQuery(c, query, map[string]string{"status": "status"})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	var exams []models.Exam
	if err := query.Order("created_at desc").Limit(size).Offset(offset).Find(&exams).Error; err != nil {
		serverError(c, err)
		return
	}

	// Each row carries its own counts so the exam list is useful at a glance
	// rather than needing a request per exam.
	type examRow struct {
		models.Exam
		Summary examSummary `json:"summary"`
	}
	rows := make([]examRow, 0, len(exams))
	for _, exam := range exams {
		rows = append(rows, examRow{Exam: exam, Summary: h.summarizeExam(exam.ID)})
	}

	list(c, rows, makePage(page, size, total))
}

// examSummary counts the material and output attached to an exam.
type examSummary struct {
	Subjects      int64 `json:"subjects"`
	Documents     int64 `json:"documents"`
	Questions     int64 `json:"questions"`
	OwnQuestions  int64 `json:"own_questions"`
	Borrowed      int64 `json:"borrowed_questions"`
	Answered      int64 `json:"answered_questions"`
	Papers        int64 `json:"papers"`
	Associations  int64 `json:"associations"`
	HasPattern    bool  `json:"has_pattern"`
	PatternTotal  int   `json:"pattern_total_questions"`
	ActivePattern *uint `json:"active_pattern_id,omitempty"`
}

func (h *Handler) summarizeExam(examID uint) examSummary {
	var out examSummary

	h.DB.Model(&models.ExamSubject{}).Where("exam_id = ?", examID).Count(&out.Subjects)
	h.DB.Model(&models.Document{}).Where("exam_id = ?", examID).Count(&out.Documents)
	h.DB.Model(&models.TestPaper{}).Where("exam_id = ?", examID).Count(&out.Papers)
	h.DB.Model(&models.ExamAssociation{}).Where("exam_id = ?", examID).Count(&out.Associations)

	h.DB.Model(&models.QuestionExamLink{}).Where("exam_id = ?", examID).Count(&out.Questions)
	h.DB.Model(&models.QuestionExamLink{}).
		Where("exam_id = ? AND link_type = ?", examID, models.LinkDirect).Count(&out.OwnQuestions)
	out.Borrowed = out.Questions - out.OwnQuestions

	h.DB.Table("question_exam_links AS l").
		Joins("JOIN questions q ON q.id = l.question_id AND q.deleted_at IS NULL").
		Where("l.exam_id = ? AND q.has_answer = ?", examID, true).
		Count(&out.Answered)

	var pattern models.ExamPattern
	if err := h.DB.Where("exam_id = ? AND is_active = ?", examID, true).First(&pattern).Error; err == nil {
		out.HasPattern = true
		out.PatternTotal = pattern.TotalQuestions
		id := pattern.ID
		out.ActivePattern = &id
	}

	return out
}

// CreateExam registers a new exam.
func (h *Handler) CreateExam(c *gin.Context) {
	var payload examPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Name == "" {
		badRequestf(c, "name is required")
		return
	}

	code := slug(payload.Code)
	if code == "" {
		code = slug(payload.Name)
	}
	if code == "" {
		badRequestf(c, "could not derive a code from the name; provide one explicitly")
		return
	}

	exam := models.Exam{
		Name:        payload.Name,
		Code:        code,
		Description: strings.TrimSpace(payload.Description),
		Language:    firstNonBlank(payload.Language, "en"),
		Status:      models.Status(firstNonBlank(payload.Status, string(models.StatusDraft))),
	}
	if err := h.DB.Create(&exam).Error; err != nil {
		dbError(c, err, "exam code "+code)
		return
	}
	created(c, exam)
}

// GetExam returns one exam with its subjects, patterns and associations.
func (h *Handler) GetExam(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var exam models.Exam
	err := h.DB.
		Preload("ExamSubjects", func(db *gorm.DB) *gorm.DB {
			return db.Order("exam_subjects.order_index asc")
		}).
		Preload("ExamSubjects.Subject").
		Preload("Patterns", func(db *gorm.DB) *gorm.DB {
			return db.Order("exam_patterns.version desc")
		}).
		Preload("Patterns.Sections", func(db *gorm.DB) *gorm.DB {
			return db.Order("pattern_sections.order_index asc")
		}).
		Preload("Patterns.Sections.Subject").
		Preload("Associations").
		Preload("Associations.SourceExam").
		Preload("Associations.Subject").
		First(&exam, id).Error
	if err != nil {
		dbError(c, err, "exam")
		return
	}

	ok(c, gin.H{"exam": exam, "summary": h.summarizeExam(exam.ID)})
}

// UpdateExam edits an exam's details.
func (h *Handler) UpdateExam(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var exam models.Exam
	if err := h.DB.First(&exam, id).Error; err != nil {
		dbError(c, err, "exam")
		return
	}

	var payload examPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	updates := map[string]any{}
	if name := strings.TrimSpace(payload.Name); name != "" {
		updates["name"] = name
	}
	if code := slug(payload.Code); code != "" {
		updates["code"] = code
	}
	if payload.Description != "" {
		updates["description"] = strings.TrimSpace(payload.Description)
	}
	if payload.Language != "" {
		updates["language"] = payload.Language
	}
	if payload.Status != "" {
		updates["status"] = payload.Status
	}
	if len(updates) == 0 {
		ok(c, exam)
		return
	}

	if err := h.DB.Model(&exam).Updates(updates).Error; err != nil {
		dbError(c, err, "exam")
		return
	}
	ok(c, exam)
}

// DeleteExam removes an exam. Its questions survive in the warehouse, because
// they belong to a subject; only the exam's own links and pattern go away.
func (h *Handler) DeleteExam(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("exam_id = ?", id).Delete(&models.QuestionExamLink{}).Error; err != nil {
			return err
		}
		// Join rows are hard-deleted so their codes and pairs become reusable.
		if err := tx.Unscoped().Where("exam_id = ? OR source_exam_id = ?", id, id).
			Delete(&models.ExamAssociation{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("exam_id = ?", id).
			Delete(&models.ExamSubject{}).Error; err != nil {
			return err
		}
		var patternIDs []uint
		if err := tx.Model(&models.ExamPattern{}).Where("exam_id = ?", id).
			Pluck("id", &patternIDs).Error; err != nil {
			return err
		}
		if len(patternIDs) > 0 {
			if err := tx.Where("exam_pattern_id IN ?", patternIDs).
				Delete(&models.PatternSection{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("exam_id = ?", id).Delete(&models.ExamPattern{}).Error; err != nil {
			return err
		}
		// Documents and questions keep their history but lose the exam pointer.
		if err := tx.Model(&models.Document{}).Where("exam_id = ?", id).
			Update("exam_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Question{}).Where("origin_exam_id = ?", id).
			Update("origin_exam_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Exam{}, id).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// --- exam subjects ---------------------------------------------------------

type examSubjectPayload struct {
	SubjectID   uint   `json:"subject_id"`
	DisplayName string `json:"display_name"`
	OrderIndex  int    `json:"order_index"`
}

// ListExamSubjects returns the subjects an exam covers.
func (h *Handler) ListExamSubjects(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var rows []models.ExamSubject
	if err := h.DB.Preload("Subject").Where("exam_id = ?", id).
		Order("order_index asc").Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	list(c, rows, nil)
}

// AddExamSubject attaches a subject to an exam.
func (h *Handler) AddExamSubject(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var payload examSubjectPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	if payload.SubjectID == 0 {
		badRequestf(c, "subject_id is required")
		return
	}

	var subject models.Subject
	if err := h.DB.First(&subject, payload.SubjectID).Error; err != nil {
		dbError(c, err, "subject")
		return
	}

	row := models.ExamSubject{
		ExamID:      id,
		SubjectID:   payload.SubjectID,
		DisplayName: strings.TrimSpace(payload.DisplayName),
		OrderIndex:  payload.OrderIndex,
	}
	err := h.DB.Where("exam_id = ? AND subject_id = ?", id, payload.SubjectID).
		FirstOrCreate(&row, row).Error
	if err != nil {
		dbError(c, err, "exam subject")
		return
	}
	row.Subject = &subject
	created(c, row)
}

// RemoveExamSubject detaches a subject from an exam.
func (h *Handler) RemoveExamSubject(c *gin.Context) {
	examID, valid := namedIDParam(c, "id")
	if !valid {
		return
	}
	subjectID, valid := namedIDParam(c, "subjectId")
	if !valid {
		return
	}
	// Hard delete: this is a pure join row with no history worth keeping, and a
	// soft-deleted one would block re-attaching the same subject later.
	if err := h.DB.Unscoped().Where("exam_id = ? AND subject_id = ?", examID, subjectID).
		Delete(&models.ExamSubject{}).Error; err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": true})
}

// --- associations ----------------------------------------------------------

type associationPayload struct {
	SourceExamID uint     `json:"source_exam_id"`
	SubjectID    *uint    `json:"subject_id"`
	Similarity   *float64 `json:"similarity"`
	MaxSharePct  *float64 `json:"max_share_pct"`
	Enabled      *bool    `json:"enabled"`
	Note         string   `json:"note"`
}

// ListAssociations returns the exams this one borrows from.
func (h *Handler) ListAssociations(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var rows []models.ExamAssociation
	if err := h.DB.Preload("SourceExam").Preload("Subject").
		Where("exam_id = ?", id).Order("created_at asc").Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	list(c, rows, nil)
}

// CreateAssociation lets this exam draw on another exam's questions.
//
// Creating the association immediately materialises the borrowed links, so the
// new exam's warehouse view and paper generation reflect it right away.
func (h *Handler) CreateAssociation(c *gin.Context) {
	examID, valid := idParam(c)
	if !valid {
		return
	}
	var payload associationPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	if payload.SourceExamID == 0 {
		badRequestf(c, "source_exam_id is required")
		return
	}
	if payload.SourceExamID == examID {
		badRequestf(c, "an exam cannot borrow from itself")
		return
	}

	var source models.Exam
	if err := h.DB.First(&source, payload.SourceExamID).Error; err != nil {
		dbError(c, err, "source exam")
		return
	}
	if payload.SubjectID != nil {
		var subject models.Subject
		if err := h.DB.First(&subject, *payload.SubjectID).Error; err != nil {
			dbError(c, err, "subject")
			return
		}
	}

	assoc := models.ExamAssociation{
		ExamID:       examID,
		SourceExamID: payload.SourceExamID,
		SubjectID:    payload.SubjectID,
		Similarity:   0.8,
		Enabled:      true,
		Note:         strings.TrimSpace(payload.Note),
	}
	if payload.Similarity != nil {
		assoc.Similarity = clampUnit(*payload.Similarity)
	}
	if payload.MaxSharePct != nil {
		assoc.MaxSharePct = *payload.MaxSharePct
	}
	if payload.Enabled != nil {
		assoc.Enabled = *payload.Enabled
	}

	// Guard the nullable-subject case the unique index cannot cover.
	dup := h.DB.Where("exam_id = ? AND source_exam_id = ?", examID, payload.SourceExamID)
	if payload.SubjectID == nil {
		dup = dup.Where("subject_id IS NULL")
	} else {
		dup = dup.Where("subject_id = ?", *payload.SubjectID)
	}
	var existing models.ExamAssociation
	if err := dup.First(&existing).Error; err == nil {
		conflict(c, "this association already exists")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		serverError(c, err)
		return
	}

	if err := h.DB.Create(&assoc).Error; err != nil {
		dbError(c, err, "association")
		return
	}
	if err := pipeline.RebuildExamLinks(h.DB, examID); err != nil {
		serverError(c, err)
		return
	}

	assoc.SourceExam = &source
	created(c, gin.H{"association": assoc, "summary": h.summarizeExam(examID)})
}

// UpdateAssociation edits similarity, cap, enablement or note.
func (h *Handler) UpdateAssociation(c *gin.Context) {
	examID, valid := namedIDParam(c, "id")
	if !valid {
		return
	}
	assocID, valid := namedIDParam(c, "assocId")
	if !valid {
		return
	}

	var assoc models.ExamAssociation
	if err := h.DB.Where("exam_id = ?", examID).First(&assoc, assocID).Error; err != nil {
		dbError(c, err, "association")
		return
	}

	var payload associationPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	updates := map[string]any{}
	if payload.Similarity != nil {
		updates["similarity"] = clampUnit(*payload.Similarity)
	}
	if payload.MaxSharePct != nil {
		updates["max_share_pct"] = *payload.MaxSharePct
	}
	if payload.Enabled != nil {
		updates["enabled"] = *payload.Enabled
	}
	if payload.Note != "" {
		updates["note"] = strings.TrimSpace(payload.Note)
	}
	if len(updates) > 0 {
		if err := h.DB.Model(&assoc).Updates(updates).Error; err != nil {
			serverError(c, err)
			return
		}
	}

	// Links follow the association, so rebuild whenever it changes.
	if err := pipeline.RebuildExamLinks(h.DB, examID); err != nil {
		serverError(c, err)
		return
	}
	ok(c, assoc)
}

// DeleteAssociation removes an association and the links it produced.
func (h *Handler) DeleteAssociation(c *gin.Context) {
	examID, valid := namedIDParam(c, "id")
	if !valid {
		return
	}
	assocID, valid := namedIDParam(c, "assocId")
	if !valid {
		return
	}

	// Hard delete so the same association can be recreated later.
	if err := h.DB.Unscoped().Where("exam_id = ?", examID).
		Delete(&models.ExamAssociation{}, assocID).Error; err != nil {
		serverError(c, err)
		return
	}
	if err := pipeline.DeleteExamLinksForAssociation(h.DB, assocID); err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": assocID, "summary": h.summarizeExam(examID)})
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
