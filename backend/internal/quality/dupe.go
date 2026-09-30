package quality

import (
	"fmt"
	"strings"

	"mockcreator/internal/models"

	"gorm.io/gorm"
)

// NearDuplicateThreshold is the trigram similarity above which two stems are
// treated as the same question.
//
// Exact duplicates are already caught by the content hash before a question is
// stored. This catches the harder case: the same question reprinted with a
// different number, a reworded preamble or a typo fixed. 0.82 was chosen because
// below it genuine sibling questions from one paper start matching each other
// (they share long instruction phrases), and above it a single edited word is
// enough to slip through.
const NearDuplicateThreshold = 0.82

// DuplicateMatch is another question that says the same thing.
type DuplicateMatch struct {
	QuestionID uint    `json:"question_id"`
	Similarity float64 `json:"similarity"`
	Stem       string  `json:"stem"`
	SubjectID  uint    `json:"subject_id"`
	Exact      bool    `json:"exact"`
}

// Duplicates finds questions that duplicate the given one.
//
// Exact matches come from the content hash, which is order-insensitive across
// options. Near matches come from PostgreSQL trigram similarity, restricted to
// the same subject because the index makes that cheap and because a physics
// question is not a duplicate of an English one however similar the wording.
func Duplicates(db *gorm.DB, questionID, subjectID uint, contentHash, stem string) ([]DuplicateMatch, error) {
	var out []DuplicateMatch

	if contentHash != "" {
		var exact []DuplicateMatch
		err := db.Table("questions").
			Select("id AS question_id, 1.0 AS similarity, question_text AS stem, subject_id, true AS exact").
			Where("content_hash = ?", contentHash).
			Where("id <> ?", questionID).
			Where("deleted_at IS NULL").
			Limit(5).
			Scan(&exact).Error
		if err != nil {
			return nil, fmt.Errorf("exact duplicate lookup: %w", err)
		}
		out = append(out, exact...)
	}

	stem = strings.TrimSpace(stem)
	// Very short stems match each other on trigrams by accident, so they are
	// left to the exact check alone.
	if len(stem) >= 40 {
		var near []DuplicateMatch
		err := db.Table("questions").
			Select("id AS question_id, similarity(question_text, ?) AS similarity, question_text AS stem, subject_id, false AS exact", stem).
			Where("subject_id = ?", subjectID).
			Where("id <> ?", questionID).
			Where("deleted_at IS NULL").
			Where("similarity(question_text, ?) >= ?", stem, NearDuplicateThreshold).
			Order("similarity DESC").
			Limit(5).
			Scan(&near).Error
		if err != nil {
			// Similarity needs pg_trgm. Without it duplicate detection degrades to
			// exact matching, which is stated rather than hidden.
			return out, fmt.Errorf("near-duplicate lookup (is pg_trgm installed?): %w", err)
		}
		for _, match := range near {
			if !containsID(out, match.QuestionID) {
				out = append(out, match)
			}
		}
	}

	return out, nil
}

// DuplicateIssues turns duplicate matches into quality issues.
//
// An exact duplicate is critical: two identical questions in one paper is the
// kind of defect a client notices immediately. A near duplicate is major,
// because deciding whether a reworded question is the same question needs
// judgement the rules do not have.
func DuplicateIssues(matches []DuplicateMatch) models.QualityIssues {
	var out models.QualityIssues
	for _, match := range matches {
		severity := models.SeverityMajor
		code := CodeNearDuplicate
		message := fmt.Sprintf("question %d says nearly the same thing (%.0f%% similar)",
			match.QuestionID, match.Similarity*100)
		if match.Exact {
			severity = models.SeverityCritical
			code = CodeDuplicateQuestion
			message = fmt.Sprintf("question %d is the same question", match.QuestionID)
		}
		out = append(out, models.QualityIssue{
			Code:     code,
			Severity: severity,
			Source:   models.SourceRules,
			Message:  message,
			Evidence: trim(match.Stem, 110),
		})
	}
	return out
}

func containsID(list []DuplicateMatch, id uint) bool {
	for _, item := range list {
		if item.QuestionID == id {
			return true
		}
	}
	return false
}
