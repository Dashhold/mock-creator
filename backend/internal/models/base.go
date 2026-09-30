// Package models defines the persistence schema for the content engine.
//
// The engine is deliberately exam-agnostic: nothing in this package encodes
// knowledge about a specific examination, board or syllabus. Every piece of
// exam-specific behaviour (which subjects exist, how a paper is structured,
// how sections are labelled, which other exams may be drawn from) lives in
// data rows the user creates, not in Go code.
package models

import (
	"time"

	"gorm.io/gorm"
)

// Base is embedded in every model to provide common fields.
type Base struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Difficulty is the graded hardness of a question.
type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

// AllDifficulties lists every difficulty in ascending order.
func AllDifficulties() []Difficulty {
	return []Difficulty{DifficultyEasy, DifficultyMedium, DifficultyHard}
}

// Valid reports whether d is a known difficulty.
func (d Difficulty) Valid() bool {
	switch d {
	case DifficultyEasy, DifficultyMedium, DifficultyHard:
		return true
	}
	return false
}

// QuestionType captures the structural format of a question. The set is
// intentionally broad because the engine ingests material from any exam.
type QuestionType string

const (
	TypeMCQ             QuestionType = "mcq"
	TypeMultiSelect     QuestionType = "multi_select"
	TypeTrueFalse       QuestionType = "true_false"
	TypeFillBlank       QuestionType = "fill_blank"
	TypeAssertionReason QuestionType = "assertion_reason"
	TypeMatchColumns    QuestionType = "match_columns"
	TypeNumeric         QuestionType = "numeric"
	TypeDescriptive     QuestionType = "descriptive"
	TypeComprehension   QuestionType = "comprehension"
)

// Origin records how a question entered the warehouse.
type Origin string

const (
	// OriginExtracted means the question was machine-read out of a document.
	OriginExtracted Origin = "extracted"
	// OriginGenerated means a model produced it.
	OriginGenerated Origin = "generated"
	// OriginManual means a person typed it in.
	OriginManual Origin = "manual"
)

// DocumentKind classifies an uploaded file by the role it plays, not by the
// exam it belongs to. A document may belong to no exam at all.
type DocumentKind string

const (
	KindQuestionPaper DocumentKind = "question_paper"
	KindAnswerKey     DocumentKind = "answer_key"
	KindSolution      DocumentKind = "solution"
	KindBook          DocumentKind = "book"
	KindNotes         DocumentKind = "notes"
	KindSyllabus      DocumentKind = "syllabus"
	KindOther         DocumentKind = "other"
)

// Valid reports whether k is a known document kind.
func (k DocumentKind) Valid() bool {
	switch k {
	case KindQuestionPaper, KindAnswerKey, KindSolution,
		KindBook, KindNotes, KindSyllabus, KindOther:
		return true
	}
	return false
}

// Status is the shared lifecycle vocabulary for documents, jobs, questions,
// reviews and papers. Not every value applies to every entity.
type Status string

const (
	StatusPending    Status = "pending"
	StatusQueued     Status = "queued"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusDraft      Status = "draft"
	StatusPublished  Status = "published"
)

// Terminal reports whether a status represents finished work.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// JobType identifies which pipeline a Job drives.
type JobType string

const (
	// JobIngest converts a document and extracts questions from it.
	JobIngest JobType = "ingest"
	// JobPatternAnalysis infers an exam pattern from one or more documents.
	JobPatternAnalysis JobType = "pattern_analysis"
	// JobPaperBuild assembles a test paper from the warehouse.
	JobPaperBuild JobType = "paper_build"
	// JobQuestionAudit re-validates questions and, when a model is configured,
	// has it review them. It runs separately from ingest so a slow or unreachable
	// model cannot hold an upload open, and so review can be re-run on its own.
	JobQuestionAudit JobType = "question_audit"
	// JobPaperQA runs the final review of an assembled paper.
	JobPaperQA JobType = "paper_qa"
)

// PatternSource records whether a pattern was typed in or inferred.
type PatternSource string

const (
	PatternManual  PatternSource = "manual"
	PatternDerived PatternSource = "derived"
)

// LinkType distinguishes a question's own exam from exams that reuse it
// through an association.
type LinkType string

const (
	// LinkDirect means the question was extracted from that exam's material.
	LinkDirect LinkType = "direct"
	// LinkAssociated means another exam borrows it via an ExamAssociation.
	LinkAssociated LinkType = "associated"
)

// PaperType is the flavour of an assembled paper. These describe difficulty
// and sampling intent, not any particular examination.
type PaperType string

const (
	PaperBalanced PaperType = "balanced"
	PaperEasy     PaperType = "easy"
	PaperTough    PaperType = "tough"
	PaperPrevious PaperType = "previous_style"
	PaperRecent   PaperType = "recent_trend"
	PaperSpeed    PaperType = "speed"
	PaperMixed    PaperType = "mixed"
	PaperCustom   PaperType = "custom"
)

// DifficultyMix is the target share of each difficulty, in percent.
type DifficultyMix struct {
	Easy   int `json:"easy"`
	Medium int `json:"medium"`
	Hard   int `json:"hard"`
}

// DefaultMixFor returns a sensible difficulty split for a paper type. It is a
// starting point the user can always override per section.
func DefaultMixFor(t PaperType) DifficultyMix {
	switch t {
	case PaperEasy:
		return DifficultyMix{Easy: 60, Medium: 35, Hard: 5}
	case PaperTough:
		return DifficultyMix{Easy: 10, Medium: 35, Hard: 55}
	case PaperSpeed:
		return DifficultyMix{Easy: 55, Medium: 40, Hard: 5}
	default:
		return DifficultyMix{Easy: 30, Medium: 50, Hard: 20}
	}
}

// Normalized scales the mix so the three shares sum to 100.
func (m DifficultyMix) Normalized() DifficultyMix {
	total := m.Easy + m.Medium + m.Hard
	if total <= 0 {
		return DifficultyMix{Easy: 30, Medium: 50, Hard: 20}
	}
	if total == 100 {
		return m
	}
	easy := m.Easy * 100 / total
	medium := m.Medium * 100 / total
	return DifficultyMix{Easy: easy, Medium: medium, Hard: 100 - easy - medium}
}
