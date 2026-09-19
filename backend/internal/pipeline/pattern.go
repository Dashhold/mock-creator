package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"mockcreator/internal/models"
	"mockcreator/internal/pattern"

	"gorm.io/gorm"
)

// PatternParams is the request stored on a pattern-analysis job.
type PatternParams struct {
	// DocumentIDs are the papers to learn from. Empty means every completed
	// question paper already attached to the exam.
	DocumentIDs []uint `json:"document_ids,omitempty"`
	Name        string `json:"name,omitempty"`
	// Activate makes the derived pattern the exam's current one.
	Activate bool `json:"activate"`
	// RoundTo snaps section counts to a multiple, smoothing extraction noise.
	RoundTo int `json:"round_to,omitempty"`
	// IngestAfter also extracts the questions from these papers, so dropping a
	// paper in gives you both the pattern and the content in one action.
	IngestAfter bool `json:"ingest_after"`
}

// PatternAnalysisResult is what the job returns.
type PatternAnalysisResult struct {
	ExamID        uint          `json:"exam_id"`
	PatternID     uint          `json:"pattern_id"`
	Version       int           `json:"version"`
	Activated     bool          `json:"activated"`
	DocumentsUsed int           `json:"documents_used"`
	Draft         pattern.Draft `json:"draft"`
	IngestJobIDs  []uint        `json:"ingest_job_ids,omitempty"`
	DurationMS    int64         `json:"duration_ms"`
}

// runPatternAnalysis infers an exam's pattern from real question papers.
func (r *Runner) runPatternAnalysis(ctx context.Context, job *models.Job) (any, error) {
	started := time.Now()

	if job.ExamID == nil {
		return nil, errors.New("pattern analysis job has no exam")
	}
	var exam models.Exam
	if err := r.db.First(&exam, *job.ExamID).Error; err != nil {
		return nil, fmt.Errorf("load exam %d: %w", *job.ExamID, err)
	}

	var params PatternParams
	if err := decodeParams(job, &params); err != nil {
		return nil, err
	}

	docs, err := r.patternDocuments(exam.ID, params.DocumentIDs)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, errors.New("no documents to analyse; upload at least one question paper for this exam")
	}

	r.counters(job.ID, 0, len(docs))

	observations := make([]pattern.Observation, 0, len(docs))
	for i := range docs {
		doc := docs[i]
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		r.progress(job.ID, fmt.Sprintf("reading %s", doc.Title), 5+i*70/len(docs))

		conversion, _, err := r.ensureConversion(ctx, job, &doc, IngestParams{})
		if err != nil {
			// One unreadable paper should not sink the whole analysis.
			log.Printf("pipeline: pattern analysis skipping document %d: %v", doc.ID, err)
			r.counters(job.ID, i+1, len(docs))
			continue
		}

		parsed, err := r.parseDocument(&doc, conversion.Markdown)
		if err != nil {
			log.Printf("pipeline: pattern analysis could not parse document %d: %v", doc.ID, err)
			r.counters(job.ID, i+1, len(docs))
			continue
		}

		observations = append(observations, pattern.Observation{
			DocumentID: doc.ID,
			Title:      doc.Title,
			Year:       doc.Year,
			Markdown:   conversion.Markdown,
			Result:     parsed,
		})
		r.counters(job.ID, i+1, len(docs))
	}

	if len(observations) == 0 {
		return nil, errors.New("none of the supplied documents could be read; check the upload and try forcing OCR")
	}

	r.progress(job.ID, "inferring pattern", 80)
	draft := pattern.Analyze(observations, pattern.Options{RoundTo: params.RoundTo})

	r.progress(job.ID, "saving pattern", 90)
	saved, err := r.savePattern(&exam, draft, params)
	if err != nil {
		return nil, err
	}

	result := PatternAnalysisResult{
		ExamID:        exam.ID,
		PatternID:     saved.ID,
		Version:       saved.Version,
		Activated:     saved.IsActive,
		DocumentsUsed: len(observations),
		Draft:         draft,
		DurationMS:    time.Since(started).Milliseconds(),
	}

	if params.IngestAfter {
		result.IngestJobIDs = r.enqueueIngestForPatternDocs(observations)
	}

	return result, nil
}

// patternDocuments resolves which documents to analyse.
func (r *Runner) patternDocuments(examID uint, ids []uint) ([]models.Document, error) {
	query := r.db.Model(&models.Document{})
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	} else {
		query = query.Where("exam_id = ? AND kind = ?", examID, models.KindQuestionPaper)
	}

	var docs []models.Document
	if err := query.Order("year asc, created_at asc").Find(&docs).Error; err != nil {
		return nil, fmt.Errorf("load documents: %w", err)
	}
	return docs, nil
}

// savePattern writes the derived pattern as a new version and wires up the
// exam's subject list so the taxonomy matches what the papers actually contain.
func (r *Runner) savePattern(
	exam *models.Exam,
	draft pattern.Draft,
	params PatternParams,
) (*models.ExamPattern, error) {
	name := params.Name
	if name == "" {
		name = fmt.Sprintf("Derived from %d paper(s)", draft.DerivedFromCount)
	}

	var record models.ExamPattern

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var maxVersion int
		if err := tx.Model(&models.ExamPattern{}).
			Where("exam_id = ?", exam.ID).
			Select("COALESCE(MAX(version), 0)").
			Scan(&maxVersion).Error; err != nil {
			return fmt.Errorf("read current version: %w", err)
		}

		plan, sections := draft.ToModels(exam.ID, name, maxVersion+1)
		plan.IsActive = params.Activate

		if params.Activate {
			if err := tx.Model(&models.ExamPattern{}).
				Where("exam_id = ?", exam.ID).
				Update("is_active", false).Error; err != nil {
				return fmt.Errorf("deactivate previous patterns: %w", err)
			}
		}

		if err := tx.Create(&plan).Error; err != nil {
			return fmt.Errorf("create pattern: %w", err)
		}
		for i := range sections {
			sections[i].ExamPatternID = plan.ID
			mix := models.DefaultMixFor(models.PaperBalanced)
			if encoded, err := json.Marshal(mix); err == nil {
				sections[i].DifficultyMix = encoded
			}
		}
		if len(sections) > 0 {
			if err := tx.Create(&sections).Error; err != nil {
				return fmt.Errorf("create pattern sections: %w", err)
			}
		}

		plan.Sections = sections
		record = plan

		// Every subject the papers turned out to contain becomes part of the
		// exam's taxonomy, so the generator and Warehouse agree with the pattern.
		return linkExamSubjects(tx, exam.ID, draft)
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// linkExamSubjects creates ExamSubject rows for the subjects a pattern uses.
func linkExamSubjects(tx *gorm.DB, examID uint, draft pattern.Draft) error {
	for order, section := range draft.Sections {
		if section.SubjectID == nil {
			continue
		}
		link := models.ExamSubject{
			ExamID:      examID,
			SubjectID:   *section.SubjectID,
			DisplayName: section.Name,
			OrderIndex:  order,
		}
		err := tx.Where("exam_id = ? AND subject_id = ?", examID, *section.SubjectID).
			FirstOrCreate(&link, models.ExamSubject{
				ExamID:    examID,
				SubjectID: *section.SubjectID,
			}).Error
		if err != nil {
			return fmt.Errorf("link exam subject: %w", err)
		}
	}
	return nil
}

// enqueueIngestForPatternDocs queues question extraction for papers that were
// analysed but never mined, so one action gives both pattern and content.
func (r *Runner) enqueueIngestForPatternDocs(observations []pattern.Observation) []uint {
	var jobIDs []uint
	for _, obs := range observations {
		var count int64
		if err := r.db.Model(&models.Question{}).
			Where("document_id = ?", obs.DocumentID).
			Count(&count).Error; err != nil || count > 0 {
			continue
		}

		docID := obs.DocumentID
		// The conversion is already cached, so this is a cheap parse-only pass.
		params, _ := json.Marshal(IngestParams{ParseOnly: true})
		job := &models.Job{
			Type:       models.JobIngest,
			DocumentID: &docID,
			Params:     params,
		}
		if err := r.Enqueue(job); err != nil {
			log.Printf("pipeline: could not queue ingest for document %d: %v", docID, err)
			continue
		}
		jobIDs = append(jobIDs, job.ID)
	}
	return jobIDs
}
