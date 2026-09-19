package models

import "gorm.io/datatypes"

// ExamPattern is the blueprint for one exam: how many questions, over what
// duration, split across which sections and in what proportion.
//
// A pattern arrives one of two ways. The user types it in (PatternManual), or
// the user drops in one or more real question papers and the analyser infers
// it (PatternDerived). Patterns are versioned so a re-analysis never destroys
// what was there before; exactly one version per exam is active at a time.
type ExamPattern struct {
	Base
	ExamID uint  `gorm:"index;not null" json:"exam_id"`
	Exam   *Exam `json:"-"`

	Name    string        `gorm:"size:160" json:"name"`
	Version int           `gorm:"index;default:1" json:"version"`
	Source  PatternSource `gorm:"size:16;index;default:'manual'" json:"source"`

	// DerivedFromCount is how many documents the analyser consumed, and
	// Confidence is its agreement score across them (0..1). Both are zero for
	// manually entered patterns.
	DerivedFromCount int     `json:"derived_from_count"`
	Confidence       float64 `json:"confidence"`

	TotalQuestions   int     `json:"total_questions"`
	DurationMin      int     `json:"duration_min"`
	TotalMarks       float64 `json:"total_marks"`
	MarksPerQuestion float64 `gorm:"default:1" json:"marks_per_question"`
	NegativeMarks    float64 `json:"negative_marks"`
	// OptionCount is the observed number of choices per objective question.
	OptionCount int `json:"option_count"`

	IsActive bool           `gorm:"index" json:"is_active"`
	Notes    string         `gorm:"type:text" json:"notes"`
	Meta     datatypes.JSON `json:"meta,omitempty"`

	Sections []PatternSection `json:"sections,omitempty"`
}

// PatternSection is one section of a pattern: a subject, how many questions it
// contributes, and its share of the paper.
type PatternSection struct {
	Base
	ExamPatternID uint         `gorm:"index;not null" json:"exam_pattern_id"`
	ExamPattern   *ExamPattern `json:"-"`

	// SubjectID is nullable because the analyser may find a section whose
	// heading matches nothing in the subject catalogue yet. The user resolves
	// it afterwards from the pattern editor.
	SubjectID *uint    `gorm:"index" json:"subject_id,omitempty"`
	Subject   *Subject `json:"subject,omitempty"`

	// Name is the heading as printed in the source document, preserved so the
	// generated paper can reproduce the original wording.
	Name       string `gorm:"size:200" json:"name"`
	OrderIndex int    `gorm:"index" json:"order_index"`

	QuestionCount    int     `json:"question_count"`
	Weightage        float64 `json:"weightage"` // percent share of the paper
	MarksPerQuestion float64 `gorm:"default:1" json:"marks_per_question"`
	NegativeMarks    float64 `json:"negative_marks"`

	// DifficultyMix is the target easy/medium/hard split for this section.
	DifficultyMix datatypes.JSON `json:"difficulty_mix,omitempty"`
	Meta          datatypes.JSON `json:"meta,omitempty"`
}

// ExamAssociation lets one exam draw questions from another. This is the
// content-sharing primitive: if a new exam tests arithmetic at a level
// comparable to an exam already in the warehouse, the user associates them and
// the generator may pull that exam's arithmetic questions.
//
// The link is directed. ExamID is the exam that gains access; SourceExamID is
// the exam whose content becomes available. Associating A -> B does not let B
// use A's questions.
type ExamAssociation struct {
	Base
	// ExamID is the borrower.
	ExamID uint  `gorm:"index;not null" json:"exam_id"`
	Exam   *Exam `gorm:"foreignKey:ExamID" json:"-"`

	// SourceExamID is the exam being borrowed from.
	SourceExamID uint  `gorm:"index;not null" json:"source_exam_id"`
	SourceExam   *Exam `gorm:"foreignKey:SourceExamID" json:"source_exam,omitempty"`

	// SubjectID scopes the association to a single subject. Nil means every
	// subject the source exam covers.
	SubjectID *uint    `gorm:"index" json:"subject_id,omitempty"`
	Subject   *Subject `json:"subject,omitempty"`

	// Similarity is the user's judgement of how comparable the two are (0..1).
	// The generator prefers higher-similarity sources when it has a choice.
	Similarity float64 `gorm:"default:0.8" json:"similarity"`

	// MaxSharePct caps how much of a section may come from this source, so a
	// borrowed exam cannot swamp the exam's own material. 0 means uncapped.
	MaxSharePct float64 `json:"max_share_pct"`

	Enabled bool   `gorm:"default:true;index" json:"enabled"`
	Note    string `gorm:"type:text" json:"note"`
}
