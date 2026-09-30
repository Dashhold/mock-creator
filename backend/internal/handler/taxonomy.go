package handler

import (
	"strings"

	"mockcreator/internal/extract"
	"mockcreator/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// subjectPayload is the create/update body for a subject.
//
// Aliases are the important field: they are the headings this subject is
// printed under in real documents, and they are what lets the parser recognise
// a section without any exam-specific code.
type subjectPayload struct {
	Name        string   `json:"name"`
	Code        string   `json:"code"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
}

type topicPayload struct {
	SubjectID uint     `json:"subject_id"`
	Name      string   `json:"name"`
	Code      string   `json:"code"`
	Aliases   []string `json:"aliases"`
}

// subjectRow decorates a subject with how much content sits under it.
type subjectRow struct {
	models.Subject
	QuestionCount int64 `json:"question_count"`
	AnsweredCount int64 `json:"answered_count"`
	TopicCount    int64 `json:"topic_count"`
	ExamCount     int64 `json:"exam_count"`
}

// ListSubjects returns the subject catalogue.
func (h *Handler) ListSubjects(c *gin.Context) {
	query := h.DB.Model(&models.Subject{})
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(code) LIKE ?", like, like)
	}

	var subjects []models.Subject
	preload := boolQuery(c, "with_topics", true)
	if preload {
		query = query.Preload("Topics", func(db *gorm.DB) *gorm.DB {
			return db.Order("topics.name asc")
		})
	}
	if err := query.Order("name asc").Find(&subjects).Error; err != nil {
		serverError(c, err)
		return
	}

	if !boolQuery(c, "with_counts", true) {
		list(c, subjects, nil)
		return
	}

	rows := make([]subjectRow, 0, len(subjects))
	for _, subject := range subjects {
		row := subjectRow{Subject: subject}
		h.DB.Model(&models.Question{}).Where("subject_id = ?", subject.ID).Count(&row.QuestionCount)
		h.DB.Model(&models.Question{}).
			Where("subject_id = ? AND has_answer = ?", subject.ID, true).Count(&row.AnsweredCount)
		h.DB.Model(&models.Topic{}).Where("subject_id = ?", subject.ID).Count(&row.TopicCount)
		h.DB.Model(&models.ExamSubject{}).Where("subject_id = ?", subject.ID).Count(&row.ExamCount)
		rows = append(rows, row)
	}
	list(c, rows, nil)
}

// CreateSubject adds a subject to the catalogue.
func (h *Handler) CreateSubject(c *gin.Context) {
	var payload subjectPayload
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

	// The subject's own name is always a usable alias, so add it implicitly.
	aliases := append([]string{payload.Name}, payload.Aliases...)
	encoded, err := extract.EncodeAliases(aliases)
	if err != nil {
		serverError(c, err)
		return
	}

	subject := models.Subject{
		Name:        payload.Name,
		Code:        code,
		Description: strings.TrimSpace(payload.Description),
		Aliases:     encoded,
	}
	if err := h.DB.Create(&subject).Error; err != nil {
		dbError(c, err, "subject code "+code)
		return
	}
	created(c, subject)
}

// GetSubject returns one subject with its topics.
func (h *Handler) GetSubject(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var subject models.Subject
	if err := h.DB.Preload("Topics").First(&subject, id).Error; err != nil {
		dbError(c, err, "subject")
		return
	}
	ok(c, subject)
}

// UpdateSubject edits a subject, including its recognition aliases.
func (h *Handler) UpdateSubject(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var subject models.Subject
	if err := h.DB.First(&subject, id).Error; err != nil {
		dbError(c, err, "subject")
		return
	}

	var payload subjectPayload
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
	if payload.Aliases != nil {
		name := firstNonBlank(payload.Name, subject.Name)
		encoded, err := extract.EncodeAliases(append([]string{name}, payload.Aliases...))
		if err != nil {
			serverError(c, err)
			return
		}
		updates["aliases"] = encoded
	}

	if len(updates) > 0 {
		if err := h.DB.Model(&subject).Updates(updates).Error; err != nil {
			dbError(c, err, "subject")
			return
		}
	}
	ok(c, subject)
}

// DeleteSubject removes a subject, refusing while questions still reference it.
func (h *Handler) DeleteSubject(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var questions int64
	h.DB.Model(&models.Question{}).Where("subject_id = ?", id).Count(&questions)
	if questions > 0 {
		conflict(c, "this subject still holds questions; move or delete them first")
		return
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("subject_id = ?", id).Delete(&models.Topic{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("subject_id = ?", id).
			Delete(&models.ExamSubject{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Subject{}, id).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// ListTopics returns topics, optionally for one subject.
func (h *Handler) ListTopics(c *gin.Context) {
	query := h.DB.Model(&models.Topic{}).Preload("Subject")
	if subjectID := uintQuery(c, "subject_id"); subjectID != nil {
		query = query.Where("subject_id = ?", *subjectID)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		query = query.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(search)+"%")
	}

	var topics []models.Topic
	if err := query.Order("name asc").Find(&topics).Error; err != nil {
		serverError(c, err)
		return
	}
	list(c, topics, nil)
}

// CreateTopic adds a topic under a subject.
func (h *Handler) CreateTopic(c *gin.Context) {
	var payload topicPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Name == "" || payload.SubjectID == 0 {
		badRequestf(c, "subject_id and name are required")
		return
	}

	var subject models.Subject
	if err := h.DB.First(&subject, payload.SubjectID).Error; err != nil {
		dbError(c, err, "subject")
		return
	}

	code := slug(payload.Code)
	if code == "" {
		code = slug(payload.Name)
	}
	encoded, err := extract.EncodeAliases(append([]string{payload.Name}, payload.Aliases...))
	if err != nil {
		serverError(c, err)
		return
	}

	topic := models.Topic{
		SubjectID: payload.SubjectID,
		Name:      payload.Name,
		Code:      code,
		Aliases:   encoded,
	}
	if err := h.DB.Create(&topic).Error; err != nil {
		dbError(c, err, "topic")
		return
	}
	created(c, topic)
}

// UpdateTopic edits a topic.
func (h *Handler) UpdateTopic(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var topic models.Topic
	if err := h.DB.First(&topic, id).Error; err != nil {
		dbError(c, err, "topic")
		return
	}

	var payload topicPayload
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
	if payload.SubjectID != 0 {
		updates["subject_id"] = payload.SubjectID
	}
	if payload.Aliases != nil {
		name := firstNonBlank(payload.Name, topic.Name)
		encoded, err := extract.EncodeAliases(append([]string{name}, payload.Aliases...))
		if err != nil {
			serverError(c, err)
			return
		}
		updates["aliases"] = encoded
	}

	if len(updates) > 0 {
		if err := h.DB.Model(&topic).Updates(updates).Error; err != nil {
			dbError(c, err, "topic")
			return
		}
	}
	ok(c, topic)
}

// DeleteTopic removes a topic, detaching any questions tagged with it.
func (h *Handler) DeleteTopic(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Question{}).Where("topic_id = ?", id).
			Update("topic_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Topic{}, id).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}
