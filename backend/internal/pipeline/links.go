package pipeline

import (
	"fmt"

	"mockcreator/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Exam links are derived data. A question belongs to a subject; which exams may
// use it follows from where it came from plus the associations the user has
// configured. Keeping that as rows rather than computing it per query is what
// lets the Warehouse show every exam tag on a question cheaply, and lets the
// generator filter by exam with a plain join.

// onConflictIgnore skips rows that already exist for a (question, exam) pair.
var onConflictIgnore = clause.OnConflict{
	Columns:   []clause.Column{{Name: "question_id"}, {Name: "exam_id"}},
	DoNothing: true,
}

// syncLinksForQuestions links freshly ingested questions to their own exam and
// to every exam that borrows from it.
func syncLinksForQuestions(tx *gorm.DB, questionIDs []uint, examID *uint) error {
	if len(questionIDs) == 0 || examID == nil {
		return nil
	}

	direct := make([]models.QuestionExamLink, 0, len(questionIDs))
	for _, id := range questionIDs {
		direct = append(direct, models.QuestionExamLink{
			QuestionID: id,
			ExamID:     *examID,
			LinkType:   models.LinkDirect,
			Relevance:  1,
		})
	}
	if err := tx.Clauses(onConflictIgnore).CreateInBatches(direct, 300).Error; err != nil {
		return fmt.Errorf("create direct exam links: %w", err)
	}

	var associations []models.ExamAssociation
	if err := tx.Where("source_exam_id = ? AND enabled = ?", *examID, true).
		Find(&associations).Error; err != nil {
		return fmt.Errorf("load associations: %w", err)
	}
	if len(associations) == 0 {
		return nil
	}

	// Subjects are needed to honour a subject-scoped association.
	type row struct {
		ID        uint
		SubjectID uint
	}
	var rows []row
	if err := tx.Model(&models.Question{}).
		Select("id, subject_id").
		Where("id IN ?", questionIDs).
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("load question subjects: %w", err)
	}

	var borrowed []models.QuestionExamLink
	for _, assoc := range associations {
		assocID := assoc.ID
		for _, q := range rows {
			if assoc.SubjectID != nil && *assoc.SubjectID != q.SubjectID {
				continue
			}
			borrowed = append(borrowed, models.QuestionExamLink{
				QuestionID:    q.ID,
				ExamID:        assoc.ExamID,
				LinkType:      models.LinkAssociated,
				AssociationID: &assocID,
				Relevance:     assoc.Similarity,
			})
		}
	}
	if len(borrowed) == 0 {
		return nil
	}
	if err := tx.Clauses(onConflictIgnore).CreateInBatches(borrowed, 300).Error; err != nil {
		return fmt.Errorf("create borrowed exam links: %w", err)
	}
	return nil
}

// EnsureDirectLinks makes sure every question originating from an exam is linked
// to it. Used after attaching a document to an exam after the fact.
func EnsureDirectLinks(db *gorm.DB, examID uint) error {
	sql := `
		INSERT INTO question_exam_links (created_at, question_id, exam_id, link_type, relevance)
		SELECT NOW(), q.id, ?, ?, 1
		FROM questions q
		WHERE q.deleted_at IS NULL AND q.origin_exam_id = ?
		ON CONFLICT (question_id, exam_id) DO NOTHING`
	if err := db.Exec(sql, examID, models.LinkDirect, examID).Error; err != nil {
		return fmt.Errorf("ensure direct links for exam %d: %w", examID, err)
	}
	return nil
}

// RebuildExamLinks recomputes which borrowed questions an exam can draw on.
//
// Called whenever that exam's associations change. Direct links are untouched:
// an exam always keeps access to its own material.
func RebuildExamLinks(db *gorm.DB, examID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("exam_id = ? AND link_type = ?", examID, models.LinkAssociated).
			Delete(&models.QuestionExamLink{}).Error; err != nil {
			return fmt.Errorf("clear borrowed links: %w", err)
		}

		var associations []models.ExamAssociation
		if err := tx.Where("exam_id = ? AND enabled = ?", examID, true).
			Find(&associations).Error; err != nil {
			return fmt.Errorf("load associations: %w", err)
		}

		for _, assoc := range associations {
			if err := insertAssociatedLinks(tx, assoc); err != nil {
				return err
			}
		}
		return nil
	})
}

// RebuildLinksFromSource refreshes every exam that borrows from sourceExamID.
// Called after new questions land in the source exam.
func RebuildLinksFromSource(db *gorm.DB, sourceExamID uint) error {
	var associations []models.ExamAssociation
	if err := db.Where("source_exam_id = ? AND enabled = ?", sourceExamID, true).
		Find(&associations).Error; err != nil {
		return fmt.Errorf("load associations: %w", err)
	}
	for _, assoc := range associations {
		if err := insertAssociatedLinks(db, assoc); err != nil {
			return err
		}
	}
	return nil
}

// insertAssociatedLinks materialises one association as link rows.
func insertAssociatedLinks(db *gorm.DB, assoc models.ExamAssociation) error {
	relevance := assoc.Similarity
	if relevance <= 0 {
		relevance = 0.5
	}

	sql := `
		INSERT INTO question_exam_links
			(created_at, question_id, exam_id, link_type, association_id, relevance)
		SELECT NOW(), q.id, ?, ?, ?, ?
		FROM questions q
		WHERE q.deleted_at IS NULL AND q.origin_exam_id = ?`
	args := []any{assoc.ExamID, models.LinkAssociated, assoc.ID, relevance, assoc.SourceExamID}

	if assoc.SubjectID != nil {
		sql += ` AND q.subject_id = ?`
		args = append(args, *assoc.SubjectID)
	}
	sql += ` ON CONFLICT (question_id, exam_id) DO NOTHING`

	if err := db.Exec(sql, args...).Error; err != nil {
		return fmt.Errorf("materialise association %d: %w", assoc.ID, err)
	}
	return nil
}

// DeleteExamLinksForAssociation removes the links one association produced.
func DeleteExamLinksForAssociation(db *gorm.DB, associationID uint) error {
	if err := db.Where("association_id = ?", associationID).
		Delete(&models.QuestionExamLink{}).Error; err != nil {
		return fmt.Errorf("delete links for association %d: %w", associationID, err)
	}
	return nil
}
