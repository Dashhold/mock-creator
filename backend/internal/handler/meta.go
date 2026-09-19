package handler

import (
	"time"

	"mockcreator/internal/models"

	"github.com/gin-gonic/gin"
)

// Capabilities reports what the engine can accept right now.
//
// The upload form reads this instead of hard-coding a format list, so what the
// UI offers always matches what the converter actually has installed.
func (h *Handler) Capabilities(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, 10*time.Second)
	defer cancel()

	payload := gin.H{
		"upload": gin.H{
			"max_bytes":          h.Cfg.Upload.MaxBytes,
			"allowed_extensions": h.Cfg.Upload.AllowedExtensions,
		},
		"document_kinds": []models.DocumentKind{
			models.KindQuestionPaper, models.KindAnswerKey, models.KindSolution,
			models.KindBook, models.KindNotes, models.KindSyllabus, models.KindOther,
		},
		"paper_types": []models.PaperType{
			models.PaperBalanced, models.PaperEasy, models.PaperTough,
			models.PaperPrevious, models.PaperRecent, models.PaperSpeed,
			models.PaperMixed, models.PaperCustom,
		},
		"difficulties":     models.AllDifficulties(),
		"review_threshold": h.Cfg.Review.ScoreThreshold,
		"defaults": gin.H{
			"engine":     h.Cfg.Converter.DefaultEngine,
			"ocr_mode":   h.Cfg.Converter.DefaultOCRMode,
			"ocr_engine": h.Cfg.Converter.DefaultOCREngine,
			"table_mode": h.Cfg.Converter.TableMode,
		},
		// The UI needs to know whether model-backed review ran, so a paper that
		// only passed the deterministic gates is never presented as if a model
		// had also read it.
		"llm": gin.H{
			"enabled":           h.Cfg.LLM.Enabled(),
			"model":             h.Cfg.LLM.Model,
			"audit_questions":   h.Cfg.LLM.Enabled() && h.Cfg.LLM.AuditQuestions,
			"audit_papers":      h.Cfg.LLM.Enabled() && h.Cfg.LLM.AuditPapers,
			"repair_boundaries": h.Cfg.LLM.Enabled() && h.Cfg.LLM.RepairBoundaries,
			"judge_duplicates":  h.Cfg.LLM.Enabled() && h.Cfg.LLM.JudgeDuplicates,
		},
		"quality": gin.H{
			"min_extraction_confidence": h.Cfg.Converter.MinExtractionConfidence,
		},
	}

	// The converter may still be warming up; report that rather than failing.
	caps, err := h.Converter.Capabilities(ctx)
	if err != nil {
		payload["converter"] = gin.H{"available": false, "error": err.Error()}
		ok(c, payload)
		return
	}
	payload["converter"] = gin.H{
		"available":      true,
		"engines":        caps.Engines,
		"engine_version": caps.EngineVersion,
		"extensions":     caps.Extensions,
		"ocr_engines":    caps.OCREngines,
		"ocr_modes":      caps.OCRModes,
		"table_modes":    caps.TableModes,
		"max_upload":     caps.MaxUploadBytes,
		"concurrency":    caps.Concurrency,
	}
	ok(c, payload)
}

// Overview is the dashboard payload: counts plus the live job feed.
func (h *Handler) Overview(c *gin.Context) {
	type counts struct {
		Exams             int64 `json:"exams"`
		Subjects          int64 `json:"subjects"`
		Topics            int64 `json:"topics"`
		Documents         int64 `json:"documents"`
		DocumentsReady    int64 `json:"documents_ready"`
		DocumentsFailed   int64 `json:"documents_failed"`
		Questions         int64 `json:"questions"`
		QuestionsAnswered int64 `json:"questions_answered"`
		QuestionsPending  int64 `json:"questions_pending"`
		Papers            int64 `json:"papers"`
		Patterns          int64 `json:"patterns"`
		Associations      int64 `json:"associations"`
	}

	var out counts
	h.DB.Model(&models.Exam{}).Count(&out.Exams)
	h.DB.Model(&models.Subject{}).Count(&out.Subjects)
	h.DB.Model(&models.Topic{}).Count(&out.Topics)
	h.DB.Model(&models.Document{}).Count(&out.Documents)
	h.DB.Model(&models.Document{}).Where("status = ?", models.StatusCompleted).Count(&out.DocumentsReady)
	h.DB.Model(&models.Document{}).Where("status = ?", models.StatusFailed).Count(&out.DocumentsFailed)
	h.DB.Model(&models.Question{}).Count(&out.Questions)
	h.DB.Model(&models.Question{}).Where("has_answer = ?", true).Count(&out.QuestionsAnswered)
	h.DB.Model(&models.Question{}).Where("status = ?", models.StatusPending).Count(&out.QuestionsPending)
	h.DB.Model(&models.TestPaper{}).Count(&out.Papers)
	h.DB.Model(&models.ExamPattern{}).Count(&out.Patterns)
	h.DB.Model(&models.ExamAssociation{}).Count(&out.Associations)

	var activeJobs []models.Job
	h.DB.Preload("Document").Preload("Exam").
		Where("status IN ?", []models.Status{models.StatusQueued, models.StatusProcessing}).
		Order("created_at asc").Limit(10).Find(&activeJobs)
	for i := range activeJobs {
		activeJobs[i].Result = nil
	}

	var recentJobs []models.Job
	h.DB.Preload("Document").Preload("Exam").
		Where("status IN ?", []models.Status{models.StatusCompleted, models.StatusFailed}).
		Order("finished_at desc").Limit(8).Find(&recentJobs)
	for i := range recentJobs {
		recentJobs[i].Result = nil
	}

	// Subject-wise totals double as the Warehouse headline.
	type subjectTotal struct {
		SubjectID uint   `json:"subject_id"`
		Name      string `json:"name"`
		Code      string `json:"code"`
		Total     int64  `json:"total"`
		Answered  int64  `json:"answered"`
	}
	var subjectTotals []subjectTotal
	h.DB.Table("questions AS q").
		Select(`q.subject_id, s.name, s.code, COUNT(*) AS total,
		        COUNT(*) FILTER (WHERE q.has_answer) AS answered`).
		Joins("JOIN subjects s ON s.id = q.subject_id").
		Where("q.deleted_at IS NULL").
		Group("q.subject_id, s.name, s.code").
		Order("total desc").
		Scan(&subjectTotals)

	ok(c, gin.H{
		"counts":      out,
		"jobs":        h.countJobs(),
		"active_jobs": activeJobs,
		"recent_jobs": recentJobs,
		"subjects":    subjectTotals,
	})
}

// ConverterHealth proxies the converter's own health check.
func (h *Handler) ConverterHealth(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, 8*time.Second)
	defer cancel()

	health, err := h.Converter.Health(ctx)
	if err != nil {
		c.JSON(503, gin.H{"error": err.Error(), "data": gin.H{"available": false}})
		return
	}
	ok(c, gin.H{
		"available":      true,
		"status":         health.Status,
		"engines":        health.Engines,
		"engine_version": health.EngineVersion,
		"models_ready":   health.ModelsReady,
		"ocr_engines":    health.OCREngines,
		"queue":          health.Queue,
	})
}
