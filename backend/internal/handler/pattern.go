package handler

import (
	"encoding/json"
	"math"
	"strings"

	"mockcreator/internal/models"
	"mockcreator/internal/pipeline"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// patternSectionPayload is one section in a manually entered pattern.
type patternSectionPayload struct {
	ID               uint                  `json:"id,omitempty"`
	SubjectID        *uint                 `json:"subject_id"`
	Name             string                `json:"name"`
	OrderIndex       int                   `json:"order_index"`
	QuestionCount    int                   `json:"question_count"`
	MarksPerQuestion float64               `json:"marks_per_question"`
	NegativeMarks    float64               `json:"negative_marks"`
	DifficultyMix    *models.DifficultyMix `json:"difficulty_mix,omitempty"`
}

// patternPayload is the create/update body for a pattern.
type patternPayload struct {
	Name             string                  `json:"name"`
	DurationMin      int                     `json:"duration_min"`
	MarksPerQuestion float64                 `json:"marks_per_question"`
	NegativeMarks    float64                 `json:"negative_marks"`
	OptionCount      int                     `json:"option_count"`
	TotalQuestions   int                     `json:"total_questions"`
	Activate         bool                    `json:"activate"`
	Notes            string                  `json:"notes"`
	Sections         []patternSectionPayload `json:"sections"`
}

// ListPatterns returns every pattern version for an exam, newest first.
func (h *Handler) ListPatterns(c *gin.Context) {
	examID, valid := idParam(c)
	if !valid {
		return
	}
	var patterns []models.ExamPattern
	err := h.DB.Where("exam_id = ?", examID).
		Preload("Sections", func(db *gorm.DB) *gorm.DB {
			return db.Order("pattern_sections.order_index asc")
		}).
		Preload("Sections.Subject").
		Order("version desc").
		Find(&patterns).Error
	if err != nil {
		serverError(c, err)
		return
	}
	list(c, patterns, nil)
}

// GetPattern returns one pattern.
func (h *Handler) GetPattern(c *gin.Context) {
	patternID, valid := namedIDParam(c, "patternId")
	if !valid {
		return
	}
	var pattern models.ExamPattern
	err := h.DB.
		Preload("Sections", func(db *gorm.DB) *gorm.DB {
			return db.Order("pattern_sections.order_index asc")
		}).
		Preload("Sections.Subject").
		First(&pattern, patternID).Error
	if err != nil {
		dbError(c, err, "pattern")
		return
	}
	ok(c, pattern)
}

// CreatePattern saves a hand-entered pattern as a new version.
//
// Question counts are the source of truth; weightage is computed from them so
// the two can never disagree.
func (h *Handler) CreatePattern(c *gin.Context) {
	examID, valid := idParam(c)
	if !valid {
		return
	}
	var exam models.Exam
	if err := h.DB.First(&exam, examID).Error; err != nil {
		dbError(c, err, "exam")
		return
	}

	var payload patternPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	if len(payload.Sections) == 0 {
		badRequestf(c, "a pattern needs at least one section")
		return
	}

	pattern, sections, err := h.buildPattern(examID, payload)
	if err != nil {
		badRequest(c, err)
		return
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		var maxVersion int
		if err := tx.Model(&models.ExamPattern{}).Where("exam_id = ?", examID).
			Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		pattern.Version = maxVersion + 1

		if pattern.IsActive {
			if err := tx.Model(&models.ExamPattern{}).Where("exam_id = ?", examID).
				Update("is_active", false).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&pattern).Error; err != nil {
			return err
		}
		for i := range sections {
			sections[i].ExamPatternID = pattern.ID
		}
		if err := tx.Create(&sections).Error; err != nil {
			return err
		}
		pattern.Sections = sections

		// Keep the exam's subject list in step with its pattern.
		for order, section := range sections {
			if section.SubjectID == nil {
				continue
			}
			link := models.ExamSubject{
				ExamID:      examID,
				SubjectID:   *section.SubjectID,
				DisplayName: section.Name,
				OrderIndex:  order,
			}
			if err := tx.Where("exam_id = ? AND subject_id = ?", examID, *section.SubjectID).
				FirstOrCreate(&link, models.ExamSubject{
					ExamID: examID, SubjectID: *section.SubjectID,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(c, err)
		return
	}

	created(c, pattern)
}

// UpdatePattern replaces a pattern's details and sections in place.
func (h *Handler) UpdatePattern(c *gin.Context) {
	examID, valid := namedIDParam(c, "id")
	if !valid {
		return
	}
	patternID, valid := namedIDParam(c, "patternId")
	if !valid {
		return
	}

	var existing models.ExamPattern
	if err := h.DB.Where("exam_id = ?", examID).First(&existing, patternID).Error; err != nil {
		dbError(c, err, "pattern")
		return
	}

	var payload patternPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	pattern, sections, err := h.buildPattern(examID, payload)
	if err != nil {
		badRequest(c, err)
		return
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"name":               pattern.Name,
			"duration_min":       pattern.DurationMin,
			"marks_per_question": pattern.MarksPerQuestion,
			"negative_marks":     pattern.NegativeMarks,
			"option_count":       pattern.OptionCount,
			"total_questions":    pattern.TotalQuestions,
			"total_marks":        pattern.TotalMarks,
			"notes":              pattern.Notes,
		}
		// Editing a derived pattern makes it a user-owned one; pretending it is
		// still machine-derived would misreport its confidence.
		if existing.Source == models.PatternDerived {
			updates["source"] = models.PatternManual
			updates["confidence"] = 1.0
		}
		if payload.Activate {
			if err := tx.Model(&models.ExamPattern{}).Where("exam_id = ?", examID).
				Update("is_active", false).Error; err != nil {
				return err
			}
			updates["is_active"] = true
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}

		if err := tx.Where("exam_pattern_id = ?", existing.ID).
			Delete(&models.PatternSection{}).Error; err != nil {
			return err
		}
		for i := range sections {
			sections[i].ExamPatternID = existing.ID
		}
		if len(sections) > 0 {
			if err := tx.Create(&sections).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(c, err)
		return
	}

	h.GetPattern(c)
}

// buildPattern converts a payload into model structs, deriving weightage and
// totals so the stored pattern is internally consistent.
func (h *Handler) buildPattern(examID uint, payload patternPayload) (models.ExamPattern, []models.PatternSection, error) {
	marks := payload.MarksPerQuestion
	if marks <= 0 {
		marks = 1
	}

	total := 0
	for _, s := range payload.Sections {
		if s.QuestionCount > 0 {
			total += s.QuestionCount
		}
	}
	if total == 0 {
		total = payload.TotalQuestions
	}

	pattern := models.ExamPattern{
		ExamID:           examID,
		Name:             firstNonBlank(payload.Name, "Pattern"),
		Source:           models.PatternManual,
		Confidence:       1,
		TotalQuestions:   total,
		DurationMin:      payload.DurationMin,
		MarksPerQuestion: marks,
		NegativeMarks:    payload.NegativeMarks,
		OptionCount:      payload.OptionCount,
		IsActive:         payload.Activate,
		Notes:            strings.TrimSpace(payload.Notes),
	}

	sections := make([]models.PatternSection, 0, len(payload.Sections))
	for i, s := range payload.Sections {
		if s.QuestionCount <= 0 {
			continue
		}
		sectionMarks := s.MarksPerQuestion
		if sectionMarks <= 0 {
			sectionMarks = marks
		}
		negative := s.NegativeMarks
		if negative <= 0 {
			negative = payload.NegativeMarks
		}

		weightage := 0.0
		if total > 0 {
			weightage = math.Round(float64(s.QuestionCount)/float64(total)*10000) / 100
		}

		section := models.PatternSection{
			SubjectID:        s.SubjectID,
			Name:             firstNonBlank(s.Name, "Section"),
			OrderIndex:       i,
			QuestionCount:    s.QuestionCount,
			Weightage:        weightage,
			MarksPerQuestion: sectionMarks,
			NegativeMarks:    negative,
		}

		mix := models.DefaultMixFor(models.PaperBalanced)
		if s.DifficultyMix != nil {
			mix = s.DifficultyMix.Normalized()
		}
		if encoded, err := json.Marshal(mix); err == nil {
			section.DifficultyMix = encoded
		}

		sections = append(sections, section)
		pattern.TotalMarks += float64(s.QuestionCount) * sectionMarks
	}

	return pattern, sections, nil
}

// ActivatePattern makes one version the exam's current pattern.
func (h *Handler) ActivatePattern(c *gin.Context) {
	examID, valid := namedIDParam(c, "id")
	if !valid {
		return
	}
	patternID, valid := namedIDParam(c, "patternId")
	if !valid {
		return
	}

	var pattern models.ExamPattern
	if err := h.DB.Where("exam_id = ?", examID).First(&pattern, patternID).Error; err != nil {
		dbError(c, err, "pattern")
		return
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.ExamPattern{}).Where("exam_id = ?", examID).
			Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Model(&pattern).Update("is_active", true).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"activated": pattern.ID, "version": pattern.Version})
}

// DeletePattern removes a pattern version.
func (h *Handler) DeletePattern(c *gin.Context) {
	examID, valid := namedIDParam(c, "id")
	if !valid {
		return
	}
	patternID, valid := namedIDParam(c, "patternId")
	if !valid {
		return
	}

	var pattern models.ExamPattern
	if err := h.DB.Where("exam_id = ?", examID).First(&pattern, patternID).Error; err != nil {
		dbError(c, err, "pattern")
		return
	}

	var papers int64
	h.DB.Model(&models.TestPaper{}).Where("pattern_id = ?", patternID).Count(&papers)
	if papers > 0 {
		conflict(c, "this pattern was used to build papers; delete those first or keep it for the record")
		return
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("exam_pattern_id = ?", patternID).
			Delete(&models.PatternSection{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.ExamPattern{}, patternID).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": patternID})
}

// analyzePayload asks the engine to infer a pattern from real papers.
type analyzePayload struct {
	DocumentIDs []uint `json:"document_ids"`
	Name        string `json:"name"`
	Activate    *bool  `json:"activate"`
	RoundTo     int    `json:"round_to"`
	IngestAfter *bool  `json:"ingest_after"`
}

// AnalyzePattern queues pattern inference from one or more question papers.
//
// This is the path for a user who does not know their exam's blueprint: drop in
// a past paper and let the engine work it out.
func (h *Handler) AnalyzePattern(c *gin.Context) {
	examID, valid := idParam(c)
	if !valid {
		return
	}
	var exam models.Exam
	if err := h.DB.First(&exam, examID).Error; err != nil {
		dbError(c, err, "exam")
		return
	}

	var payload analyzePayload
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		badRequest(c, err)
		return
	}

	// Confirm there is something to learn from before creating a job that would
	// only fail.
	countQuery := h.DB.Model(&models.Document{})
	if len(payload.DocumentIDs) > 0 {
		countQuery = countQuery.Where("id IN ?", payload.DocumentIDs)
	} else {
		countQuery = countQuery.Where("exam_id = ? AND kind = ?", examID, models.KindQuestionPaper)
	}
	var available int64
	if err := countQuery.Count(&available).Error; err != nil {
		serverError(c, err)
		return
	}
	if available == 0 {
		badRequestf(c, "no question papers found to analyse; upload at least one for this exam first")
		return
	}

	params := pipeline.PatternParams{
		DocumentIDs: payload.DocumentIDs,
		Name:        payload.Name,
		Activate:    true,
		RoundTo:     payload.RoundTo,
		IngestAfter: true,
	}
	if payload.Activate != nil {
		params.Activate = *payload.Activate
	}
	if payload.IngestAfter != nil {
		params.IngestAfter = *payload.IngestAfter
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		serverError(c, err)
		return
	}

	job := &models.Job{
		Type:   models.JobPatternAnalysis,
		ExamID: &examID,
		Params: encoded,
	}
	if err := h.Jobs.Enqueue(job); err != nil {
		serverError(c, err)
		return
	}

	accepted(c, gin.H{"job": job, "documents": available})
}
