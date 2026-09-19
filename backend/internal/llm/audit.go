package llm

import (
	"context"
	"fmt"
	"strings"

	"mockcreator/internal/models"
)

// Question-audit issue codes. These sit alongside the deterministic codes in the
// same list, distinguished by their source being "model".
const (
	CodeNotAnswerable      = "not_answerable"
	CodeAmbiguous          = "ambiguous"
	CodeMultipleCorrect    = "multiple_plausible_answers"
	CodeAnswerWrong        = "answer_looks_wrong"
	CodeExplanationWrong   = "explanation_contradicts_answer"
	CodeContaminated       = "semantic_contamination"
	CodeIncompleteContent  = "incomplete_content"
	CodeOptionsNotParallel = "options_not_parallel"
	CodeSubjectMismatch    = "subject_mismatch"
	CodePassageUnsupported = "passage_does_not_support"
	CodeLanguageBroken     = "language_broken"
	CodeUngroundedReply    = "model_reply_ungrounded"
)

// questionAuditSystem is the instruction the model runs under.
//
// The constraints are stated plainly and then enforced in code. The most
// important is the last one: the model is told that it cannot see the original
// page, so absence of information is never evidence of a defect. Without that,
// models reliably "correct" perfectly good questions by inventing the context
// they think is missing.
const questionAuditSystem = `You are a senior assessment reviewer for an education company.
You inspect one exam question that was machine-read from a printed paper, and you report defects.

You must obey these rules:
1. You NEVER write replacement content. You do not rewrite stems, options, answers or explanations.
2. You NEVER guess what missing text said. If something is missing, you report it as missing.
3. Every string you put in an "evidence" field must be copied exactly from the input you were given.
4. You judge only what is in front of you. You cannot see the original page, so do not speculate about it.
5. If the question is sound, say so. Do not invent defects to seem thorough.

Report a defect only when you are confident a real student or teacher would be affected.

Reply with JSON only, in exactly this shape:
{
  "answerable": true,
  "defects": [
    {"code": "<one of the allowed codes>", "severity": "critical|major|minor", "field": "stem|option:N|answer|explanation|passage|subject", "message": "<short, specific>", "evidence": "<exact quote from the input, or empty>"}
  ],
  "clarity": 1-10,
  "subject_fits": true,
  "notes": "<one sentence, optional>"
}

Allowed codes:
- not_answerable: the question cannot be answered as printed
- ambiguous: more than one reading of the question is defensible
- multiple_plausible_answers: two or more options are defensibly correct
- answer_looks_wrong: the marked answer contradicts the question
- explanation_contradicts_answer: the explanation argues for a different option
- semantic_contamination: text from a different question, a heading, or page furniture is mixed in
- incomplete_content: the question refers to material that is not present
- options_not_parallel: the options are not of the same kind, making one obviously the answer
- subject_mismatch: the question does not belong to the subject it is filed under
- passage_does_not_support: the passage cannot answer this question
- language_broken: the wording is garbled enough to impede understanding`

// auditReply is the model's response shape.
type auditReply struct {
	Answerable bool `json:"answerable"`
	Defects    []struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Field    string `json:"field"`
		Message  string `json:"message"`
		Evidence string `json:"evidence"`
	} `json:"defects"`
	Clarity     int    `json:"clarity"`
	SubjectFits bool   `json:"subject_fits"`
	Notes       string `json:"notes"`
}

// QuestionAudit is what the model concluded about one question.
type QuestionAudit struct {
	Issues models.QualityIssues
	// Clarity is the model's 1-10 readability judgement, recorded on the review
	// row for ranking rather than for gating.
	Clarity int
	Notes   string
	// Grounding records whether every quoted fragment came from the input. When
	// this fails the issues are discarded and replaced with a single issue saying
	// the model could not be trusted on this question.
	Grounding Grounding
	Usage     Usage
}

// AuditQuestionInput is one question presented for review.
type AuditQuestionInput struct {
	Subject     string
	Type        models.QuestionType
	Stem        string
	Options     []AuditOption
	AnswerLabel string
	AnswerText  string
	Explanation string
	Passage     string
}

// AuditOption is one choice shown to the model.
type AuditOption struct {
	Label     string
	Text      string
	IsCorrect bool
}

// AuditQuestion asks the model to review one question.
//
// The reply is accepted only if every quoted fragment is present in the question
// that was sent. A model that quotes text which is not there has stopped
// reporting and started writing, and its findings for that question are dropped
// in favour of a single issue telling a human to look.
func (c *Client) AuditQuestion(ctx context.Context, in AuditQuestionInput) (*QuestionAudit, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}

	prompt := renderQuestionPrompt(in)

	var reply auditReply
	usage, err := c.completeJSON(ctx, questionAuditSystem, prompt, 1200, &reply)
	if err != nil {
		return nil, err
	}

	out := &QuestionAudit{
		Clarity: reply.Clarity,
		Notes:   strings.TrimSpace(reply.Notes),
		Usage:   usage,
	}

	// Ground every quoted fragment against exactly what the model was shown.
	source := NewSource(append([]string{in.Stem, in.Explanation, in.Passage},
		optionTexts(in.Options)...)...)
	quotes := make([]string, 0, len(reply.Defects))
	for _, defect := range reply.Defects {
		if strings.TrimSpace(defect.Evidence) != "" {
			quotes = append(quotes, defect.Evidence)
		}
	}
	out.Grounding = source.Check(quotes...)

	if !out.Grounding.Verified {
		out.Issues = models.QualityIssues{{
			Code:     CodeUngroundedReply,
			Severity: models.SeverityMajor,
			Source:   models.SourceModel,
			Message: "the model quoted text that is not in this question, so its review was discarded; " +
				"a person should check this question",
			Evidence: trimTo(strings.Join(out.Grounding.Ungrounded, " | "), 200),
		}}
		return out, nil
	}

	for _, defect := range reply.Defects {
		code := strings.TrimSpace(strings.ToLower(defect.Code))
		if !allowedAuditCode(code) {
			// An unknown code means the model improvised the schema. Keep the
			// observation but do not let it act as a recognised gate.
			continue
		}
		out.Issues = append(out.Issues, models.QualityIssue{
			Code:     code,
			Severity: severityFrom(defect.Severity, code),
			Source:   models.SourceModel,
			Field:    sanitiseField(defect.Field, len(in.Options)),
			Message:  trimTo(strings.TrimSpace(defect.Message), 300),
			Evidence: trimTo(strings.TrimSpace(defect.Evidence), 200),
		})
	}

	// A model that says the question is not answerable but lists no defect is
	// still telling us something; record it rather than losing the signal.
	if !reply.Answerable && !hasCode(out.Issues, CodeNotAnswerable) {
		out.Issues = append(out.Issues, models.QualityIssue{
			Code:     CodeNotAnswerable,
			Severity: models.SeverityCritical,
			Source:   models.SourceModel,
			Message:  "the model judged this question unanswerable as printed",
		})
	}
	if !reply.SubjectFits && in.Subject != "" && !hasCode(out.Issues, CodeSubjectMismatch) {
		out.Issues = append(out.Issues, models.QualityIssue{
			Code:     CodeSubjectMismatch,
			Severity: models.SeverityMajor,
			Source:   models.SourceModel,
			Field:    "subject",
			Message:  fmt.Sprintf("the model does not think this question belongs to %s", in.Subject),
		})
	}

	return out, nil
}

func renderQuestionPrompt(in AuditQuestionInput) string {
	var b strings.Builder
	b.WriteString("Review this question.\n\n")
	if in.Subject != "" {
		fmt.Fprintf(&b, "Filed under subject: %s\n", in.Subject)
	}
	if in.Type != "" {
		fmt.Fprintf(&b, "Declared type: %s\n", in.Type)
	}
	if strings.TrimSpace(in.Passage) != "" {
		fmt.Fprintf(&b, "\nShared passage:\n%s\n", strings.TrimSpace(in.Passage))
	}
	fmt.Fprintf(&b, "\nStem:\n%s\n\nOptions:\n", strings.TrimSpace(in.Stem))
	for i, opt := range in.Options {
		marker := " "
		if opt.IsCorrect {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %d. [%s] %s\n", marker, i+1, opt.Label, strings.TrimSpace(opt.Text))
	}
	b.WriteString("\n(An asterisk marks the option recorded as correct.)\n")
	if in.AnswerText != "" {
		fmt.Fprintf(&b, "\nRecorded answer text: %s\n", strings.TrimSpace(in.AnswerText))
	}
	if strings.TrimSpace(in.Explanation) != "" {
		fmt.Fprintf(&b, "\nExplanation:\n%s\n", strings.TrimSpace(in.Explanation))
	}
	return b.String()
}

func optionTexts(options []AuditOption) []string {
	out := make([]string, 0, len(options))
	for _, o := range options {
		out = append(out, o.Text)
	}
	return out
}

// allowedAuditCode keeps the model inside the agreed vocabulary. A model is free
// to be wrong about a question; it is not free to invent a new class of defect
// that nothing downstream knows how to act on.
func allowedAuditCode(code string) bool {
	switch code {
	case CodeNotAnswerable, CodeAmbiguous, CodeMultipleCorrect, CodeAnswerWrong,
		CodeExplanationWrong, CodeContaminated, CodeIncompleteContent,
		CodeOptionsNotParallel, CodeSubjectMismatch, CodePassageUnsupported,
		CodeLanguageBroken:
		return true
	}
	return false
}

// severityFrom reads the model's severity but floors it by code, so a model
// cannot downgrade a defect that is critical by definition.
func severityFrom(raw, code string) models.Severity {
	declared := models.SeverityMajor
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "critical":
		declared = models.SeverityCritical
	case "minor":
		declared = models.SeverityMinor
	}
	switch code {
	case CodeNotAnswerable, CodeAnswerWrong, CodeExplanationWrong, CodeContaminated,
		CodeMultipleCorrect, CodeIncompleteContent:
		// These make the question wrong for a student, whatever the model thinks.
		return models.SeverityCritical
	}
	return declared
}

// sanitiseField keeps field references inside the shape the UI can resolve.
func sanitiseField(field string, optionCount int) string {
	field = strings.TrimSpace(strings.ToLower(field))
	switch field {
	case "stem", "answer", "explanation", "passage", "subject", "":
		return field
	}
	if strings.HasPrefix(field, "option:") {
		var n int
		if _, err := fmt.Sscanf(field, "option:%d", &n); err == nil && n >= 1 && n <= optionCount {
			return field
		}
	}
	return ""
}

func hasCode(list models.QualityIssues, code string) bool {
	for _, issue := range list {
		if issue.Code == code {
			return true
		}
	}
	return false
}
