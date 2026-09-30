package llm

import (
	"context"
	"fmt"
	"strings"

	"mockcreator/internal/models"
)

// repairSystem asks the model to re-split a region of source text into a stem
// and its options.
//
// This is the one task where the model returns content, so it is also the one
// with the hardest constraint: every character it returns must already exist in
// the window it was given. It is not rewriting the question, it is deciding where
// the boundaries fall in text that is already correct - which is exactly the
// judgement a layout-blind parser cannot make and a reader finds obvious.
const repairSystem = `You are repairing the boundaries of one exam question that a parser split incorrectly.

You are given the exact lines of source text for one question, in reading order.
Your job is to say which part is the question stem and which parts are the options.

Absolute rules:
1. You may only COPY text from the source. Never write a word that is not there.
2. Never complete, correct, translate or tidy the text. Copy it verbatim, including odd spacing.
3. Never invent an option that is not in the source. If the source has three options, return three.
4. Never guess the answer. Do not mark anything correct.
5. Remove only material that clearly belongs to something else: a running page header,
   a section title, or the start of the next question. Put that in "excluded".
6. If you cannot tell where the boundaries are, set "confident" to false and return nothing else.

Reply with JSON only:
{
  "confident": true,
  "stem": "<verbatim stem text>",
  "options": ["<verbatim option 1>", "<verbatim option 2>"],
  "excluded": ["<verbatim text that belongs elsewhere>"],
  "reason": "<one sentence on what was wrong>"
}`

type repairReply struct {
	Confident bool     `json:"confident"`
	Stem      string   `json:"stem"`
	Options   []string `json:"options"`
	Excluded  []string `json:"excluded"`
	Reason    string   `json:"reason"`
}

// Repair is a proposed correction to a question's boundaries.
//
// Applied is false when the model declined, or when what it returned could not be
// found in the source. In both cases the question is left exactly as it was and
// sent to a human; a failed repair never degrades into a guess.
type Repair struct {
	Applied  bool
	Stem     string
	Options  []string
	Excluded []string
	Reason   string
	// Issue is what to record about this attempt, whether it worked or not.
	Issue *models.QualityIssue
	Usage Usage
}

// RepairBoundaries asks the model to re-split a question from its source lines.
//
// window is the verbatim source text the question was read from. Everything the
// model returns is checked against it, so a reply that adds, completes or
// paraphrases anything is refused.
func (c *Client) RepairBoundaries(ctx context.Context, window string, expectedOptions int) (*Repair, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(window) == "" {
		return &Repair{}, nil
	}

	prompt := fmt.Sprintf(
		"This paper prints %d options per question.\n\nSource lines for one question:\n---\n%s\n---\n",
		expectedOptions, strings.TrimSpace(window))

	var reply repairReply
	usage, err := c.completeJSON(ctx, repairSystem, prompt, 1600, &reply)
	if err != nil {
		return nil, err
	}

	out := &Repair{Reason: trimTo(strings.TrimSpace(reply.Reason), 240), Usage: usage}

	if !reply.Confident || strings.TrimSpace(reply.Stem) == "" {
		out.Issue = &models.QualityIssue{
			Code:     CodeIncompleteContent,
			Severity: models.SeverityMajor,
			Source:   models.SourceModel,
			Message:  "the model could not determine this question's boundaries; a person should split it",
			Evidence: out.Reason,
		}
		return out, nil
	}

	// This is the guarantee. Every fragment has to be in the window.
	source := NewSource(window)
	fragments := append([]string{reply.Stem}, reply.Options...)
	grounding := source.Check(fragments...)
	if !grounding.Verified {
		out.Issue = &models.QualityIssue{
			Code:     CodeUngroundedReply,
			Severity: models.SeverityMajor,
			Source:   models.SourceModel,
			Message: "the model's repair introduced text that is not in the source, so it was refused; " +
				"a person should split this question",
			Evidence: trimTo(strings.Join(grounding.Ungrounded, " | "), 220),
		}
		return out, nil
	}

	out.Applied = true
	out.Stem = strings.TrimSpace(reply.Stem)
	for _, opt := range reply.Options {
		if trimmed := strings.TrimSpace(opt); trimmed != "" {
			out.Options = append(out.Options, trimmed)
		}
	}
	for _, ex := range reply.Excluded {
		if trimmed := strings.TrimSpace(ex); trimmed != "" {
			out.Excluded = append(out.Excluded, trimTo(trimmed, 240))
		}
	}
	return out, nil
}

// duplicateSystem asks the model to decide whether two questions are the same.
const duplicateSystem = `You compare two exam questions and decide whether a student would consider them the same question.

Two questions are the SAME when they test the same fact or skill with the same answer,
even if the wording, numbering or option order differs.
Two questions are DIFFERENT when the values, the entities or the required reasoning differ,
even when the wording is nearly identical.

Reply with JSON only:
{"same": true, "confidence": 0.0-1.0, "reason": "<one sentence>"}`

type duplicateReply struct {
	Same       bool    `json:"same"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// DuplicateVerdict is the model's judgement on a candidate duplicate pair.
type DuplicateVerdict struct {
	Same       bool
	Confidence float64
	Reason     string
	Usage      Usage
}

// JudgeDuplicate decides whether two similar questions are really the same.
//
// This is the check trigram similarity cannot make. Two arithmetic questions can
// be 95% identical in wording and differ only in the number that matters, and
// suppressing one of them would be wrong; two questions can be worded completely
// differently and be the same question.
func (c *Client) JudgeDuplicate(ctx context.Context, a, b string) (*DuplicateVerdict, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}
	prompt := fmt.Sprintf("Question A:\n%s\n\nQuestion B:\n%s\n", strings.TrimSpace(a), strings.TrimSpace(b))

	var reply duplicateReply
	usage, err := c.completeJSON(ctx, duplicateSystem, prompt, 300, &reply)
	if err != nil {
		return nil, err
	}
	confidence := reply.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return &DuplicateVerdict{
		Same:       reply.Same,
		Confidence: confidence,
		Reason:     trimTo(strings.TrimSpace(reply.Reason), 240),
		Usage:      usage,
	}, nil
}
