package pipeline

import (
	"errors"
	"fmt"
	"strings"

	"mockcreator/internal/extract"
	"mockcreator/internal/models"
	"mockcreator/internal/quality"

	"gorm.io/gorm"
)

// unsortedSubjectCode is where questions land when no section heading matched a
// subject. They stay visible in the Warehouse under one bucket so the gap is
// obvious and fixable by adding an alias, rather than silently lost.
const unsortedSubjectCode = "unsorted"

type saveStats struct {
	created   int
	reused    int
	answered  int
	unmatched int
	bySubject map[string]int

	// Quality outcomes, counted so an ingest reports what is usable rather than
	// only what was stored.
	passed     int
	needReview int
	failed     int
	passages   int
}

// saveContext carries what the conversion knows about itself into persistence,
// so every question can record which engine read it and how well.
type saveContext struct {
	engine      string
	confidence  float64
	optionCount int
}

// persistQuestions writes parsed questions, reusing any that already exist.
//
// Re-ingesting a document replaces the questions it previously contributed, so
// the operation is idempotent. Questions already placed in a test paper are kept
// and merely detached from the document, because deleting them would tear a hole
// in a paper someone may have published.
func (r *Runner) persistQuestions(
	doc *models.Document,
	parsed extract.ParseResult,
	sctx saveContext,
) (*saveStats, error) {
	stats := &saveStats{bySubject: map[string]int{}}
	if len(parsed.Questions) == 0 {
		return stats, nil
	}

	var topics []models.Topic
	if err := r.db.Find(&topics).Error; err != nil {
		return nil, fmt.Errorf("load topics: %w", err)
	}
	topicMatcher := extract.NewTopicMatcher(topics)

	checks := quality.Options{
		ExpectedOptions:         sctx.optionCount,
		UnsortedSubjectCode:     unsortedSubjectCode,
		MinExtractionConfidence: r.cfg.Converter.MinExtractionConfidence,
		ModelExpected:           r.llm.Available() && r.cfg.LLM.AuditQuestions,
	}

	var createdIDs []uint

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := clearDocumentQuestions(tx, doc.ID); err != nil {
			return err
		}

		// Shared passages are stored once and referenced. Folding a passage into
		// each question's text is what used to repeat two thousand characters
		// across six stems, which made them impossible to review and invisible to
		// duplicate detection.
		passages, err := upsertPassages(tx, doc, parsed.Questions)
		if err != nil {
			return err
		}
		stats.passages = len(passages)

		// The catch-all subject is created only if a question actually needs it,
		// so a clean run does not leave an empty "Unsorted" entry in the
		// taxonomy for the user to wonder about.
		fallbackSubject := uint(0)
		unsorted := func() (uint, error) {
			if fallbackSubject != 0 {
				return fallbackSubject, nil
			}
			id, err := ensureUnsortedSubject(tx)
			if err != nil {
				return 0, err
			}
			fallbackSubject = id
			return id, nil
		}

		labels := []string{"A", "B", "C", "D", "E", "F", "G", "H"}

		for _, pq := range parsed.Questions {
			// The stem is stored as the stem. Shared material is referenced, not
			// copied in.
			text := strings.TrimSpace(pq.Text)
			if text == "" && pq.FilledOptions() == 0 {
				continue
			}

			var subjectID uint
			subjectKey := unsortedSubjectCode
			if pq.Subject != nil && pq.Subject.ID != 0 {
				subjectID = pq.Subject.ID
				subjectKey = pq.Subject.Code
			} else {
				id, err := unsorted()
				if err != nil {
					return err
				}
				subjectID = id
				stats.unmatched++
			}

			var passageID *uint
			passageText := ""
			if pq.ContextKey != "" {
				if passage, found := passages[pq.ContextKey]; found {
					id := passage.ID
					passageID = &id
					passageText = passage.Text
				}
			}

			hash := extract.ContentHash(pq.Text, pq.OptionTexts())

			// A question already in the warehouse is reused rather than copied, so
			// the same paper reaching us twice does not double the bank.
			var existing models.Question
			lookup := tx.Where("content_hash = ?", hash).
				Where("document_id IS NULL OR document_id <> ?", doc.ID).
				First(&existing).Error
			if lookup == nil {
				stats.reused++
				stats.bySubject[subjectKey]++
				createdIDs = append(createdIDs, existing.ID)
				// A reused question may now gain an answer it lacked before.
				if !existing.HasAnswer && pq.HasAnswer() {
					if err := applyParsedAnswer(tx, &existing, pq); err != nil {
						return err
					}
				}
				continue
			}
			if !errors.Is(lookup, gorm.ErrRecordNotFound) {
				return fmt.Errorf("duplicate lookup: %w", lookup)
			}

			question := models.Question{
				SubjectID:            subjectID,
				OriginExamID:         doc.ExamID,
				PassageID:            passageID,
				QuestionText:         text,
				Type:                 pq.Type,
				Difficulty:           models.DifficultyMedium,
				DifficultyConfidence: 0, // extraction cannot judge hardness
				Explanation:          pq.Explanation,
				AnswerText:           pq.AnswerText,
				Origin:               models.OriginExtracted,
				Year:                 doc.Year,
				Language:             doc.Language,
				DocumentID:           &doc.ID,
				QuestionNumber:       pq.Number,
				PageNo:               pq.PageNo,
				SourceFirstLine:      pq.SourceFirstLine,
				SourceLastLine:       pq.SourceLastLine,
				ExtractEngine:        sctx.engine,
				ExtractionConfidence: pq.Confidence,
				TrailingText:         pq.Trailing,
				ContentHash:          hash,
				HasAnswer:            pq.HasAnswer(),
				Status:               models.StatusPending,
			}

			if topic, matched := topicMatcher.Match(pq.Text, subjectID); matched && topic.Confidence >= 0.7 {
				topicID := topic.ID
				question.TopicID = &topicID
			}

			for i, opt := range pq.Options {
				optText := strings.TrimSpace(opt.Text)
				if optText == "" {
					continue
				}
				label := opt.Label
				if label == "" && i < len(labels) {
					label = labels[i]
				}
				question.Options = append(question.Options, models.QuestionOption{
					Label:      label,
					Text:       optText,
					OrderIndex: i,
					IsCorrect:  opt.IsCorrect,
				})
			}

			// Validate before storing, so nothing enters the warehouse without a
			// verdict. Unchecked is not a pass, and a question that has not been
			// looked at can never be picked for a paper.
			issues := quality.ValidateQuestion(toQualityInput(pq, subjectKey, passageText), checks)
			question.Apply(issues, quality.Score(issues, pq.Confidence), quality.RulesVersion)

			// A question read out of a real paper was already vetted by whoever set
			// that paper, so it may enter approved without a human. That trust
			// applies to the *source*, not to the extraction: anything with an open
			// issue waits for a person regardless of the setting.
			if r.cfg.Review.AutoApproveExtracted && question.QualityStatus == models.QualityPass {
				question.Status = models.StatusApproved
			}

			if err := tx.Create(&question).Error; err != nil {
				return fmt.Errorf("create question %d: %w", pq.Number, err)
			}

			createdIDs = append(createdIDs, question.ID)
			stats.created++
			stats.bySubject[subjectKey]++
			if question.HasAnswer {
				stats.answered++
			}
			switch question.QualityStatus {
			case models.QualityPass:
				stats.passed++
			case models.QualityFailed:
				stats.failed++
			default:
				stats.needReview++
			}
		}

		return syncLinksForQuestions(tx, createdIDs, doc.ExamID)
	})
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// toQualityInput adapts a parsed question to the quality engine's input.
func toQualityInput(pq extract.ParsedQuestion, subjectCode, passageText string) quality.Input {
	options := make([]quality.OptionInput, 0, len(pq.Options))
	for _, o := range pq.Options {
		options = append(options, quality.OptionInput{
			Label: o.Label, Text: o.Text, IsCorrect: o.IsCorrect,
		})
	}
	return quality.Input{
		Type:                 pq.Type,
		Stem:                 pq.Text,
		Options:              options,
		AnswerText:           pq.AnswerText,
		Explanation:          pq.Explanation,
		PassageText:          passageText,
		TrailingText:         pq.Trailing,
		SubjectCode:          subjectCode,
		ExtractIssues:        pq.Issues,
		ExtractionConfidence: pq.Confidence,
	}
}

// upsertPassages stores each distinct shared passage once and returns them keyed
// by the fingerprint the parser assigned.
//
// Reusing an existing row by hash means the same comprehension passage arriving
// from a second paper links to one stored copy, so correcting it corrects it
// everywhere.
func upsertPassages(
	tx *gorm.DB,
	doc *models.Document,
	questions []extract.ParsedQuestion,
) (map[string]*models.Passage, error) {
	type span struct {
		text     string
		from, to int
		explicit bool
		subject  *uint
	}
	spans := map[string]*span{}

	for _, q := range questions {
		key := q.ContextKey
		if key == "" || strings.TrimSpace(q.Context) == "" {
			continue
		}
		entry, seen := spans[key]
		if !seen {
			entry = &span{text: q.Context, from: q.Number, to: q.Number}
			if q.Subject != nil && q.Subject.ID != 0 {
				id := q.Subject.ID
				entry.subject = &id
			}
			// A range the document printed itself is authoritative; one we inferred
			// is recorded as inferred so a reviewer knows which is which.
			entry.explicit = !q.HasIssue(extract.IssueContextCapped)
			spans[key] = entry
			continue
		}
		if q.Number < entry.from {
			entry.from = q.Number
		}
		if q.Number > entry.to {
			entry.to = q.Number
		}
	}

	out := make(map[string]*models.Passage, len(spans))
	for key, entry := range spans {
		passage := models.Passage{
			ContentHash:   key,
			Text:          entry.text,
			Kind:          "passage",
			SubjectID:     entry.subject,
			DocumentID:    &doc.ID,
			FromQuestion:  entry.from,
			ToQuestion:    entry.to,
			RangeExplicit: entry.explicit,
			WordCount:     len(strings.Fields(entry.text)),
		}

		var existing models.Passage
		err := tx.Where("content_hash = ?", key).First(&existing).Error
		switch {
		case err == nil:
			// Widen the recorded range if this document uses the passage further.
			updates := map[string]any{}
			if passage.FromQuestion < existing.FromQuestion || existing.FromQuestion == 0 {
				updates["from_question"] = passage.FromQuestion
			}
			if passage.ToQuestion > existing.ToQuestion {
				updates["to_question"] = passage.ToQuestion
			}
			if len(updates) > 0 {
				if err := tx.Model(&existing).Updates(updates).Error; err != nil {
					return nil, fmt.Errorf("update passage: %w", err)
				}
			}
			out[key] = &existing
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(&passage).Error; err != nil {
				return nil, fmt.Errorf("create passage: %w", err)
			}
			stored := passage
			out[key] = &stored
		default:
			return nil, fmt.Errorf("lookup passage: %w", err)
		}
	}
	return out, nil
}

// clearDocumentQuestions removes the questions a document previously produced.
func clearDocumentQuestions(tx *gorm.DB, docID uint) error {
	var ids []uint
	if err := tx.Model(&models.Question{}).
		Where("document_id = ?", docID).
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("list existing questions: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	// Questions already used in a paper are preserved; they simply stop being
	// attributed to this document.
	var inUse []uint
	if err := tx.Model(&models.TestPaperItem{}).
		Where("question_id IN ?", ids).
		Distinct().
		Pluck("question_id", &inUse).Error; err != nil {
		return fmt.Errorf("check paper usage: %w", err)
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
			return fmt.Errorf("detach in-use questions: %w", err)
		}
	}
	if len(removable) == 0 {
		return nil
	}

	for _, stmt := range []func() error{
		func() error {
			return tx.Unscoped().Where("question_id IN ?", removable).
				Delete(&models.QuestionOption{}).Error
		},
		func() error {
			return tx.Unscoped().Where("question_id IN ?", removable).
				Delete(&models.QualityReview{}).Error
		},
		func() error {
			return tx.Where("question_id IN ?", removable).
				Delete(&models.QuestionExamLink{}).Error
		},
		func() error {
			return tx.Unscoped().Where("id IN ?", removable).Delete(&models.Question{}).Error
		},
	} {
		if err := stmt(); err != nil {
			return fmt.Errorf("clear prior questions: %w", err)
		}
	}
	return nil
}

// ensureUnsortedSubject returns the catch-all subject, creating it on demand.
func ensureUnsortedSubject(tx *gorm.DB) (uint, error) {
	var subject models.Subject
	err := tx.Where("code = ?", unsortedSubjectCode).First(&subject).Error
	if err == nil {
		return subject.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("lookup unsorted subject: %w", err)
	}

	subject = models.Subject{
		Name: "Unsorted",
		Code: unsortedSubjectCode,
		Description: "Questions whose section heading did not match any subject. " +
			"Add the heading as an alias on the right subject and re-parse the document.",
	}
	if err := tx.Create(&subject).Error; err != nil {
		return 0, fmt.Errorf("create unsorted subject: %w", err)
	}
	return subject.ID, nil
}

// applyParsedAnswer fills in the answer for a question that did not have one.
func applyParsedAnswer(tx *gorm.DB, question *models.Question, pq extract.ParsedQuestion) error {
	var options []models.QuestionOption
	if err := tx.Where("question_id = ?", question.ID).
		Order("order_index asc").Find(&options).Error; err != nil {
		return fmt.Errorf("load options: %w", err)
	}

	correct := -1
	for i, opt := range pq.Options {
		if opt.IsCorrect {
			correct = i
			break
		}
	}
	if correct < 0 || correct >= len(options) {
		if strings.TrimSpace(pq.AnswerText) == "" {
			return nil
		}
		return tx.Model(question).Updates(map[string]any{
			"answer_text": pq.AnswerText,
			"has_answer":  true,
		}).Error
	}

	for i := range options {
		want := i == correct
		if options[i].IsCorrect == want {
			continue
		}
		if err := tx.Model(&models.QuestionOption{}).Where("id = ?", options[i].ID).
			Update("is_correct", want).Error; err != nil {
			return fmt.Errorf("mark correct option: %w", err)
		}
	}
	return tx.Model(question).Update("has_answer", true).Error
}

// applyAnswerKeyDocument completes questions using a standalone key document.
//
// Keys are matched to questions by printed question number within the same exam
// and year, which is how they are printed and how a human would match them.
func (r *Runner) applyAnswerKeyDocument(doc *models.Document, markdown string) (int, error) {
	maxOptions := 6
	if doc.ExamID != nil {
		var pattern models.ExamPattern
		if err := r.db.Where("exam_id = ? AND is_active = ?", *doc.ExamID, true).
			First(&pattern).Error; err == nil && pattern.OptionCount > 1 {
			maxOptions = pattern.OptionCount
		}
	}

	key, explanations := extract.ParseAnswerKeyDocument(markdown, maxOptions)
	if len(key) == 0 && len(explanations) == 0 {
		return 0, nil
	}

	query := r.db.Model(&models.Question{}).Where("question_number > 0")
	switch {
	case doc.ExamID != nil:
		query = query.Where("origin_exam_id = ?", *doc.ExamID)
		if doc.Year != nil {
			query = query.Where("year = ?", *doc.Year)
		}
	default:
		// With no exam given, fall back to the most recently ingested question
		// paper so an ad-hoc key still has an obvious target.
		var latest models.Document
		if err := r.db.Where("kind = ? AND status = ?", models.KindQuestionPaper, models.StatusCompleted).
			Order("created_at desc").First(&latest).Error; err != nil {
			return 0, errors.New("this key is not attached to an exam and no ingested question paper was found")
		}
		query = query.Where("document_id = ?", latest.ID)
	}

	var questions []models.Question
	if err := query.Find(&questions).Error; err != nil {
		return 0, fmt.Errorf("load target questions: %w", err)
	}
	if len(questions) == 0 {
		return 0, nil
	}

	applied := 0
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for i := range questions {
			question := &questions[i]
			changed := false

			if explanation, found := explanations[question.QuestionNumber]; found && question.Explanation == "" {
				if err := tx.Model(question).Update("explanation", explanation).Error; err != nil {
					return fmt.Errorf("save explanation: %w", err)
				}
				changed = true
			}

			index, found := key[question.QuestionNumber]
			if !found || question.HasAnswer {
				if changed {
					applied++
				}
				continue
			}

			var options []models.QuestionOption
			if err := tx.Where("question_id = ?", question.ID).
				Order("order_index asc").Find(&options).Error; err != nil {
				return fmt.Errorf("load options: %w", err)
			}
			if index < 0 || index >= len(options) {
				continue
			}
			for k := range options {
				want := k == index
				if options[k].IsCorrect == want {
					continue
				}
				if err := tx.Model(&models.QuestionOption{}).Where("id = ?", options[k].ID).
					Update("is_correct", want).Error; err != nil {
					return fmt.Errorf("mark correct option: %w", err)
				}
			}
			if err := tx.Model(question).Update("has_answer", true).Error; err != nil {
				return fmt.Errorf("flag answered: %w", err)
			}
			applied++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}
