package models

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
)

// QualityStatus is the gate a question or a paper has to clear before it can be
// delivered to anyone.
//
// The distinction that matters is between "nobody has looked" and "this passed".
// Unchecked is not a pass: content that has never been validated is treated as
// unusable, which is what stops a broken question reaching a paper simply
// because the checks had not run yet.
type QualityStatus string

const (
	// QualityUnchecked means validation has not run. Never treated as a pass.
	QualityUnchecked QualityStatus = "unchecked"
	// QualityPass means every check cleared and the content is deliverable.
	QualityPass QualityStatus = "pass"
	// QualityNeedsReview means something is uncertain and a person should look.
	QualityNeedsReview QualityStatus = "review"
	// QualityFailed means a critical defect was found. Blocks publication.
	QualityFailed QualityStatus = "failed"
)

// Deliverable reports whether content in this state may go into a paper that is
// handed to a student.
func (q QualityStatus) Deliverable() bool { return q == QualityPass }

// Valid reports whether q is a known status.
func (q QualityStatus) Valid() bool {
	switch q {
	case QualityUnchecked, QualityPass, QualityNeedsReview, QualityFailed:
		return true
	}
	return false
}

// AllQualityStatuses lists every status, worst first, which is the order a
// review queue should present them in.
func AllQualityStatuses() []QualityStatus {
	return []QualityStatus{QualityFailed, QualityNeedsReview, QualityUnchecked, QualityPass}
}

// Severity grades how badly a defect matters.
type Severity string

const (
	// SeverityCritical is a defect that makes the content wrong or unusable:
	// a missing answer, a corrupted stem, a duplicate. Blocks publication.
	SeverityCritical Severity = "critical"
	// SeverityMajor is a defect that probably makes it unusable but needs a
	// judgement call. Sends the content to review.
	SeverityMajor Severity = "major"
	// SeverityMinor is worth recording but does not hold anything back.
	SeverityMinor Severity = "minor"
)

// Rank orders severities so the worst sorts first.
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityMajor:
		return 1
	default:
		return 2
	}
}

// IssueSource records who or what found a defect, so a model's opinion is never
// mistaken for a measured fact or for a human decision.
type IssueSource string

const (
	// SourceRules is a deterministic check in the quality engine.
	SourceRules IssueSource = "rules"
	// SourceModel is a language model's judgement.
	SourceModel IssueSource = "model"
	// SourceHuman is a reviewer's decision.
	SourceHuman IssueSource = "human"
	// SourceExtraction is something the parser recorded while reading the
	// document.
	SourceExtraction IssueSource = "extraction"
)

// QualityIssue is one defect found in a question, a passage or a paper.
type QualityIssue struct {
	Code     string      `json:"code"`
	Severity Severity    `json:"severity"`
	Source   IssueSource `json:"source"`
	// Field localises the problem: "stem", "option:3", "explanation", "answer",
	// "section:Reasoning", or empty when it is about the whole item.
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	// Evidence is the offending text, quoted so a reviewer does not have to hunt
	// for it. Kept short.
	Evidence string `json:"evidence,omitempty"`
}

// QualityIssues is a list of defects, stored as JSON.
type QualityIssues []QualityIssue

// Worst returns the most severe status implied by the issues.
func (list QualityIssues) Worst() QualityStatus {
	status := QualityPass
	for _, issue := range list {
		switch issue.Severity {
		case SeverityCritical:
			return QualityFailed
		case SeverityMajor:
			status = QualityNeedsReview
		}
	}
	return status
}

// Counts tallies issues by severity, which is what a dashboard shows.
func (list QualityIssues) Counts() map[Severity]int {
	out := map[Severity]int{}
	for _, issue := range list {
		out[issue.Severity]++
	}
	return out
}

// Codes lists the distinct issue codes present, preserving first-seen order.
func (list QualityIssues) Codes() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, issue := range list {
		if seen[issue.Code] {
			continue
		}
		seen[issue.Code] = true
		out = append(out, issue.Code)
	}
	return out
}

// JSON marshals the list for storage. A nil list stores as an empty array
// rather than SQL NULL, so a checked-and-clean item is distinguishable from one
// that was never checked.
func (list QualityIssues) JSON() datatypes.JSON {
	if list == nil {
		list = QualityIssues{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return datatypes.JSON([]byte("[]"))
	}
	return datatypes.JSON(raw)
}

// DecodeQualityIssues reads a stored issue list.
func DecodeQualityIssues(raw datatypes.JSON) QualityIssues {
	if len(raw) == 0 {
		return nil
	}
	var out QualityIssues
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// Passage is shared reading material: a comprehension passage, a set of
// directions, or a data set that several questions refer to.
//
// It exists as its own row because the alternative is to copy the text into
// every question that cites it. That is what the engine used to do, and a
// 2,000-character passage repeated across six stems is impossible to review,
// impossible to correct once, and makes every one of those questions look
// different to a duplicate check.
type Passage struct {
	Base

	// ContentHash fingerprints the text so the same passage arriving from a
	// second document is reused rather than copied.
	ContentHash string `gorm:"size:64;index;not null" json:"content_hash"`
	Text        string `gorm:"type:text;not null" json:"text"`
	// Kind is "passage", "directions" or "data_set".
	Kind string `gorm:"size:24;index;default:'passage'" json:"kind"`

	SubjectID *uint    `gorm:"index" json:"subject_id,omitempty"`
	Subject   *Subject `json:"subject,omitempty"`

	DocumentID *uint     `gorm:"index" json:"document_id,omitempty"`
	Document   *Document `json:"-"`

	// FromQuestion and ToQuestion are the printed range this material covers.
	FromQuestion int `json:"from_question"`
	ToQuestion   int `json:"to_question"`
	// RangeExplicit records that the document printed the range itself. An
	// inferred range is a guess and is treated with less confidence.
	RangeExplicit bool `json:"range_explicit"`

	WordCount int `json:"word_count"`

	QualityStatus QualityStatus  `gorm:"size:16;index;default:'unchecked'" json:"quality_status"`
	QualityIssues datatypes.JSON `json:"quality_issues,omitempty"`

	Questions []Question `json:"-"`
}

// Issues decodes the stored issue list.
func (p Passage) Issues() QualityIssues { return DecodeQualityIssues(p.QualityIssues) }

// QualityGate is the reusable verdict block embedded in questions and papers.
//
// Keeping status and score as columns rather than burying them in JSON is what
// lets the generator select only deliverable questions in SQL instead of loading
// the warehouse and filtering in memory.
type QualityGate struct {
	QualityStatus QualityStatus `gorm:"size:16;index;default:'unchecked'" json:"quality_status"`
	// QualityScore is 0..1 and is for ranking a review queue, not for gating.
	// Gating is decided by issue severity, which cannot be averaged away.
	QualityScore  float64        `json:"quality_score"`
	QualityIssues datatypes.JSON `json:"quality_issues,omitempty"`
	CheckedAt     *time.Time     `json:"quality_checked_at,omitempty"`
	// RulesVersion records which generation of the deterministic checks ran, so
	// content validated by older rules can be found and re-checked.
	RulesVersion int `json:"quality_rules_version"`
	// ModelChecked records whether a language model also reviewed this. False
	// means the model-only checks simply did not run; it never means they passed.
	ModelChecked bool   `json:"model_checked"`
	ModelName    string `gorm:"size:120" json:"model_name,omitempty"`
}

// Issues decodes the stored issue list.
func (g QualityGate) Issues() QualityIssues { return DecodeQualityIssues(g.QualityIssues) }

// Apply records a verdict on the gate.
func (g *QualityGate) Apply(issues QualityIssues, score float64, rulesVersion int) {
	now := time.Now()
	g.QualityStatus = issues.Worst()
	g.QualityScore = score
	g.QualityIssues = issues.JSON()
	g.CheckedAt = &now
	g.RulesVersion = rulesVersion
}

// Blocking returns the critical issues, which are the ones that stop delivery.
func (g QualityGate) Blocking() QualityIssues {
	var out QualityIssues
	for _, issue := range g.Issues() {
		if issue.Severity == SeverityCritical {
			out = append(out, issue)
		}
	}
	return out
}
