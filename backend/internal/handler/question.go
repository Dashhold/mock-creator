package handler

import (
	"strings"

	"mockcreator/internal/extract"
	"mockcreator/internal/models"
	"mockcreator/internal/pipeline"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// examTag is an exam a question serves, shown in the Warehouse.
type examTag struct {
	ExamID   uint            `json:"exam_id"`
	Name     string          `json:"name"`
	Code     string          `json:"code"`
	LinkType models.LinkType `json:"link_type"`
}

// warehouseQuestion is a question plus the exams it belongs to.
type warehouseQuestion struct {
	models.Question
	ExamTags []examTag `json:"exam_tags"`
}

// ListQuestions is the Warehouse query: every question, filterable, with the
// exams each one serves attached.
//
// Exam tags come from the link table in one extra query rather than a join, so
// a question that serves six exams still appears once.
func (h *Handler) ListQuestions(c *gin.Context) {
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.Question{})

	// Filtering by exam goes through the link table, which is what makes
	// borrowed questions visible under the borrowing exam.
	if examID := uintQuery(c, "exam_id"); examID != nil {
		sub := h.DB.Model(&models.QuestionExamLink{}).
			Select("question_id").Where("exam_id = ?", *examID)
		if linkType := strings.TrimSpace(c.Query("link_type")); linkType != "" && linkType != "all" {
			sub = sub.Where("link_type = ?", linkType)
		}
		query = query.Where("id IN (?)", sub)
	}

	if subjectID := uintQuery(c, "subject_id"); subjectID != nil {
		query = query.Where("subject_id = ?", *subjectID)
	}
	if topicID := uintQuery(c, "topic_id"); topicID != nil {
		query = query.Where("topic_id = ?", *topicID)
	}
	if docID := uintQuery(c, "document_id"); docID != nil {
		query = query.Where("document_id = ?", *docID)
	}
	if year := intQuery(c, "year"); year != nil {
		query = query.Where("year = ?", *year)
	}
	query = filterQuery(c, query, map[string]string{
		"difficulty":     "difficulty",
		"origin":         "origin",
		"status":         "status",
		"type":           "type",
		"quality_status": "quality_status",
		"extract_engine": "extract_engine",
	})
	if c.Query("has_answer") != "" {
		query = query.Where("has_answer = ?", boolQuery(c, "has_answer", true))
	}
	// "deliverable" is the filter that matters commercially: which questions could
	// actually go into a paper handed to a student.
	if c.Query("deliverable") != "" {
		if boolQuery(c, "deliverable", true) {
			query = query.Where("quality_status = ?", models.QualityPass)
		} else {
			query = query.Where("quality_status <> ?", models.QualityPass)
		}
	}
	if code := strings.TrimSpace(c.Query("issue_code")); code != "" {
		query = query.Where("quality_issues::text LIKE ?", "%\"code\":\""+code+"\"%")
	}
	if boolQuery(c, "unsorted", false) {
		var unsorted models.Subject
		if err := h.DB.Where("code = ?", "unsorted").First(&unsorted).Error; err == nil {
			query = query.Where("subject_id = ?", unsorted.ID)
		}
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(question_text) LIKE ?", like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	order := "created_at desc"
	switch c.Query("sort") {
	case "oldest":
		order = "created_at asc"
	case "number":
		order = "question_number asc, id asc"
	case "subject":
		order = "subject_id asc, question_number asc"
	}

	var questions []models.Question
	err := query.
		Preload("Options", func(db *gorm.DB) *gorm.DB {
			return db.Order("question_options.order_index asc")
		}).
		Preload("Subject").
		Preload("Topic").
		Order(order).
		Limit(size).Offset(offset).
		Find(&questions).Error
	if err != nil {
		serverError(c, err)
		return
	}

	rows := h.attachExamTags(questions)
	list(c, rows, makePage(page, size, total))
}

// attachExamTags loads the exam links for a page of questions in one query.
func (h *Handler) attachExamTags(questions []models.Question) []warehouseQuestion {
	rows := make([]warehouseQuestion, 0, len(questions))
	if len(questions) == 0 {
		return rows
	}

	ids := make([]uint, 0, len(questions))
	for _, q := range questions {
		ids = append(ids, q.ID)
	}

	type linkRow struct {
		QuestionID uint
		ExamID     uint
		Name       string
		Code       string
		LinkType   models.LinkType
	}
	var links []linkRow
	h.DB.Table("question_exam_links AS l").
		Select("l.question_id, l.exam_id, e.name, e.code, l.link_type").
		Joins("JOIN exams e ON e.id = l.exam_id AND e.deleted_at IS NULL").
		Where("l.question_id IN ?", ids).
		Order("l.link_type asc, e.name asc").
		Scan(&links)

	byQuestion := make(map[uint][]examTag, len(questions))
	for _, l := range links {
		byQuestion[l.QuestionID] = append(byQuestion[l.QuestionID], examTag{
			ExamID:   l.ExamID,
			Name:     l.Name,
			Code:     l.Code,
			LinkType: l.LinkType,
		})
	}

	for _, q := range questions {
		rows = append(rows, warehouseQuestion{Question: q, ExamTags: byQuestion[q.ID]})
	}
	return rows
}

// WarehouseSummary groups the question bank subject by subject.
//
// This is the Warehouse landing view: what exists, under which subject, how much
// of it is answer-complete, and which exams draw on it.
func (h *Handler) WarehouseSummary(c *gin.Context) {
	examFilter := uintQuery(c, "exam_id")

	type subjectRollup struct {
		SubjectID uint   `json:"subject_id"`
		Name      string `json:"name"`
		Code      string `json:"code"`
		Total     int64  `json:"total"`
		Answered  int64  `json:"answered"`
		Approved  int64  `json:"approved"`
		Pending   int64  `json:"pending"`
		Easy      int64  `json:"easy"`
		Medium    int64  `json:"medium"`
		Hard      int64  `json:"hard"`
		// Deliverable is the count that matters when planning a paper: how many of
		// these questions have actually passed the quality checks. Total alone
		// overstates what is usable, which is how a paper gets planned around
		// questions that cannot be used.
		Deliverable int64 `json:"deliverable"`
		NeedsReview int64 `json:"needs_review"`
		Failed      int64 `json:"failed"`
		Unchecked   int64 `json:"unchecked"`
	}

	query := h.DB.Table("questions AS q").
		Select(`q.subject_id, s.name, s.code,
		        COUNT(*) AS total,
		        COUNT(*) FILTER (WHERE q.has_answer) AS answered,
		        COUNT(*) FILTER (WHERE q.status = 'approved') AS approved,
		        COUNT(*) FILTER (WHERE q.status = 'pending') AS pending,
		        COUNT(*) FILTER (WHERE q.difficulty = 'easy') AS easy,
		        COUNT(*) FILTER (WHERE q.difficulty = 'medium') AS medium,
		        COUNT(*) FILTER (WHERE q.difficulty = 'hard') AS hard,
		        COUNT(*) FILTER (WHERE q.quality_status = 'pass') AS deliverable,
		        COUNT(*) FILTER (WHERE q.quality_status = 'review') AS needs_review,
		        COUNT(*) FILTER (WHERE q.quality_status = 'failed') AS failed,
		        COUNT(*) FILTER (WHERE q.quality_status = 'unchecked') AS unchecked`).
		Joins("JOIN subjects s ON s.id = q.subject_id").
		Where("q.deleted_at IS NULL").
		Group("q.subject_id, s.name, s.code").
		Order("total desc")

	if examFilter != nil {
		query = query.Joins("JOIN question_exam_links l ON l.question_id = q.id").
			Where("l.exam_id = ?", *examFilter)
	}

	var rollups []subjectRollup
	if err := query.Scan(&rollups).Error; err != nil {
		serverError(c, err)
		return
	}

	// Topic breakdown for each subject, so the view can expand.
	type topicRollup struct {
		SubjectID uint   `json:"subject_id"`
		TopicID   uint   `json:"topic_id"`
		Name      string `json:"name"`
		Total     int64  `json:"total"`
	}
	var topics []topicRollup
	h.DB.Table("questions AS q").
		Select("q.subject_id, q.topic_id, t.name, COUNT(*) AS total").
		Joins("JOIN topics t ON t.id = q.topic_id").
		Where("q.deleted_at IS NULL AND q.topic_id IS NOT NULL").
		Group("q.subject_id, q.topic_id, t.name").
		Order("total desc").
		Scan(&topics)

	// Which exams currently reach into each subject.
	type examRollup struct {
		SubjectID uint            `json:"subject_id"`
		ExamID    uint            `json:"exam_id"`
		Name      string          `json:"name"`
		LinkType  models.LinkType `json:"link_type"`
		Total     int64           `json:"total"`
	}
	var exams []examRollup
	h.DB.Table("question_exam_links AS l").
		Select("q.subject_id, l.exam_id, e.name, l.link_type, COUNT(*) AS total").
		Joins("JOIN questions q ON q.id = l.question_id AND q.deleted_at IS NULL").
		Joins("JOIN exams e ON e.id = l.exam_id AND e.deleted_at IS NULL").
		Group("q.subject_id, l.exam_id, e.name, l.link_type").
		Order("total desc").
		Scan(&exams)

	var orphaned int64
	h.DB.Table("questions AS q").
		Where("q.deleted_at IS NULL").
		Where("NOT EXISTS (SELECT 1 FROM question_exam_links l WHERE l.question_id = q.id)").
		Count(&orphaned)

	ok(c, gin.H{
		"subjects": rollups,
		"topics":   topics,
		"exams":    exams,
		// Questions no exam can reach yet: usually material uploaded without an
		// exam attached. Surfacing the count makes that recoverable.
		"unlinked_questions": orphaned,
	})
}

// GetQuestion returns one question in full.
func (h *Handler) GetQuestion(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var question models.Question
	err := h.DB.
		Preload("Options", func(db *gorm.DB) *gorm.DB {
			return db.Order("question_options.order_index asc")
		}).
		Preload("Reviews", func(db *gorm.DB) *gorm.DB {
			return db.Order("quality_reviews.created_at desc")
		}).
		Preload("Subject").
		Preload("Topic").
		Preload("OriginExam").
		First(&question, id).Error
	if err != nil {
		dbError(c, err, "question")
		return
	}

	rows := h.attachExamTags([]models.Question{question})
	ok(c, rows[0])
}

// questionPayload is the create/update body for a hand-written question.
type questionPayload struct {
	SubjectID    uint   `json:"subject_id"`
	TopicID      *uint  `json:"topic_id"`
	OriginExamID *uint  `json:"origin_exam_id"`
	QuestionText string `json:"question_text"`
	Type         string `json:"type"`
	Difficulty   string `json:"difficulty"`
	Explanation  string `json:"explanation"`
	AnswerText   string `json:"answer_text"`
	Year         *int   `json:"year"`
	Language     string `json:"language"`
	Status       string `json:"status"`
	Options      []struct {
		Label     string `json:"label"`
		Text      string `json:"text"`
		IsCorrect bool   `json:"is_correct"`
	} `json:"options"`
}

// CreateQuestion adds a question by hand.
func (h *Handler) CreateQuestion(c *gin.Context) {
	var payload questionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	payload.QuestionText = strings.TrimSpace(payload.QuestionText)
	if payload.QuestionText == "" || payload.SubjectID == 0 {
		badRequestf(c, "subject_id and question_text are required")
		return
	}

	var subject models.Subject
	if err := h.DB.First(&subject, payload.SubjectID).Error; err != nil {
		dbError(c, err, "subject")
		return
	}

	difficulty := models.Difficulty(payload.Difficulty)
	if !difficulty.Valid() {
		difficulty = models.DifficultyMedium
	}

	question := models.Question{
		SubjectID:    payload.SubjectID,
		TopicID:      payload.TopicID,
		OriginExamID: payload.OriginExamID,
		QuestionText: payload.QuestionText,
		Type:         models.QuestionType(firstNonBlank(payload.Type, string(models.TypeMCQ))),
		Difficulty:   difficulty,
		// A person set this difficulty, so it is trustworthy.
		DifficultyConfidence: 1,
		Explanation:          strings.TrimSpace(payload.Explanation),
		AnswerText:           strings.TrimSpace(payload.AnswerText),
		Origin:               models.OriginManual,
		Year:                 payload.Year,
		Language:             payload.Language,
		Status:               models.Status(firstNonBlank(payload.Status, string(models.StatusApproved))),
	}

	optionTexts := make([]string, 0, len(payload.Options))
	labels := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	for i, opt := range payload.Options {
		text := strings.TrimSpace(opt.Text)
		if text == "" {
			continue
		}
		label := opt.Label
		if label == "" && i < len(labels) {
			label = labels[i]
		}
		question.Options = append(question.Options, models.QuestionOption{
			Label:      label,
			Text:       text,
			OrderIndex: i,
			IsCorrect:  opt.IsCorrect,
		})
		optionTexts = append(optionTexts, text)
		if opt.IsCorrect {
			question.HasAnswer = true
		}
	}
	if question.AnswerText != "" {
		question.HasAnswer = true
	}
	question.ContentHash = extract.ContentHash(question.QuestionText, optionTexts)

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&question).Error; err != nil {
			return err
		}
		if question.OriginExamID == nil {
			return nil
		}
		return syncSingleQuestionLinks(tx, question.ID, *question.OriginExamID)
	})
	if err != nil {
		serverError(c, err)
		return
	}
	created(c, question)
}

// UpdateQuestion edits a question's text, classification or options.
func (h *Handler) UpdateQuestion(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var question models.Question
	if err := h.DB.First(&question, id).Error; err != nil {
		dbError(c, err, "question")
		return
	}

	var payload questionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	updates := map[string]any{}
	if text := strings.TrimSpace(payload.QuestionText); text != "" {
		updates["question_text"] = text
	}
	if payload.SubjectID != 0 {
		updates["subject_id"] = payload.SubjectID
	}
	if payload.TopicID != nil {
		updates["topic_id"] = *payload.TopicID
	}
	if difficulty := models.Difficulty(payload.Difficulty); difficulty.Valid() {
		updates["difficulty"] = difficulty
		updates["difficulty_confidence"] = 1
	}
	if payload.Type != "" {
		updates["type"] = payload.Type
	}
	if payload.Explanation != "" {
		updates["explanation"] = strings.TrimSpace(payload.Explanation)
	}
	if payload.Status != "" {
		updates["status"] = payload.Status
	}
	if payload.Year != nil {
		updates["year"] = *payload.Year
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&question).Updates(updates).Error; err != nil {
				return err
			}
		}
		if payload.Options == nil {
			return nil
		}

		// Options are replaced wholesale; patching them individually invites
		// two correct answers on an MCQ.
		if err := tx.Unscoped().Where("question_id = ?", question.ID).
			Delete(&models.QuestionOption{}).Error; err != nil {
			return err
		}

		hasAnswer := false
		labels := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
		texts := make([]string, 0, len(payload.Options))
		for i, opt := range payload.Options {
			text := strings.TrimSpace(opt.Text)
			if text == "" {
				continue
			}
			label := opt.Label
			if label == "" && i < len(labels) {
				label = labels[i]
			}
			if err := tx.Create(&models.QuestionOption{
				QuestionID: question.ID,
				Label:      label,
				Text:       text,
				OrderIndex: i,
				IsCorrect:  opt.IsCorrect,
			}).Error; err != nil {
				return err
			}
			texts = append(texts, text)
			if opt.IsCorrect {
				hasAnswer = true
			}
		}

		stem := question.QuestionText
		if text, found := updates["question_text"].(string); found {
			stem = text
		}
		return tx.Model(&question).Updates(map[string]any{
			"has_answer":   hasAnswer || strings.TrimSpace(payload.AnswerText) != "",
			"content_hash": extract.ContentHash(stem, texts),
		}).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}

	h.GetQuestion(c)
}

// DeleteQuestion removes a question and everything hanging off it.
func (h *Handler) DeleteQuestion(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var inPapers int64
	h.DB.Model(&models.TestPaperItem{}).Where("question_id = ?", id).Count(&inPapers)
	if inPapers > 0 && !boolQuery(c, "force", false) {
		conflict(c, "this question is used in a test paper; pass force=true to remove it anyway")
		return
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("question_id = ?", id).Delete(&models.QuestionExamLink{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("question_id = ?", id).
			Delete(&models.QuestionOption{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("question_id = ?", id).
			Delete(&models.QualityReview{}).Error; err != nil {
			return err
		}
		if inPapers > 0 {
			if err := tx.Where("question_id = ?", id).
				Delete(&models.TestPaperItem{}).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&models.Question{}, id).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// reviewPayload is a human or model review of a question.
type reviewPayload struct {
	ReviewerType     string `json:"reviewer_type"`
	ReviewerName     string `json:"reviewer_name"`
	RelevanceScore   int    `json:"relevance_score"`
	DifficultyScore  int    `json:"difficulty_score"`
	OriginalityScore int    `json:"originality_score"`
	ClarityScore     int    `json:"clarity_score"`
	IsAmbiguous      bool   `json:"is_ambiguous"`
	IsFactual        bool   `json:"is_factual"`
	IsDuplicate      bool   `json:"is_duplicate"`
	SetDifficulty    string `json:"set_difficulty"`
	Notes            string `json:"notes"`
	// Verdict forces an outcome, bypassing the score threshold.
	Verdict string `json:"verdict"`
}

// ReviewQuestion records a review and updates the question's status.
func (h *Handler) ReviewQuestion(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var question models.Question
	if err := h.DB.First(&question, id).Error; err != nil {
		dbError(c, err, "question")
		return
	}

	var payload reviewPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	review := models.QualityReview{
		QuestionID:       id,
		ReviewerType:     firstNonBlank(payload.ReviewerType, "human"),
		ReviewerName:     strings.TrimSpace(payload.ReviewerName),
		RelevanceScore:   payload.RelevanceScore,
		DifficultyScore:  payload.DifficultyScore,
		OriginalityScore: payload.OriginalityScore,
		ClarityScore:     payload.ClarityScore,
		IsAmbiguous:      payload.IsAmbiguous,
		IsFactual:        payload.IsFactual,
		IsDuplicate:      payload.IsDuplicate,
		Notes:            strings.TrimSpace(payload.Notes),
	}

	verdict := models.Status(payload.Verdict)
	if verdict != models.StatusApproved && verdict != models.StatusRejected {
		if review.Passed(h.Cfg.Review.ScoreThreshold) {
			verdict = models.StatusApproved
		} else {
			verdict = models.StatusRejected
		}
	}
	review.Verdict = verdict

	questionUpdates := map[string]any{"status": verdict}
	if difficulty := models.Difficulty(payload.SetDifficulty); difficulty.Valid() {
		review.SetDifficulty = difficulty
		// A reviewer's difficulty is authoritative, unlike the extractor's guess.
		questionUpdates["difficulty"] = difficulty
		questionUpdates["difficulty_confidence"] = 1
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&review).Error; err != nil {
			return err
		}
		return tx.Model(&question).Updates(questionUpdates).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}

	ok(c, gin.H{"question_status": verdict, "review": review})
}

// bulkPayload applies one change to many questions at once, which is how a
// reviewer works through a freshly ingested paper.
type bulkPayload struct {
	QuestionIDs []uint `json:"question_ids"`
	Status      string `json:"status"`
	Difficulty  string `json:"difficulty"`
	SubjectID   *uint  `json:"subject_id"`
	TopicID     *uint  `json:"topic_id"`
}

// BulkUpdateQuestions applies status, difficulty or re-filing in one request.
func (h *Handler) BulkUpdateQuestions(c *gin.Context) {
	var payload bulkPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	if len(payload.QuestionIDs) == 0 {
		badRequestf(c, "question_ids is required")
		return
	}
	if len(payload.QuestionIDs) > 2000 {
		badRequestf(c, "at most 2000 questions can be updated at once")
		return
	}

	updates := map[string]any{}
	if status := models.Status(payload.Status); status != "" {
		switch status {
		case models.StatusApproved, models.StatusRejected, models.StatusPending:
			updates["status"] = status
		default:
			badRequestf(c, "status must be approved, rejected or pending")
			return
		}
	}
	if difficulty := models.Difficulty(payload.Difficulty); difficulty.Valid() {
		updates["difficulty"] = difficulty
		updates["difficulty_confidence"] = 1
	}
	if payload.SubjectID != nil {
		updates["subject_id"] = *payload.SubjectID
	}
	if payload.TopicID != nil {
		updates["topic_id"] = *payload.TopicID
	}
	if len(updates) == 0 {
		badRequestf(c, "nothing to update")
		return
	}

	result := h.DB.Model(&models.Question{}).
		Where("id IN ?", payload.QuestionIDs).Updates(updates)
	if result.Error != nil {
		serverError(c, result.Error)
		return
	}
	ok(c, gin.H{"updated": result.RowsAffected})
}

// syncSingleQuestionLinks links a hand-written question to its exam and to any
// exam borrowing from it.
func syncSingleQuestionLinks(tx *gorm.DB, questionID, examID uint) error {
	if err := tx.Create(&models.QuestionExamLink{
		QuestionID: questionID,
		ExamID:     examID,
		LinkType:   models.LinkDirect,
		Relevance:  1,
	}).Error; err != nil && !isUniqueViolation(err) {
		return err
	}
	return pipeline.RebuildLinksFromSource(tx, examID)
}
