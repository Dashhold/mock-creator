package models

import "gorm.io/datatypes"

// TestPaper is an assembled paper: an ordered, section-balanced selection of
// questions drawn from the warehouse according to an exam's pattern.
type TestPaper struct {
	Base
	ExamID uint  `gorm:"index;not null" json:"exam_id"`
	Exam   *Exam `json:"exam,omitempty"`

	// PatternID is the pattern version used to build it, kept so a paper can be
	// explained and rebuilt even after the exam's pattern moves on.
	PatternID *uint        `gorm:"index" json:"pattern_id,omitempty"`
	Pattern   *ExamPattern `json:"pattern,omitempty"`

	Title string    `gorm:"size:255;not null" json:"title"`
	Type  PaperType `gorm:"size:24;index;default:'balanced'" json:"type"`

	DurationMin    int     `json:"duration_min"`
	TotalQuestions int     `json:"total_questions"`
	TotalMarks     float64 `json:"total_marks"`
	NegativeMarks  float64 `json:"negative_marks"`

	Status Status `gorm:"size:16;index;default:'draft'" json:"status"`

	// Seed makes generation reproducible: the same seed, pattern and warehouse
	// state produce the same paper.
	Seed int64 `json:"seed"`

	// QualityGate is the paper's own QA verdict, computed over the assembled
	// paper rather than inherited from its questions. A paper can be built
	// entirely from sound questions and still be wrong: duplicated content,
	// a broken section balance, marks that do not add up.
	//
	// Publication and export are refused while this reads failed.
	QualityGate

	// Analytics is a snapshot taken at build time: difficulty spread, subject
	// coverage, how much came from associated exams, and any shortfalls.
	Analytics datatypes.JSON `json:"analytics,omitempty"`
	Notes     string         `gorm:"type:text" json:"notes"`

	Items []TestPaperItem `json:"items,omitempty"`
}

// Publishable reports whether this paper may be marked published or exported for
// delivery. Anything with a critical defect is refused, and a paper that has
// never been checked is refused too, because unchecked is not the same as clean.
func (p TestPaper) Publishable() bool {
	return p.QualityStatus == QualityPass
}

// QAExplanation states, in one line, why a paper is or is not deliverable.
func (p TestPaper) QAExplanation() string {
	switch p.QualityStatus {
	case QualityPass:
		return "passed every quality check"
	case QualityNeedsReview:
		return "needs a reviewer to resolve open questions before delivery"
	case QualityFailed:
		return "blocked: the paper has defects that would reach students"
	default:
		return "not checked yet"
	}
}

// TestPaperItem places one question in a paper with its order, section and
// marks. Section is denormalised text rather than a foreign key so the paper
// keeps the wording it was generated with.
type TestPaperItem struct {
	Base
	TestPaperID uint       `gorm:"index;not null" json:"test_paper_id"`
	TestPaper   *TestPaper `json:"-"`

	QuestionID uint      `gorm:"index;not null" json:"question_id"`
	Question   *Question `json:"question,omitempty"`

	SequenceNo  int      `gorm:"index" json:"sequence_no"`
	SectionName string   `gorm:"size:200" json:"section_name"`
	SubjectID   *uint    `gorm:"index" json:"subject_id,omitempty"`
	Subject     *Subject `json:"subject,omitempty"`

	Marks         float64 `gorm:"default:1" json:"marks"`
	NegativeMarks float64 `json:"negative_marks"`

	// SourceKind records whether this question came from the exam itself or
	// from an associated exam, which the analytics summarise.
	SourceKind LinkType `gorm:"size:16;default:'direct'" json:"source_kind"`
}

// PaperAnalytics is the shape stored in TestPaper.Analytics.
type PaperAnalytics struct {
	DifficultySpread map[string]int     `json:"difficulty_spread"`
	SubjectSpread    map[string]int     `json:"subject_spread"`
	SectionFill      []SectionFill      `json:"section_fill"`
	BorrowedCount    int                `json:"borrowed_count"`
	BorrowedFrom     map[string]int     `json:"borrowed_from,omitempty"`
	YearSpread       map[string]int     `json:"year_spread,omitempty"`
	Shortfalls       []SectionShortfall `json:"shortfalls,omitempty"`
	EstimatedMinutes int                `json:"estimated_minutes"`
}

// SectionFill reports how completely one section could be filled.
type SectionFill struct {
	Section   string `json:"section"`
	Requested int    `json:"requested"`
	Filled    int    `json:"filled"`
	Borrowed  int    `json:"borrowed"`
}

// SectionShortfall explains why a section came up short, so the UI can tell the
// user what to upload next instead of silently producing a thin paper.
type SectionShortfall struct {
	Section   string `json:"section"`
	Requested int    `json:"requested"`
	Available int    `json:"available"`
	Reason    string `json:"reason"`
}
