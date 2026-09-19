package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"mockcreator/internal/models"
	"mockcreator/internal/pipeline"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// uploadOutcome is the per-file result of an upload request.
type uploadOutcome struct {
	Filename  string           `json:"filename"`
	Document  *models.Document `json:"document,omitempty"`
	JobID     *uint            `json:"job_id,omitempty"`
	Duplicate bool             `json:"duplicate,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// UploadDocuments accepts one or more files and stores them in the database.
//
// This is the only way material enters the system. Bytes live in a table, not on
// a mounted directory, which means nothing has to be copied into the image or
// staged on the host, and a document can be re-read or re-parsed at any time.
func (h *Handler) UploadDocuments(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		badRequestf(c, "could not read the upload: %v", err)
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["file"]
	}
	if len(files) == 0 {
		badRequestf(c, "no file was attached; send it as \"file\" or \"files\"")
		return
	}

	kind := models.DocumentKind(strings.TrimSpace(firstFormValue(form, "kind")))
	if kind == "" {
		kind = models.KindQuestionPaper
	}
	if !kind.Valid() {
		badRequestf(c, "unknown document kind %q", kind)
		return
	}

	var examID *uint
	if raw := strings.TrimSpace(firstFormValue(form, "exam_id")); raw != "" {
		value, convErr := strconv.ParseUint(raw, 10, 64)
		if convErr != nil || value == 0 {
			badRequestf(c, "invalid exam_id %q", raw)
			return
		}
		id := uint(value)
		var exam models.Exam
		if err := h.DB.First(&exam, id).Error; err != nil {
			dbError(c, err, "exam")
			return
		}
		examID = &id
	}

	var year *int
	if raw := strings.TrimSpace(firstFormValue(form, "year")); raw != "" {
		value, convErr := strconv.Atoi(raw)
		if convErr == nil && value > 1800 && value < 2200 {
			year = &value
		}
	}

	titleOverride := strings.TrimSpace(firstFormValue(form, "title"))
	language := strings.TrimSpace(firstFormValue(form, "language"))
	ocrMode := strings.TrimSpace(firstFormValue(form, "ocr_mode"))
	ocrEngine := strings.TrimSpace(firstFormValue(form, "ocr_engine"))
	autoIngest := parseBoolDefault(firstFormValue(form, "auto_ingest"), true)

	outcomes := make([]uploadOutcome, 0, len(files))
	stored := 0

	for _, header := range files {
		outcome := uploadOutcome{Filename: header.Filename}

		title := titleOverride
		if title == "" || len(files) > 1 {
			title = titleFromFilename(header.Filename)
		}

		doc, duplicate, err := h.storeDocument(header, storeRequest{
			Kind:      kind,
			Title:     title,
			ExamID:    examID,
			Year:      year,
			Language:  language,
			OCRMode:   ocrMode,
			OCREngine: ocrEngine,
		})
		if err != nil {
			outcome.Error = err.Error()
			outcomes = append(outcomes, outcome)
			continue
		}

		outcome.Document = doc
		outcome.Duplicate = duplicate
		if !duplicate {
			stored++
		}

		if autoIngest && !duplicate {
			if jobID, err := h.enqueueIngest(doc.ID, pipeline.IngestParams{
				OCRMode:   ocrMode,
				OCREngine: ocrEngine,
			}); err != nil {
				outcome.Error = fmt.Sprintf("stored, but could not queue processing: %v", err)
			} else {
				outcome.JobID = &jobID
			}
		}

		outcomes = append(outcomes, outcome)
	}

	status := http.StatusCreated
	if stored == 0 {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"data": outcomes, "meta": gin.H{
		"received": len(files),
		"stored":   stored,
	}})
}

type storeRequest struct {
	Kind      models.DocumentKind
	Title     string
	ExamID    *uint
	Year      *int
	Language  string
	OCRMode   string
	OCREngine string
}

// storeDocument validates a file and writes it with its bytes.
func (h *Handler) storeDocument(header *multipart.FileHeader, req storeRequest) (*models.Document, bool, error) {
	extension := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
	if extension == "" {
		return nil, false, errors.New("file has no extension, so its format cannot be determined")
	}
	if !h.Cfg.Upload.AllowsExtension(extension) {
		return nil, false, fmt.Errorf("%s files are not accepted", extension)
	}
	if header.Size > h.Cfg.Upload.MaxBytes {
		return nil, false, fmt.Errorf("file is %d bytes, limit is %d", header.Size, h.Cfg.Upload.MaxBytes)
	}

	opened, err := header.Open()
	if err != nil {
		return nil, false, fmt.Errorf("could not read the upload: %w", err)
	}
	defer opened.Close()

	// LimitReader guards against a header that understates the real size.
	data, err := io.ReadAll(io.LimitReader(opened, h.Cfg.Upload.MaxBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("could not read the upload: %w", err)
	}
	if int64(len(data)) > h.Cfg.Upload.MaxBytes {
		return nil, false, fmt.Errorf("file exceeds the %d byte limit", h.Cfg.Upload.MaxBytes)
	}
	if len(data) == 0 {
		return nil, false, errors.New("file is empty")
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	// The same bytes uploaded twice is almost always an accident, so point the
	// user at what they already have instead of duplicating the work.
	var existing models.Document
	err = h.DB.Where("sha256 = ?", digest).First(&existing).Error
	if err == nil {
		return &existing, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	doc := models.Document{
		ExamID:    req.ExamID,
		Kind:      req.Kind,
		Title:     firstNonBlank(req.Title, header.Filename),
		Filename:  filepath.Base(header.Filename),
		Extension: extension,
		MimeType:  header.Header.Get("Content-Type"),
		SizeBytes: int64(len(data)),
		SHA256:    digest,
		Year:      req.Year,
		Language:  req.Language,
		Status:    models.StatusPending,
		Stage:     "uploaded",
		OCRMode:   firstNonBlank(req.OCRMode, h.Cfg.Converter.DefaultOCRMode),
		OCREngine: firstNonBlank(req.OCREngine, h.Cfg.Converter.DefaultOCREngine),
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&doc).Error; err != nil {
			return err
		}
		return tx.Create(&models.DocumentBlob{DocumentID: doc.ID, Data: data}).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &doc, false, nil
}

// enqueueIngest queues conversion and extraction for a document.
func (h *Handler) enqueueIngest(docID uint, params pipeline.IngestParams) (uint, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return 0, err
	}
	job := &models.Job{Type: models.JobIngest, DocumentID: &docID, Params: encoded}
	if err := h.Jobs.Enqueue(job); err != nil {
		return 0, err
	}
	if err := h.DB.Model(&models.Document{}).Where("id = ?", docID).Updates(map[string]any{
		"status": models.StatusQueued,
		"stage":  "queued for processing",
	}).Error; err != nil {
		return job.ID, err
	}
	return job.ID, nil
}

// documentRow adds the live job to a document so the list can show progress.
type documentRow struct {
	models.Document
	ActiveJob *models.Job `json:"active_job,omitempty"`
}

// ListDocuments returns uploaded documents.
func (h *Handler) ListDocuments(c *gin.Context) {
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.Document{}).Preload("Exam")
	query = filterQuery(c, query, map[string]string{
		"kind":      "kind",
		"status":    "status",
		"extension": "extension",
	})
	if examID := uintQuery(c, "exam_id"); examID != nil {
		query = query.Where("exam_id = ?", *examID)
	}
	if boolQuery(c, "unassigned", false) {
		query = query.Where("exam_id IS NULL")
	}
	if year := intQuery(c, "year"); year != nil {
		query = query.Where("year = ?", *year)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(title) LIKE ? OR LOWER(filename) LIKE ?", like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	var docs []models.Document
	if err := query.Order("created_at desc").Limit(size).Offset(offset).Find(&docs).Error; err != nil {
		serverError(c, err)
		return
	}

	rows := make([]documentRow, 0, len(docs))
	for _, doc := range docs {
		row := documentRow{Document: doc}
		if !doc.Status.Terminal() {
			var job models.Job
			err := h.DB.Where("document_id = ? AND status IN ?", doc.ID,
				[]models.Status{models.StatusQueued, models.StatusProcessing}).
				Order("created_at desc").First(&job).Error
			if err == nil {
				row.ActiveJob = &job
			}
		}
		rows = append(rows, row)
	}

	list(c, rows, makePage(page, size, total))
}

// GetDocument returns one document with its conversion summary.
func (h *Handler) GetDocument(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var doc models.Document
	if err := h.DB.Preload("Exam").First(&doc, id).Error; err != nil {
		dbError(c, err, "document")
		return
	}

	// The markdown can be megabytes; omit it here and serve it on demand.
	var conversion models.DocumentConversion
	hasConversion := h.DB.Select(
		"id, document_id, engine, engine_version, ocr_engine, ocr_applied, format, "+
			"page_count, char_count, table_count, duration_ms, text_ratio, status, error, created_at, updated_at").
		Where("document_id = ?", id).First(&conversion).Error == nil

	var jobs []models.Job
	h.DB.Where("document_id = ?", id).Order("created_at desc").Limit(5).Find(&jobs)

	var questionCount int64
	h.DB.Model(&models.Question{}).Where("document_id = ?", id).Count(&questionCount)

	payload := gin.H{
		"document":       doc,
		"jobs":           jobs,
		"question_count": questionCount,
	}
	if hasConversion {
		payload["conversion"] = conversion
	}
	ok(c, payload)
}

// DownloadDocument streams the stored bytes back.
func (h *Handler) DownloadDocument(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var doc models.Document
	if err := h.DB.First(&doc, id).Error; err != nil {
		dbError(c, err, "document")
		return
	}
	var blob models.DocumentBlob
	if err := h.DB.Where("document_id = ?", id).First(&blob).Error; err != nil {
		dbError(c, err, "file contents")
		return
	}

	mime := doc.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", doc.Filename))
	c.Data(http.StatusOK, mime, blob.Data)
}

// GetConversion returns the converted markdown, optionally truncated.
func (h *Handler) GetConversion(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var conversion models.DocumentConversion
	if err := h.DB.Where("document_id = ?", id).First(&conversion).Error; err != nil {
		dbError(c, err, "conversion")
		return
	}

	limit := 200000
	if raw := intQuery(c, "limit"); raw != nil && *raw > 0 {
		limit = *raw
	}
	markdown := conversion.Markdown
	truncated := false
	if len(markdown) > limit {
		markdown = markdown[:limit]
		truncated = true
	}

	ok(c, gin.H{
		"conversion": gin.H{
			"engine":         conversion.Engine,
			"engine_version": conversion.EngineVersion,
			"ocr_engine":     conversion.OCREngine,
			"ocr_applied":    conversion.OCRApplied,
			"page_count":     conversion.PageCount,
			"char_count":     conversion.CharCount,
			"table_count":    conversion.TableCount,
			"text_ratio":     conversion.TextRatio,
			"duration_ms":    conversion.DurationMS,
		},
		"markdown":  markdown,
		"truncated": truncated,
	})
}

// ListBlocks returns the structural blocks the converter identified.
func (h *Handler) ListBlocks(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.ExtractedBlock{}).Where("document_id = ?", id)
	if blockType := strings.TrimSpace(c.Query("block_type")); blockType != "" && blockType != "all" {
		query = query.Where("block_type = ?", blockType)
	}
	if pageNo := intQuery(c, "page_no"); pageNo != nil {
		query = query.Where("page_no = ?", *pageNo)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	var blocks []models.ExtractedBlock
	if err := query.Order("order_index asc").Limit(size).Offset(offset).
		Find(&blocks).Error; err != nil {
		serverError(c, err)
		return
	}
	list(c, blocks, makePage(page, size, total))
}

// ingestPayload lets a caller override conversion settings for one run.
type ingestPayload struct {
	// Engine is auto, geometry or docling. Naming one is how a user re-reads a
	// document that came out wrong under the other.
	Engine    string   `json:"engine"`
	OCRMode   string   `json:"ocr_mode"`
	OCREngine string   `json:"ocr_engine"`
	TableMode string   `json:"table_mode"`
	Languages []string `json:"languages"`
	MaxPages  int      `json:"max_pages"`
	Reconvert bool     `json:"reconvert"`
	ParseOnly bool     `json:"parse_only"`
	// NoFallback stops the pipeline trying the other engine when the first one
	// parses poorly, so a caller can see exactly what one engine produces.
	NoFallback bool `json:"no_fallback"`
}

// IngestDocument queues conversion and extraction.
func (h *Handler) IngestDocument(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var doc models.Document
	if err := h.DB.First(&doc, id).Error; err != nil {
		dbError(c, err, "document")
		return
	}

	var payload ingestPayload
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		badRequest(c, err)
		return
	}

	// Two ingests of the same document would fight over its questions.
	var running int64
	h.DB.Model(&models.Job{}).
		Where("document_id = ? AND type = ? AND status IN ?", id, models.JobIngest,
			[]models.Status{models.StatusQueued, models.StatusProcessing}).
		Count(&running)
	if running > 0 {
		conflict(c, "this document is already being processed")
		return
	}

	jobID, err := h.enqueueIngest(id, pipeline.IngestParams{
		Engine:     payload.Engine,
		OCRMode:    payload.OCRMode,
		OCREngine:  payload.OCREngine,
		TableMode:  payload.TableMode,
		Languages:  payload.Languages,
		MaxPages:   payload.MaxPages,
		Reconvert:  payload.Reconvert,
		ParseOnly:  payload.ParseOnly,
		NoFallback: payload.NoFallback,
	})
	if err != nil {
		serverError(c, err)
		return
	}

	var job models.Job
	h.DB.First(&job, jobID)
	accepted(c, job)
}

// ReparseDocument re-runs extraction on the cached conversion.
//
// Cheap, and the right action after editing subject aliases: the expensive OCR
// result is reused and only the parsing rules change.
func (h *Handler) ReparseDocument(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	var conversion models.DocumentConversion
	if err := h.DB.Select("id").Where("document_id = ?", id).First(&conversion).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			badRequestf(c, "this document has not been converted yet; run a full ingest first")
			return
		}
		serverError(c, err)
		return
	}

	jobID, err := h.enqueueIngest(id, pipeline.IngestParams{ParseOnly: true})
	if err != nil {
		serverError(c, err)
		return
	}
	var job models.Job
	h.DB.First(&job, jobID)
	accepted(c, job)
}

// documentPatch edits a document's metadata after upload.
type documentPatch struct {
	Title    *string `json:"title"`
	Kind     *string `json:"kind"`
	ExamID   *uint   `json:"exam_id"`
	Year     *int    `json:"year"`
	Language *string `json:"language"`
	// DetachExam clears the exam link, since a null exam_id cannot be expressed
	// by omitting the field.
	DetachExam bool `json:"detach_exam"`
}

// UpdateDocument edits metadata, including attaching a document to an exam.
func (h *Handler) UpdateDocument(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var doc models.Document
	if err := h.DB.First(&doc, id).Error; err != nil {
		dbError(c, err, "document")
		return
	}

	var payload documentPatch
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, err)
		return
	}

	updates := map[string]any{}
	if payload.Title != nil && strings.TrimSpace(*payload.Title) != "" {
		updates["title"] = strings.TrimSpace(*payload.Title)
	}
	if payload.Kind != nil {
		kind := models.DocumentKind(*payload.Kind)
		if !kind.Valid() {
			badRequestf(c, "unknown document kind %q", *payload.Kind)
			return
		}
		updates["kind"] = kind
	}
	if payload.Year != nil {
		updates["year"] = *payload.Year
	}
	if payload.Language != nil {
		updates["language"] = *payload.Language
	}

	newExam := doc.ExamID
	switch {
	case payload.DetachExam:
		updates["exam_id"] = nil
		newExam = nil
	case payload.ExamID != nil:
		var exam models.Exam
		if err := h.DB.First(&exam, *payload.ExamID).Error; err != nil {
			dbError(c, err, "exam")
			return
		}
		updates["exam_id"] = *payload.ExamID
		newExam = payload.ExamID
	}

	if len(updates) > 0 {
		if err := h.DB.Model(&doc).Updates(updates).Error; err != nil {
			serverError(c, err)
			return
		}
	}

	// Attaching a document to an exam retroactively gives that exam its
	// questions, and refreshes anything borrowing from it.
	if newExam != nil && (doc.ExamID == nil || *doc.ExamID != *newExam) {
		if err := h.DB.Model(&models.Question{}).Where("document_id = ?", doc.ID).
			Update("origin_exam_id", *newExam).Error; err != nil {
			serverError(c, err)
			return
		}
		if err := pipeline.EnsureDirectLinks(h.DB, *newExam); err != nil {
			serverError(c, err)
			return
		}
		if err := pipeline.RebuildLinksFromSource(h.DB, *newExam); err != nil {
			serverError(c, err)
			return
		}
	}

	h.DB.First(&doc, id)
	ok(c, doc)
}

// DeleteDocument removes a document, its bytes, its conversion and the
// questions it contributed that are not already used in a paper.
func (h *Handler) DeleteDocument(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}

	keepQuestions := boolQuery(c, "keep_questions", false)

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if !keepQuestions {
			if err := clearDocumentQuestionsTx(tx, id); err != nil {
				return err
			}
		} else if err := tx.Model(&models.Question{}).Where("document_id = ?", id).
			Update("document_id", nil).Error; err != nil {
			return err
		}

		for _, step := range []func() error{
			func() error {
				return tx.Unscoped().Where("document_id = ?", id).Delete(&models.ExtractedBlock{}).Error
			},
			func() error {
				return tx.Unscoped().Where("document_id = ?", id).Delete(&models.DocumentConversion{}).Error
			},
			func() error {
				return tx.Unscoped().Where("document_id = ?", id).Delete(&models.DocumentBlob{}).Error
			},
			func() error {
				return tx.Unscoped().Delete(&models.Document{}, id).Error
			},
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, gin.H{"deleted": id})
}

// clearDocumentQuestionsTx mirrors the pipeline's cleanup for the delete path.
func clearDocumentQuestionsTx(tx *gorm.DB, docID uint) error {
	var ids []uint
	if err := tx.Model(&models.Question{}).Where("document_id = ?", docID).
		Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	var inUse []uint
	if err := tx.Model(&models.TestPaperItem{}).Where("question_id IN ?", ids).
		Distinct().Pluck("question_id", &inUse).Error; err != nil {
		return err
	}
	protected := make(map[uint]bool, len(inUse))
	for _, id := range inUse {
		protected[id] = true
	}
	removable := make([]uint, 0, len(ids))
	for _, id := range ids {
		if !protected[id] {
			removable = append(removable, id)
		}
	}

	if len(inUse) > 0 {
		if err := tx.Model(&models.Question{}).Where("id IN ?", inUse).
			Update("document_id", nil).Error; err != nil {
			return err
		}
	}
	if len(removable) == 0 {
		return nil
	}
	if err := tx.Unscoped().Where("question_id IN ?", removable).
		Delete(&models.QuestionOption{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("question_id IN ?", removable).
		Delete(&models.QualityReview{}).Error; err != nil {
		return err
	}
	if err := tx.Where("question_id IN ?", removable).
		Delete(&models.QuestionExamLink{}).Error; err != nil {
		return err
	}
	return tx.Unscoped().Where("id IN ?", removable).Delete(&models.Question{}).Error
}

func firstFormValue(form *multipart.Form, key string) string {
	if values, found := form.Value[key]; found && len(values) > 0 {
		return values[0]
	}
	return ""
}

func parseBoolDefault(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// titleFromFilename turns "cgl-2023-shift-1.pdf" into "Cgl 2023 Shift 1".
func titleFromFilename(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	base = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(base)
	fields := strings.Fields(base)
	for i, field := range fields {
		runes := []rune(field)
		if len(runes) > 0 && runes[0] >= 'a' && runes[0] <= 'z' {
			runes[0] = runes[0] - 'a' + 'A'
			fields[i] = string(runes)
		}
	}
	title := strings.Join(fields, " ")
	if title == "" {
		return filepath.Base(name)
	}
	return title
}
