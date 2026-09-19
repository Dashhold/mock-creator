package models

import "gorm.io/datatypes"

// Exam is a target examination the user defines. The engine ships with no
// exams; every row here is created by a user.
type Exam struct {
	Base
	Name string `gorm:"size:160;not null" json:"name"`
	// Code is unique among live rows only. The index is created in the database
	// package as a partial one, because a plain unique index would let a
	// soft-deleted exam hold its code hostage forever.
	Code        string `gorm:"size:60;not null" json:"code"`
	Description string `gorm:"type:text" json:"description"`
	// Language is a free-form hint (e.g. "en", "hi", "en+hi") used when
	// selecting questions, never for parsing decisions.
	Language string `gorm:"size:24;default:'en'" json:"language"`

	Status Status         `gorm:"size:20;index;default:'draft'" json:"status"`
	Meta   datatypes.JSON `json:"meta,omitempty"`

	ExamSubjects []ExamSubject     `json:"exam_subjects,omitempty"`
	Patterns     []ExamPattern     `json:"patterns,omitempty"`
	Associations []ExamAssociation `gorm:"foreignKey:ExamID" json:"associations,omitempty"`
}

// Subject is an entry in a global, user-editable subject catalogue. Subjects
// are shared across exams so that content can be reused: two exams both
// testing arithmetic point at the same Subject row.
type Subject struct {
	Base
	Name string `gorm:"size:120;not null" json:"name"`
	// Unique among live rows only; see the note on Exam.Code.
	Code string `gorm:"size:60;not null" json:"code"`

	// Aliases holds alternative headings this subject is printed under in real
	// documents, as a JSON array of strings. The section detector matches
	// document headings against these, which is how the parser stays
	// exam-agnostic: recognition data lives in the database, not in code.
	Aliases datatypes.JSON `json:"aliases,omitempty"`

	Description string `gorm:"type:text" json:"description"`

	Topics []Topic `json:"topics,omitempty"`
}

// Topic is a chapter inside a subject, e.g. "percentages" under arithmetic.
type Topic struct {
	Base
	SubjectID uint     `gorm:"index;not null" json:"subject_id"`
	Subject   *Subject `json:"-"`

	Name string `gorm:"size:160;not null" json:"name"`
	Code string `gorm:"size:80;index" json:"code"`

	// Aliases lets the tagger recognise this topic in headings and notes.
	Aliases datatypes.JSON `json:"aliases,omitempty"`
}

// ExamSubject links an exam to a subject it tests. It is an explicit join so
// each exam can keep its own display label for a shared subject: one exam may
// print "Quantitative Aptitude" where another prints "Numerical Ability",
// while both reference the same Subject row and therefore share content.
// The (exam, subject) pair is unique among live rows, enforced by a partial
// index created in the database package. Detaching a subject hard-deletes the
// row, so re-attaching it later works.
type ExamSubject struct {
	Base
	ExamID uint  `gorm:"index;not null" json:"exam_id"`
	Exam   *Exam `json:"-"`

	SubjectID uint     `gorm:"index;not null" json:"subject_id"`
	Subject   *Subject `json:"subject,omitempty"`

	// DisplayName is how this exam labels the subject. Empty means use
	// Subject.Name.
	DisplayName string `gorm:"size:160" json:"display_name"`
	OrderIndex  int    `json:"order_index"`
}

// Label returns the exam-specific label for the subject, falling back to the
// catalogue name.
func (es ExamSubject) Label() string {
	if es.DisplayName != "" {
		return es.DisplayName
	}
	if es.Subject != nil {
		return es.Subject.Name
	}
	return ""
}
