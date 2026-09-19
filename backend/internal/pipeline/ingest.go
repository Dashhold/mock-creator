package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"mockcreator/internal/converter"
	"mockcreator/internal/extract"
	"mockcreator/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// IngestParams is the request stored on an ingest job.
type IngestParams struct {
	// Engine names the extraction engine: auto, geometry or docling. Empty uses
	// the configured default. Naming one is how a user re-runs a document that
	// came out wrong under the other.
	Engine    string   `json:"engine,omitempty"`
	OCRMode   string   `json:"ocr_mode,omitempty"`
	OCREngine string   `json:"ocr_engine,omitempty"`
	TableMode string   `json:"table_mode,omitempty"`
	Languages []string `json:"languages,omitempty"`
	MaxPages  int      `json:"max_pages,omitempty"`

	// Reconvert forces a fresh conversion even when one is cached, which is what
	// you want after switching OCR engine or mode.
	Reconvert bool `json:"reconvert,omitempty"`
	// ParseOnly reuses the cached conversion and only re-runs extraction, which
	// is cheap and is the right choice after editing subject aliases.
	ParseOnly bool `json:"parse_only,omitempty"`
	// NoFallback stops the pipeline trying the other engine when the first one
	// yields a poor parse. Used when a caller wants to see exactly what one
	// engine produces.
	NoFallback bool `json:"no_fallback,omitempty"`
}

// parseQualityFloor is the mean per-question parse confidence below which the
// pipeline tries the other extraction engine before accepting the result.
//
// The point is not to squeeze out a better number. It is that a poor parse and a
// difficult document look identical from the outside, and trying the alternative
// is the only way to tell them apart. Whichever attempt scores better is kept,
// and both scores are recorded.
const parseQualityFloor = 0.75

// IngestResult summarises what an ingest produced.
type IngestResult struct {
	DocumentID uint `json:"document_id"`
	Pages      int  `json:"pages"`
	Blocks     int  `json:"blocks"`

	QuestionsCreated  int            `json:"questions_created"`
	QuestionsReused   int            `json:"questions_reused"`
	AnswersApplied    int            `json:"answers_applied"`
	Answered          int            `json:"answered"`
	BySubject         map[string]int `json:"by_subject,omitempty"`
	UnmatchedSubjects int            `json:"unmatched_subjects"`

	// Quality outcomes. These are the numbers that say what is usable, as
	// distinct from what was merely stored.
	Passed      int `json:"passed"`
	NeedsReview int `json:"needs_review"`
	Failed      int `json:"failed"`
	Passages    int `json:"passages"`

	Engine        string  `json:"engine"`
	EngineReason  string  `json:"engine_reason,omitempty"`
	OCRApplied    bool    `json:"ocr_applied"`
	OCREngine     string  `json:"ocr_engine,omitempty"`
	TextRatio     float64 `json:"text_ratio"`
	FromCache     bool    `json:"from_cache"`
	QuestionStyle string  `json:"question_style,omitempty"`
	OptionStyle   string  `json:"option_style,omitempty"`
	OptionCount   int     `json:"option_count"`

	// ExtractionConfidence is the converter's opinion of the text; ParseConfidence
	// is the parser's opinion of the questions it found in that text. Both are
	// reported because they fail independently.
	ExtractionConfidence float64 `json:"extraction_confidence"`
	ParseConfidence      float64 `json:"parse_confidence"`
	// EngineRetried records that the other engine was tried, and what it scored.
	EngineRetried   string  `json:"engine_retried,omitempty"`
	RetryConfidence float64 `json:"retry_confidence,omitempty"`
	UnreadableChars int     `json:"unreadable_chars,omitempty"`

	AnswerKeyFound bool     `json:"answer_key_found"`
	Warnings       []string `json:"warnings,omitempty"`
	DurationMS     int64    `json:"duration_ms"`
}

// runIngest converts a document and extracts its questions.
func (r *Runner) runIngest(ctx context.Context, job *models.Job) (any, error) {
	started := time.Now()

	if job.DocumentID == nil {
		return nil, errors.New("ingest job has no document")
	}
	var doc models.Document
	if err := r.db.First(&doc, *job.DocumentID).Error; err != nil {
		return nil, fmt.Errorf("load document %d: %w", *job.DocumentID, err)
	}

	var params IngestParams
	if err := decodeParams(job, &params); err != nil {
		return nil, err
	}

	r.setDocumentStatus(doc.ID, models.StatusProcessing, "converting", "")
	r.progress(job.ID, "converting", 5)

	conversion, fromCache, err := r.ensureConversion(ctx, job, &doc, params)
	if err != nil {
		r.setDocumentStatus(doc.ID, models.StatusFailed, "conversion failed", err.Error())
		return nil, err
	}

	result := IngestResult{
		DocumentID:           doc.ID,
		Pages:                conversion.PageCount,
		Engine:               conversion.Engine,
		EngineReason:         conversion.EngineReason,
		OCRApplied:           conversion.OCRApplied,
		OCREngine:            conversion.OCREngine,
		TextRatio:            conversion.TextRatio,
		ExtractionConfidence: conversion.Confidence,
		UnreadableChars:      conversion.UnreadableChars,
		FromCache:            fromCache,
		BySubject:            map[string]int{},
		Warnings:             decodeStringList(conversion.Warnings),
	}

	var blockCount int64
	r.db.Model(&models.ExtractedBlock{}).Where("document_id = ?", doc.ID).Count(&blockCount)
	result.Blocks = int(blockCount)

	if strings.TrimSpace(conversion.Markdown) == "" {
		msg := "the document converted but produced no text; if it is a scan, re-run with OCR forced"
		r.setDocumentStatus(doc.ID, models.StatusFailed, "no text extracted", msg)
		result.Warnings = append(result.Warnings, msg)
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}

	// An answer key carries no questions of its own; it completes questions that
	// were extracted from the matching paper.
	if doc.Kind == models.KindAnswerKey {
		r.progress(job.ID, "applying answer key", 70)
		applied, err := r.applyAnswerKeyDocument(&doc, conversion.Markdown)
		if err != nil {
			r.setDocumentStatus(doc.ID, models.StatusFailed, "answer key failed", err.Error())
			return nil, err
		}
		result.AnswersApplied = applied
		note := fmt.Sprintf("answer key applied to %d question(s)", applied)
		if applied == 0 {
			note = "no questions matched this answer key; ingest the question paper first, " +
				"and check the exam and year match"
			result.Warnings = append(result.Warnings, note)
		}
		r.setDocumentStatus(doc.ID, models.StatusCompleted, "answer key applied", note)
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}

	// Reference material is stored for lookup but is not mined for questions.
	if doc.Kind == models.KindSyllabus {
		note := fmt.Sprintf("stored as reference material: %d blocks across %d page(s)",
			result.Blocks, result.Pages)
		r.setDocumentStatus(doc.ID, models.StatusCompleted, "stored", note)
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}

	r.progress(job.ID, "parsing questions", 65)

	parsed, err := r.parseDocument(&doc, conversion.Markdown)
	if err != nil {
		r.setDocumentStatus(doc.ID, models.StatusFailed, "parsing failed", err.Error())
		return nil, err
	}

	// A weak parse is ambiguous: the document may be hard, or this engine may
	// have read it badly. Trying the other one settles it, and the better result
	// is kept with both scores recorded so the choice is auditable.
	if r.shouldRetryOtherEngine(parsed, conversion, params) {
		r.progress(job.ID, "re-reading with the other engine", 70)
		if alt, altParsed, ok := r.retryOtherEngine(ctx, job, &doc, params, conversion); ok {
			result.EngineRetried = alt.Engine
			result.RetryConfidence = altParsed.Confidence
			if betterParse(altParsed, parsed) {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"first read with %s scored %.2f; re-read with %s scored %.2f and was kept",
					conversion.Engine, parsed.Confidence, alt.Engine, altParsed.Confidence))
				conversion, parsed = alt, altParsed
				result.Engine = alt.Engine
				result.EngineReason = alt.EngineReason
				result.ExtractionConfidence = alt.Confidence
				result.UnreadableChars = alt.UnreadableChars
				result.Pages = alt.PageCount
				result.OCRApplied = alt.OCRApplied
				result.FromCache = false
			} else {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"re-read with %s scored %.2f, no better than %s at %.2f; the first read was kept",
					alt.Engine, altParsed.Confidence, conversion.Engine, parsed.Confidence))
			}
		}
	}

	result.QuestionStyle = parsed.QuestionStyle
	result.OptionStyle = parsed.OptionStyle
	result.OptionCount = parsed.OptionCount
	result.AnswerKeyFound = parsed.AnswerKeyFound
	result.ParseConfidence = parsed.Confidence
	result.Warnings = append(result.Warnings, parsed.Warnings...)

	r.progress(job.ID, "saving and validating questions", 80)

	saved, err := r.persistQuestions(&doc, parsed, saveContext{
		engine:      conversion.Engine,
		confidence:  conversion.Confidence,
		optionCount: parsed.OptionCount,
	})
	if err != nil {
		r.setDocumentStatus(doc.ID, models.StatusFailed, "saving failed", err.Error())
		return nil, err
	}
	result.QuestionsCreated = saved.created
	result.QuestionsReused = saved.reused
	result.Answered = saved.answered
	result.BySubject = saved.bySubject
	result.UnmatchedSubjects = saved.unmatched
	result.Passed = saved.passed
	result.NeedsReview = saved.needReview
	result.Failed = saved.failed
	result.Passages = saved.passages

	// The note states what is usable, not merely what was stored. "100 questions
	// extracted" reads as success even when half of them are unusable.
	note := fmt.Sprintf("%d question(s) extracted: %d ready to use, %d need review, %d rejected",
		saved.created, saved.passed, saved.needReview, saved.failed)
	if saved.reused > 0 {
		note += fmt.Sprintf("; %d already in the warehouse", saved.reused)
	}
	r.setDocumentStatus(doc.ID, models.StatusCompleted, "ready", note)

	warnings, _ := json.Marshal(result.Warnings)
	if err := r.db.Model(&models.Document{}).Where("id = ?", doc.ID).Updates(map[string]any{
		"question_count":        saved.created,
		"flagged_count":         saved.needReview + saved.failed,
		"page_count":            conversion.PageCount,
		"block_count":           result.Blocks,
		"engine":                conversion.Engine,
		"extraction_confidence": conversion.Confidence,
		"warnings":              datatypes.JSON(warnings),
	}).Error; err != nil {
		return nil, fmt.Errorf("update document counters: %w", err)
	}

	// Model review runs as its own job so a slow or unreachable model cannot hold
	// the ingest open, and so it can be re-run without re-converting.
	if r.llm.Available() && r.cfg.LLM.AuditQuestions && saved.created > 0 {
		if err := r.Enqueue(&models.Job{
			Type:       models.JobQuestionAudit,
			DocumentID: &doc.ID,
			ExamID:     doc.ExamID,
			Params:     auditParamsFor(doc.ID),
		}); err != nil {
			result.Warnings = append(result.Warnings,
				"questions were saved but the model review could not be queued: "+err.Error())
		}
	}

	r.progress(job.ID, "linking exams", 95)
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// shouldRetryOtherEngine decides whether a second reading is worth the time.
func (r *Runner) shouldRetryOtherEngine(
	parsed extract.ParseResult,
	conversion *models.DocumentConversion,
	params IngestParams,
) bool {
	if params.NoFallback || params.ParseOnly {
		return false
	}
	// An explicitly chosen engine is honoured, not second-guessed.
	if params.Engine == "geometry" || params.Engine == "docling" {
		return false
	}
	if otherEngine(conversion.Engine) == "" {
		return false
	}
	// No questions at all, or a weak parse, are both worth a second opinion.
	return len(parsed.Questions) == 0 || parsed.Confidence < parseQualityFloor
}

// otherEngine names the alternative to the one that was used.
func otherEngine(used string) string {
	switch used {
	case "geometry":
		return "docling"
	case "docling":
		return "geometry"
	default:
		return ""
	}
}

// retryOtherEngine converts and parses the document again with the other engine.
//
// The result is returned rather than stored, so the caller can compare the two
// and keep the better. Nothing is overwritten until that decision is made.
func (r *Runner) retryOtherEngine(
	ctx context.Context,
	job *models.Job,
	doc *models.Document,
	params IngestParams,
	current *models.DocumentConversion,
) (*models.DocumentConversion, extract.ParseResult, bool) {
	alternative := otherEngine(current.Engine)
	retry := params
	retry.Engine = alternative
	retry.Reconvert = true
	retry.ParseOnly = false

	converted, err := r.convert(ctx, job, doc, retry)
	if err != nil {
		log.Printf("pipeline: re-read of document %d with %s failed: %v", doc.ID, alternative, err)
		return nil, extract.ParseResult{}, false
	}

	record := conversionRecordFor(doc, converted)
	parsed, err := r.parseDocument(doc, record.Markdown)
	if err != nil {
		log.Printf("pipeline: re-parse of document %d with %s failed: %v", doc.ID, alternative, err)
		return nil, extract.ParseResult{}, false
	}
	return record, parsed, true
}

// betterParse reports whether candidate is a clear improvement on incumbent.
//
// More questions wins first, because a parse that found half the paper is worse
// than one that found all of it whatever its confidence. Confidence breaks the
// tie, and a small margin is required so noise does not flip the choice.
func betterParse(candidate, incumbent extract.ParseResult) bool {
	if len(candidate.Questions) == 0 {
		return false
	}
	if len(incumbent.Questions) == 0 {
		return true
	}
	candidateCount, incumbentCount := len(candidate.Questions), len(incumbent.Questions)
	if candidateCount > incumbentCount*11/10 {
		return true
	}
	if incumbentCount > candidateCount*11/10 {
		return false
	}
	return candidate.Confidence > incumbent.Confidence+0.05
}

// decodeStringList reads a JSON array of strings, tolerating an empty column.
func decodeStringList(raw datatypes.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// ensureConversion reuses a cached conversion when possible, otherwise calls the
// converter service. Caching matters because re-parsing with improved rules is
// common and OCR is the expensive part.
func (r *Runner) ensureConversion(
	ctx context.Context,
	job *models.Job,
	doc *models.Document,
	params IngestParams,
) (*models.DocumentConversion, bool, error) {
	var existing models.DocumentConversion
	err := r.db.Where("document_id = ?", doc.ID).First(&existing).Error
	hasCache := err == nil && existing.Status == models.StatusCompleted &&
		strings.TrimSpace(existing.Markdown) != ""
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, fmt.Errorf("load cached conversion: %w", err)
	}

	if hasCache && !params.Reconvert {
		return &existing, true, nil
	}
	if params.ParseOnly && !hasCache {
		return nil, false, errors.New("no cached conversion to re-parse; run a full ingest first")
	}

	converted, err := r.convert(ctx, job, doc, params)
	if err != nil {
		return nil, false, err
	}

	record, err := r.saveConversion(doc, converted)
	if err != nil {
		return nil, false, err
	}
	return record, false, nil
}

// convert calls the converter service for a document's stored bytes.
func (r *Runner) convert(
	ctx context.Context,
	job *models.Job,
	doc *models.Document,
	params IngestParams,
) (*converter.Result, error) {
	var blob models.DocumentBlob
	if err := r.db.Where("document_id = ?", doc.ID).First(&blob).Error; err != nil {
		return nil, fmt.Errorf("load file bytes for document %d: %w", doc.ID, err)
	}
	if len(blob.Data) == 0 {
		return nil, errors.New("stored file is empty")
	}

	opts := converter.Options{
		Engine:        firstNonEmpty(params.Engine, doc.Engine),
		OCRMode:       firstNonEmpty(params.OCRMode, doc.OCRMode),
		OCREngine:     firstNonEmpty(params.OCREngine, doc.OCREngine),
		TableMode:     params.TableMode,
		Languages:     params.Languages,
		MaxPages:      params.MaxPages,
		IncludeBlocks: true,
	}
	opts = opts.WithDefaults(r.cfg.Converter)
	if len(opts.Languages) == 0 && doc.Language != "" {
		opts.Languages = []string{doc.Language}
	}

	converted, err := r.conv.Convert(ctx, doc.Filename, blob.Data, opts,
		func(stage string, percent int) {
			// The converter's 0-100 maps onto the first 60% of the job.
			r.progress(job.ID, stage, 5+percent*55/100)
		})
	if err != nil {
		return nil, fmt.Errorf("convert %s: %w", doc.Filename, err)
	}
	return converted, nil
}

// conversionRecordFor builds the row that describes a conversion, without
// storing it. Keeping construction separate from persistence is what lets the
// pipeline compare two engines' output before committing to either.
func conversionRecordFor(doc *models.Document, res *converter.Result) *models.DocumentConversion {
	meta, _ := json.Marshal(res.Metadata)
	warnings, _ := json.Marshal(res.Metadata.Warnings)

	return &models.DocumentConversion{
		DocumentID:      doc.ID,
		Engine:          res.Metadata.Engine,
		EngineVersion:   res.Metadata.EngineVersion,
		EngineReason:    res.Metadata.EngineReason,
		OCREngine:       res.Metadata.OCREngine,
		OCRApplied:      res.Metadata.OCRApplied,
		Format:          res.Metadata.Format,
		Markdown:        res.Markdown,
		PageCount:       res.Metadata.PageCount,
		CharCount:       res.Metadata.CharCount,
		TableCount:      res.Metadata.TableCount,
		DurationMS:      res.Metadata.DurationMS,
		TextRatio:       res.Metadata.TextRatio,
		Confidence:      res.Metadata.ExtractionConfidence,
		UnreadableChars: res.Metadata.UnreadableChars,
		Warnings:        datatypes.JSON(warnings),
		Status:          models.StatusCompleted,
		Meta:            meta,
	}
}

// saveConversion stores the conversion and its blocks, replacing any prior run.
func (r *Runner) saveConversion(doc *models.Document, res *converter.Result) (*models.DocumentConversion, error) {
	record := *conversionRecordFor(doc, res)

	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Replace rather than accumulate, so a re-convert leaves one truth.
		if err := tx.Unscoped().Where("document_id = ?", doc.ID).
			Delete(&models.DocumentConversion{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("document_id = ?", doc.ID).
			Delete(&models.ExtractedBlock{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}

		if len(res.Blocks) == 0 {
			return nil
		}
		blocks := make([]models.ExtractedBlock, 0, len(res.Blocks))
		for _, b := range res.Blocks {
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			blocks = append(blocks, models.ExtractedBlock{
				DocumentID: doc.ID,
				OrderIndex: b.OrderIndex,
				PageNo:     b.PageNo,
				BlockType:  b.BlockType,
				Text:       text,
				CharCount:  len(text),
			})
		}
		if len(blocks) == 0 {
			return nil
		}
		return tx.CreateInBatches(blocks, 200).Error
	})
	if err != nil {
		return nil, fmt.Errorf("save conversion: %w", err)
	}
	return &record, nil
}

// parseDocument runs extraction using the current subject catalogue.
func (r *Runner) parseDocument(doc *models.Document, markdown string) (extract.ParseResult, error) {
	var subjects []models.Subject
	if err := r.db.Find(&subjects).Error; err != nil {
		return extract.ParseResult{}, fmt.Errorf("load subjects: %w", err)
	}
	var topics []models.Topic
	if err := r.db.Find(&topics).Error; err != nil {
		return extract.ParseResult{}, fmt.Errorf("load topics: %w", err)
	}

	opts := extract.Options{
		Subjects: extract.NewSubjectMatcher(subjects),
		Topics:   extract.NewTopicMatcher(topics),
	}
	// When the exam already has a pattern, use its question count only to warn
	// about a suspicious yield. It never changes how parsing behaves.
	if doc.ExamID != nil {
		var pattern models.ExamPattern
		if err := r.db.Where("exam_id = ? AND is_active = ?", *doc.ExamID, true).
			First(&pattern).Error; err == nil {
			opts.ExpectedCount = pattern.TotalQuestions
			if pattern.OptionCount > 1 {
				opts.MaxOptions = pattern.OptionCount
			}
		}
	}

	return extract.Parse(markdown, opts), nil
}

// setDocumentStatus updates a document's lifecycle fields.
func (r *Runner) setDocumentStatus(docID uint, status models.Status, stage, notes string) {
	updates := map[string]any{"status": status, "stage": stage}
	if notes != "" {
		updates["notes"] = notes
	}
	if err := r.db.Model(&models.Document{}).Where("id = ?", docID).Updates(updates).Error; err != nil {
		// A status write failing is worth knowing about but must not abort the job.
		fmt.Printf("pipeline: could not update document %d status: %v\n", docID, err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
