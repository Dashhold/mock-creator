package models

import (
	"time"

	"gorm.io/datatypes"
)

// Job is a unit of background work. Every long operation in the system runs
// through this table so the API can answer immediately and the UI can track
// progress instead of holding an HTTP request open for minutes.
//
// One table covers all pipelines rather than one per pipeline, because the UI
// wants a single "what is happening right now" feed.
type Job struct {
	Base
	Type   JobType `gorm:"size:24;index;not null" json:"type"`
	Status Status  `gorm:"size:20;index;not null;default:'queued'" json:"status"`

	// Stage is a short human-readable step name, e.g. "converting",
	// "running ocr", "parsing questions", "linking exams".
	Stage    string `gorm:"size:80" json:"stage"`
	Progress int    `json:"progress"` // 0..100

	Total     int `json:"total"`
	Processed int `json:"processed"`

	// Subject of the work. Which of these is set depends on Type.
	DocumentID *uint     `gorm:"index" json:"document_id,omitempty"`
	Document   *Document `json:"document,omitempty"`
	ExamID     *uint     `gorm:"index" json:"exam_id,omitempty"`
	Exam       *Exam     `json:"exam,omitempty"`
	PaperID    *uint     `gorm:"index" json:"paper_id,omitempty"`

	// Params is the request that created the job, kept so a job can be retried
	// exactly as it was first submitted.
	Params datatypes.JSON `json:"params,omitempty"`
	Result datatypes.JSON `json:"result,omitempty"`
	Error  string         `gorm:"type:text" json:"error,omitempty"`

	Attempts   int        `json:"attempts"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// CancelRequested is polled by the worker between stages so a long
	// conversion can be abandoned without killing the process.
	CancelRequested bool `gorm:"default:false" json:"cancel_requested"`
}

// Active reports whether the job is still in flight.
func (j Job) Active() bool {
	return !j.Status.Terminal()
}

// DurationMS returns how long the job ran, or how long it has been running.
func (j Job) DurationMS() int64 {
	if j.StartedAt == nil {
		return 0
	}
	end := time.Now()
	if j.FinishedAt != nil {
		end = *j.FinishedAt
	}
	return end.Sub(*j.StartedAt).Milliseconds()
}
