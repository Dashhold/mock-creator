package models

import (
	"time"

	"gorm.io/datatypes"
)

// Document is an uploaded file. Bytes live in the database (DocumentBlob), not
// on a mounted directory, so the only way material enters the system is
// through the API and nothing has to be copied into the image or a volume.
type Document struct {
	Base
	// ExamID is optional. A document can be generic reference material that
	// belongs to no exam, and it can be attached to an exam later.
	ExamID *uint `gorm:"index" json:"exam_id,omitempty"`
	Exam   *Exam `json:"exam,omitempty"`

	Kind  DocumentKind `gorm:"size:24;index;not null" json:"kind"`
	Title string       `gorm:"size:255;not null" json:"title"`

	Filename  string `gorm:"size:255;not null" json:"filename"`
	Extension string `gorm:"size:16;index" json:"extension"`
	MimeType  string `gorm:"size:160" json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	// SHA256 of the raw bytes, used to reject re-uploads of the same file.
	SHA256 string `gorm:"size:64;index" json:"sha256"`

	Year     *int   `gorm:"index" json:"year,omitempty"`
	Language string `gorm:"size:24" json:"language"`

	Status Status `gorm:"size:20;index;default:'pending'" json:"status"`
	// Stage is a human-readable description of where processing has reached.
	Stage string `gorm:"size:80" json:"stage"`
	Notes string `gorm:"type:text" json:"notes"`

	PageCount     int `json:"page_count"`
	QuestionCount int `json:"question_count"`
	BlockCount    int `json:"block_count"`
	// FlaggedCount is how many of this document's questions came out with a
	// problem. Surfacing it on the document is what turns "ingest succeeded"
	// into an honest report of what was actually recovered.
	FlaggedCount int `json:"flagged_count"`

	// Engine requested for this document: auto, geometry or docling. Naming one
	// explicitly is how a user re-runs a document that came out wrong.
	Engine string `gorm:"size:16;default:'auto'" json:"engine"`
	// OCRMode requested for this document: auto, force or off.
	OCRMode   string `gorm:"size:16;default:'auto'" json:"ocr_mode"`
	OCREngine string `gorm:"size:32" json:"ocr_engine"`

	// ExtractionConfidence is the converter's 0..1 assessment of how reliably
	// this document was read. Low confidence does not fail the upload; it sends
	// everything the document produced to review.
	ExtractionConfidence float64 `json:"extraction_confidence"`
	// Warnings are the reasons to distrust part of this document, stored as a
	// JSON array of strings and shown next to it in the UI.
	Warnings datatypes.JSON `json:"warnings,omitempty"`

	Meta datatypes.JSON `json:"meta,omitempty"`

	Conversion *DocumentConversion `json:"conversion,omitempty"`
}

// DocumentBlob holds the raw file bytes in a table of its own so that listing
// or filtering documents never drags megabytes of payload along.
type DocumentBlob struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	DocumentID uint      `gorm:"uniqueIndex;not null" json:"document_id"`
	Data       []byte    `gorm:"type:bytea" json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

// DocumentConversion caches what the Python converter produced for a document:
// the normalised markdown plus everything worth knowing about how it was
// obtained. Re-extracting questions with improved parsing rules reuses this
// instead of paying for OCR again.
type DocumentConversion struct {
	Base
	DocumentID uint      `gorm:"uniqueIndex;not null" json:"document_id"`
	Document   *Document `json:"-"`

	Engine string `gorm:"size:60" json:"engine"`
	// Wide enough for a real version string. The geometric engine reports both
	// the PDF library it read with and the layout model that was available, and
	// truncating that to 40 characters failed every conversion outright.
	EngineVersion string `gorm:"size:200" json:"engine_version"`
	// EngineReason states why that engine was chosen, so a surprising result can
	// be explained without re-running anything.
	EngineReason string `gorm:"size:255" json:"engine_reason"`
	OCREngine    string `gorm:"size:32" json:"ocr_engine"`
	OCRApplied   bool   `json:"ocr_applied"`
	Format       string `gorm:"size:24" json:"format"`

	// Markdown is the converted document text. It is the single input the
	// extraction engine parses.
	Markdown string `gorm:"type:text" json:"markdown,omitempty"`

	PageCount  int     `json:"page_count"`
	CharCount  int     `json:"char_count"`
	TableCount int     `json:"table_count"`
	DurationMS int64   `json:"duration_ms"`
	TextRatio  float64 `json:"text_ratio"` // extractable text before OCR, 0..1

	// Confidence is the converter's 0..1 assessment of this conversion.
	Confidence float64 `json:"confidence"`
	// UnreadableChars counts characters the source fonts never mapped to
	// Unicode. Every one is a hole in the content, so questions containing them
	// are held back rather than shipped with a replacement glyph in the middle.
	UnreadableChars int `json:"unreadable_chars"`
	// Warnings is a JSON array of reasons to distrust part of this conversion.
	Warnings datatypes.JSON `json:"warnings,omitempty"`

	Status Status         `gorm:"size:20;index;default:'pending'" json:"status"`
	Error  string         `gorm:"type:text" json:"error,omitempty"`
	Meta   datatypes.JSON `json:"meta,omitempty"`
}

// ExtractedBlock is one structural element the converter identified: a
// heading, paragraph, list item, table or detected question region. Blocks
// keep page numbers so extracted questions can be traced back to a page.
type ExtractedBlock struct {
	Base
	DocumentID uint      `gorm:"index:idx_block_doc_order;not null" json:"document_id"`
	Document   *Document `json:"-"`

	OrderIndex int `gorm:"index:idx_block_doc_order" json:"order_index"`
	PageNo     int `gorm:"index" json:"page_no"`

	// BlockType is the converter's label, normalised to: heading, paragraph,
	// list, table, caption, formula, code, question, answer_key, explanation.
	BlockType string `gorm:"size:32;index" json:"block_type"`
	Text      string `gorm:"type:text" json:"text"`

	// SubjectID is filled when this block fell under a recognised section.
	SubjectID *uint    `gorm:"index" json:"subject_id,omitempty"`
	Subject   *Subject `json:"-"`

	// SectionLabel is the raw heading text this block sits under.
	SectionLabel string         `gorm:"size:200" json:"section_label"`
	CharCount    int            `json:"char_count"`
	Meta         datatypes.JSON `json:"meta,omitempty"`
}
