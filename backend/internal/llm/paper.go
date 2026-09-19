package llm

import (
	"context"
	"fmt"
	"strings"

	"mockcreator/internal/models"
)

// Paper-level issue codes reported by the final review.
const (
	CodePaperDuplicateContent = "paper_duplicate_content"
	CodePaperAnswerPattern    = "paper_answer_pattern"
	CodePaperBrokenQuestion   = "paper_broken_question"
	CodePaperSectionMismatch  = "paper_section_mismatch"
	CodePaperInconsistentTone = "paper_inconsistent_style"
	CodePaperOrphanPassage    = "paper_orphan_passage"
	CodePaperRepeatedAnswer   = "paper_repeated_answer_position"
	CodePaperTooEasy          = "paper_difficulty_implausible"
)

// paperAuditSystem is the final gate before a paper goes to a client.
//
// The deterministic checks have already confirmed the paper's arithmetic: the
// counts, the marks, the section balance. What is left is the judgement a person
// makes on reading it end to end - two questions that are really the same, a
// section whose questions do not belong to it, an answer key that is obviously
// patterned. That is what this asks for, and nothing else.
const paperAuditSystem = `You are the final reviewer of a complete mock test paper before it is delivered to an education company.

You have already been told the paper's structure is arithmetically correct.
Your job is to find what a real student, teacher or client would notice on reading it.

Look for:
- two questions that test the same thing, even when worded differently
- a question that does not belong in the section it appears in
- a question that is broken, garbled or unanswerable as printed
- an answer key with an obvious pattern a student could exploit
- a passage that no question refers to, or questions referring to a passage that is absent
- questions whose style or difficulty is wildly out of keeping with the rest

Rules:
1. You NEVER rewrite content. You report, using the sequence numbers you were given.
2. You NEVER invent a defect to seem thorough. An acceptable paper gets an empty list.
3. Every "evidence" string must be copied exactly from the paper you were shown.
4. Judge only what you were shown.

Reply with JSON only:
{
  "verdict": "pass|review|fail",
  "defects": [
    {"code": "<allowed code>", "severity": "critical|major|minor", "questions": [3, 47], "message": "<short and specific>", "evidence": "<exact quote or empty>"}
  ],
  "summary": "<two sentences at most>"
}

Allowed codes:
- paper_duplicate_content: two questions test the same thing
- paper_answer_pattern: the answer key is patterned in a way a student could exploit
- paper_broken_question: a question is garbled or unanswerable
- paper_section_mismatch: a question is in the wrong section
- paper_inconsistent_style: a question is badly out of keeping with the rest
- paper_orphan_passage: a passage or its questions are missing their counterpart
- paper_difficulty_implausible: the stated difficulty does not match the content`

type paperReply struct {
	Verdict string `json:"verdict"`
	Defects []struct {
		Code      string `json:"code"`
		Severity  string `json:"severity"`
		Questions []int  `json:"questions"`
		Message   string `json:"message"`
		Evidence  string `json:"evidence"`
	} `json:"defects"`
	Summary string `json:"summary"`
}

// PaperItem is one question as it appears in the paper being reviewed.
type PaperItem struct {
	SequenceNo  int
	Section     string
	Subject     string
	Difficulty  string
	Stem        string
	Options     []string
	AnswerLabel string
	HasPassage  bool
}

// PaperAudit is the model's verdict on a complete paper.
type PaperAudit struct {
	Issues    models.QualityIssues
	Summary   string
	Grounding Grounding
	Usage     Usage
	// Verdict is the model's own overall call, kept for the report. The gate is
	// still decided by issue severity, so a model saying "pass" cannot clear a
	// critical defect it also reported.
	Verdict string
}

// AuditPaper runs the final review over an assembled paper.
//
// Long papers are summarised rather than truncated: sending the first forty
// questions and silently dropping the rest would produce a review that looks
// complete and is not. Option text is included only where it is short, because
// duplicate detection needs the stems and the answer key, not every distractor.
func (c *Client) AuditPaper(
	ctx context.Context,
	title string,
	items []PaperItem,
	structureNote string,
) (*PaperAudit, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}
	if len(items) == 0 {
		return &PaperAudit{Verdict: "pass"}, nil
	}

	prompt := renderPaperPrompt(title, items, structureNote, c.cfg.MaxInputChars)

	var reply paperReply
	usage, err := c.completeJSON(ctx, paperAuditSystem, prompt, 2000, &reply)
	if err != nil {
		return nil, err
	}

	out := &PaperAudit{
		Summary: trimTo(strings.TrimSpace(reply.Summary), 400),
		Verdict: strings.ToLower(strings.TrimSpace(reply.Verdict)),
		Usage:   usage,
	}

	parts := make([]string, 0, len(items)*2)
	for _, item := range items {
		parts = append(parts, item.Stem)
		parts = append(parts, item.Options...)
	}
	source := NewSource(parts...)

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
			Message: "the final review quoted text that is not in this paper, so it was discarded; " +
				"a person should read the paper before it is delivered",
			Evidence: trimTo(strings.Join(out.Grounding.Ungrounded, " | "), 220),
		}}
		return out, nil
	}

	for _, defect := range reply.Defects {
		code := strings.TrimSpace(strings.ToLower(defect.Code))
		if !allowedPaperCode(code) {
			continue
		}
		field := ""
		if len(defect.Questions) > 0 {
			numbers := make([]string, 0, len(defect.Questions))
			for _, n := range defect.Questions {
				if n >= 1 && n <= len(items) {
					numbers = append(numbers, fmt.Sprintf("%d", n))
				}
			}
			if len(numbers) > 0 {
				field = "q" + strings.Join(numbers, ",")
			}
		}
		out.Issues = append(out.Issues, models.QualityIssue{
			Code:     code,
			Severity: paperSeverity(defect.Severity, code),
			Source:   models.SourceModel,
			Field:    field,
			Message:  trimTo(strings.TrimSpace(defect.Message), 300),
			Evidence: trimTo(strings.TrimSpace(defect.Evidence), 200),
		})
	}

	// A model that declares failure without naming a defect still has to be
	// heard, or its verdict disappears.
	if out.Verdict == "fail" && len(out.Issues) == 0 {
		out.Issues = append(out.Issues, models.QualityIssue{
			Code:     CodePaperBrokenQuestion,
			Severity: models.SeverityCritical,
			Source:   models.SourceModel,
			Message:  "the final review failed this paper without naming a specific defect",
			Evidence: out.Summary,
		})
	}

	return out, nil
}

func renderPaperPrompt(title string, items []PaperItem, structureNote string, budget int) string {
	if budget <= 0 {
		budget = 24000
	}
	// Reserve room for the instructions and the reply.
	budget = budget * 3 / 4

	var b strings.Builder
	fmt.Fprintf(&b, "Paper: %s\n", title)
	if structureNote != "" {
		fmt.Fprintf(&b, "Structure already verified: %s\n", structureNote)
	}
	fmt.Fprintf(&b, "Questions: %d\n\n", len(items))

	// Decide how much of each option set fits. Every question gets its stem and
	// answer; option text is included while there is room, oldest first, so the
	// paper is covered evenly rather than in detail up front and not at all later.
	perQuestion := budget / maxInt(1, len(items))
	for _, item := range items {
		fmt.Fprintf(&b, "%d. [%s / %s / %s]", item.SequenceNo, item.Section, item.Subject, item.Difficulty)
		if item.HasPassage {
			b.WriteString(" [has passage]")
		}
		fmt.Fprintf(&b, " answer=%s\n", item.AnswerLabel)
		fmt.Fprintf(&b, "   %s\n", trimTo(item.Stem, maxInt(120, perQuestion*2/3)))
		if len(item.Options) > 0 && perQuestion > 200 {
			shown := make([]string, 0, len(item.Options))
			for _, opt := range item.Options {
				shown = append(shown, trimTo(opt, 60))
			}
			fmt.Fprintf(&b, "   options: %s\n", strings.Join(shown, " | "))
		}
	}
	return b.String()
}

func allowedPaperCode(code string) bool {
	switch code {
	case CodePaperDuplicateContent, CodePaperAnswerPattern, CodePaperBrokenQuestion,
		CodePaperSectionMismatch, CodePaperInconsistentTone, CodePaperOrphanPassage,
		CodePaperTooEasy:
		return true
	}
	return false
}

// paperSeverity floors severity by code. A duplicate or a broken question is a
// critical defect in a delivered paper regardless of how the model grades it.
func paperSeverity(raw, code string) models.Severity {
	switch code {
	case CodePaperDuplicateContent, CodePaperBrokenQuestion, CodePaperOrphanPassage:
		return models.SeverityCritical
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "critical":
		return models.SeverityCritical
	case "minor":
		return models.SeverityMinor
	}
	return models.SeverityMajor
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
