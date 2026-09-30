package models

import (
	"time"

	"gorm.io/datatypes"
)

// Question is the unit of content in the warehouse.
//
// A question is owned by a Subject, not by an exam. Exam membership is
// expressed through QuestionExamLink rows, which is what allows one question
// to serve several exams at once: the exam it came from gets a direct link,
// and any exam associated with that one gets an inherited link.
type Question struct {
	Base
	SubjectID uint     `gorm:"index;not null" json:"subject_id"`
	Subject   *Subject `json:"subject,omitempty"`
	TopicID   *uint    `gorm:"index" json:"topic_id,omitempty"`
	Topic     *Topic   `json:"topic,omitempty"`

	// OriginExamID is the exam whose material this came from, when known.
	OriginExamID *uint `gorm:"index" json:"origin_exam_id,omitempty"`
	OriginExam   *Exam `gorm:"foreignKey:OriginExamID" json:"origin_exam,omitempty"`

	QuestionText string       `gorm:"type:text;not null" json:"question_text"`
	Type         QuestionType `gorm:"size:24;index;default:'mcq'" json:"type"`
	Difficulty   Difficulty   `gorm:"size:10;index;default:'medium'" json:"difficulty"`

	// DifficultyConfidence records how much to trust Difficulty. Extraction
	// cannot judge hardness, so it writes a low confidence and the value is
	// treated as a placeholder until a reviewer or model sets it properly.
	DifficultyConfidence float64 `json:"difficulty_confidence"`

	Explanation string `gorm:"type:text" json:"explanation"`
	// AnswerText carries the answer for formats that have no option list.
	AnswerText        string `gorm:"type:text" json:"answer_text"`
	EstimatedSolveSec int    `json:"estimated_solve_sec"`

	Origin   Origin `gorm:"size:16;index;default:'extracted'" json:"origin"`
	Year     *int   `gorm:"index" json:"year,omitempty"`
	Language string `gorm:"size:24" json:"language"`

	// PassageID points at shared reading material this question refers to. The
	// passage text is stored once on the Passage row, never copied into
	// QuestionText, so a comprehension set has one passage and six questions
	// rather than six near-identical stems.
	PassageID *uint    `gorm:"index" json:"passage_id,omitempty"`
	Passage   *Passage `json:"passage,omitempty"`

	// Provenance back to the document and block it was read from.
	DocumentID     *uint     `gorm:"index" json:"document_id,omitempty"`
	Document       *Document `json:"-"`
	QuestionNumber int       `json:"question_number"`
	PageNo         int       `json:"page_no"`

	// SourceFirstLine and SourceLastLine locate this question inside the
	// document's converted text. With them a reviewer can be shown exactly what
	// was read, which is the difference between "this looks wrong" and knowing
	// whether the extractor or the source is at fault.
	SourceFirstLine int `json:"source_first_line"`
	SourceLastLine  int `json:"source_last_line"`
	// ExtractEngine records which converter produced the text: the geometric
	// reader or the layout model.
	ExtractEngine string `gorm:"size:24;index" json:"extract_engine"`
	// ExtractionConfidence is the parser's own 0..1 opinion of how cleanly this
	// question came out, independent of whether the content is any good.
	ExtractionConfidence float64 `json:"extraction_confidence"`
	// TrailingText is material that followed a structurally complete question.
	// It is kept for a reviewer instead of being appended to the last option,
	// which is where it used to end up.
	TrailingText string `gorm:"type:text" json:"trailing_text,omitempty"`

	// QualityGate is the verdict that decides whether this question may appear
	// in a paper.
	QualityGate

	// ContentHash is a normalised fingerprint of the stem plus options, used to
	// detect the same question arriving from a second document.
	ContentHash string `gorm:"size:64;index" json:"content_hash"`
	// DuplicateOfID points at the first copy when a duplicate is kept for
	// traceability rather than discarded.
	DuplicateOfID *uint `gorm:"index" json:"duplicate_of_id,omitempty"`

	// HasAnswer is false when extraction could not determine a correct option.
	// These questions are held back from generated papers.
	HasAnswer bool `gorm:"index;default:false" json:"has_answer"`

	Status Status         `gorm:"size:16;index;default:'pending'" json:"status"`
	Tags   datatypes.JSON `json:"tags,omitempty"`
	Meta   datatypes.JSON `json:"meta,omitempty"`

	Options   []QuestionOption   `json:"options,omitempty"`
	Reviews   []QualityReview    `json:"reviews,omitempty"`
	ExamLinks []QuestionExamLink `json:"exam_links,omitempty"`
}

// QuestionOption is a single answer choice.
type QuestionOption struct {
	Base
	QuestionID uint `gorm:"index;not null" json:"question_id"`

	// Label is the choice marker as printed in the source ("A", "b", "3",
	// "iv"), preserved so a rebuilt paper looks like the original.
	Label      string `gorm:"size:8" json:"label"`
	Text       string `gorm:"type:text;not null" json:"text"`
	OrderIndex int    `json:"order_index"`

	IsCorrect bool `gorm:"index;default:false" json:"is_correct"`
	// IsDistractor marks a deliberately plausible wrong answer.
	IsDistractor bool `gorm:"default:false" json:"is_distractor"`
}

// QuestionExamLink makes a question available to an exam.
//
// Direct links are created when a question is extracted from that exam's own
// material. Associated links are derived from ExamAssociation rows and are
// rebuilt whenever associations change, so they always reflect current
// configuration. The Warehouse view reads these rows to tag each question with
// every exam it serves.
//
// This table deliberately does not soft-delete. Links are derived data that is
// rebuilt wholesale, and a soft-deleted row would collide with the unique index
// the next time the same pair was created.
type QuestionExamLink struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	QuestionID uint      `gorm:"index:idx_qexam,unique;not null" json:"question_id"`
	Question   *Question `json:"-"`

	ExamID uint  `gorm:"index:idx_qexam,unique;not null" json:"exam_id"`
	Exam   *Exam `json:"exam,omitempty"`

	LinkType LinkType `gorm:"size:16;index;default:'direct'" json:"link_type"`

	// AssociationID records which association produced an inherited link.
	AssociationID *uint            `gorm:"index" json:"association_id,omitempty"`
	Association   *ExamAssociation `json:"-"`

	// Relevance is carried over from the association's similarity and lets the
	// generator prefer closer matches.
	Relevance float64 `gorm:"default:1" json:"relevance"`
}

// QualityReview is an audit of a question, by a person or by a model.
//
// Model audits are stored here as ordinary rows rather than folded silently into
// the question. A reviewer can see what the model said, which check produced it
// and whether a human later disagreed.
type QualityReview struct {
	Base
	QuestionID uint      `gorm:"index;not null" json:"question_id"`
	Question   *Question `json:"-"`

	// ReviewerType is "human" or "model".
	ReviewerType string `gorm:"size:16;index;default:'human'" json:"reviewer_type"`
	ReviewerName string `gorm:"size:120" json:"reviewer_name"`

	// Issues is what this review found, in the same shape the quality engine
	// uses, so a model's findings and the deterministic ones merge cleanly.
	Issues datatypes.JSON `json:"issues,omitempty"`
	// Grounded records that every quoted fragment the model returned was found
	// in the source text. A model reply that failed this check is kept for
	// inspection but never acted on.
	Grounded bool `json:"grounded"`
	// PromptTokens and CompletionTokens make model cost visible.
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`

	RelevanceScore   int `json:"relevance_score"`   // fit to the subject, /10
	DifficultyScore  int `json:"difficulty_score"`  // difficulty labelled correctly, /10
	OriginalityScore int `json:"originality_score"` // not a near-duplicate, /10
	ClarityScore     int `json:"clarity_score"`     // unambiguous wording, /10

	IsAmbiguous bool `json:"is_ambiguous"`
	IsFactual   bool `json:"is_factual"`
	IsDuplicate bool `json:"is_duplicate"`

	// SetDifficulty lets a reviewer correct the question's difficulty in the
	// same action as approving it.
	SetDifficulty Difficulty `gorm:"size:10" json:"set_difficulty,omitempty"`

	Verdict Status `gorm:"size:16;index" json:"verdict"`
	Notes   string `gorm:"type:text" json:"notes"`
}

// FoundIssues decodes what this review recorded.
func (r QualityReview) FoundIssues() QualityIssues { return DecodeQualityIssues(r.Issues) }

// Passed reports whether the review clears the acceptance bar. Scores left at
// zero are treated as "not assessed" and skipped rather than failing the
// question, so a reviewer can approve on clarity alone.
func (r QualityReview) Passed(threshold int) bool {
	if r.IsAmbiguous || r.IsDuplicate {
		return false
	}
	// A model review that could not be grounded in the source proves nothing.
	if r.ReviewerType == "model" && !r.Grounded {
		return false
	}
	scores := []int{r.RelevanceScore, r.DifficultyScore, r.OriginalityScore, r.ClarityScore}
	assessed := 0
	for _, s := range scores {
		if s <= 0 {
			continue
		}
		assessed++
		if s < threshold {
			return false
		}
	}
	// With nothing scored, fall back to the boolean flags alone.
	return assessed > 0 || r.IsFactual
}
