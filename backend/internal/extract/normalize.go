// Package extract turns converted document text into structured questions.
//
// Nothing here is tied to a particular exam. The parser discovers how a
// document is laid out by sampling it: which marker style numbers the
// questions, which style labels the options, how many options each question
// carries, where the answer key sits. Subject recognition is driven by alias
// rows in the database rather than a table compiled into the binary, so
// supporting a new exam means adding data, not shipping code.
package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

var (
	whitespaceRe = regexp.MustCompile(`\s+`)
	// Markdown decorations the converter emits around headings and emphasis.
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s*`)
	// A list bullet, but not an emphasis marker: "* " and "- " at line start.
	mdBulletRe = regexp.MustCompile(`^\s{0,3}[-*+]\s+`)
	mdEmphasis = regexp.MustCompile(`(\*\*|__|\*|_|` + "`" + `)`)
	// Page separators the converter inserts between pages.
	pageBreakRe = regexp.MustCompile(`(?i)^\s*(?:<!--\s*page\s*(?:break)?\s*-->|-{3,}|={3,}|\f)\s*$`)
	pageMarkRe  = regexp.MustCompile(`(?i)^\s*(?:\[?page\]?\s*[:#-]?\s*(\d+)\s*(?:of\s*\d+)?|-{0,3}\s*page\s+(\d+)\s*-{0,3})\s*$`)
	// Trailing counts on section headings, e.g. "Reasoning (25 Questions)".
	trailingCountRe = regexp.MustCompile(`(?i)[\(\[]\s*\d+\s*(?:questions?|marks?|qs?)\s*[\)\]]\s*$`)
	// Leading ordinal prefixes on headings, e.g. "Part A -", "Section II:".
	headingPrefixRe = regexp.MustCompile(`(?i)^\s*(?:part|section|paper|subject|unit|module|tier)\s*[-–—:.]?\s*(?:[ivxlcdm]+|[0-9]+|[a-z])?\s*[-–—:.)]*\s*`)
	// Hyphenation across a line break: "compre-\nhension".
	hyphenBreakRe = regexp.MustCompile(`(\p{L})-\s*$`)
)

// collapse trims a string and squeezes internal whitespace to single spaces.
func collapse(s string) string {
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(s, " "))
}

// stripMarkdown removes heading hashes, emphasis markers and list bullets so the
// parser sees the same plain text regardless of how the converter decorated it.
//
// Dropping the bullet matters: converters often emit a wrapped sentence as a
// list item, and leaving the "- " on would hide an option marker behind it or
// let the sentence run on into the previous option's text.
func stripMarkdown(line string) string {
	line = mdHeadingRe.ReplaceAllString(line, "")
	line = mdBulletRe.ReplaceAllString(line, "")
	line = mdEmphasis.ReplaceAllString(line, "")
	line = strings.ReplaceAll(line, "\\_", "_")
	line = strings.ReplaceAll(line, "\\*", "*")
	return strings.TrimSpace(line)
}

// tableHeadingText returns the single distinct value of a markdown table row, or
// "" when the row holds more than one value.
//
// Converters render a centred section banner spanning a multi-column page as a
// one-row table repeating the same text in every cell. Without this, such a
// banner is invisible to heading detection and every question under it is filed
// against the previous section's subject.
func tableHeadingText(raw string) string {
	if !isTableRow(raw) || isTableDivider(raw) {
		return ""
	}
	distinct := ""
	for _, cell := range tableCells(raw) {
		cell = strings.TrimSpace(stripMarkdown(cell))
		if cell == "" {
			continue
		}
		if distinct == "" {
			distinct = cell
			continue
		}
		if !strings.EqualFold(distinct, cell) {
			return ""
		}
	}
	// A banner is short. Anything long is prose that happened to land in a cell.
	if len([]rune(distinct)) > 90 {
		return ""
	}
	return distinct
}

// isTableRow reports whether a line looks like a markdown table row.
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 2
}

// tableCells splits a markdown table row into trimmed cell values.
func tableCells(line string) []string {
	t := strings.Trim(strings.TrimSpace(line), "|")
	parts := strings.Split(t, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// isTableDivider matches the |---|---| row under a markdown table header.
func isTableDivider(line string) bool {
	t := strings.Trim(strings.TrimSpace(line), "| ")
	if t == "" {
		return false
	}
	for _, r := range t {
		if r != '-' && r != ':' && r != ' ' && r != '|' {
			return false
		}
	}
	return true
}

// normalizeHeading reduces a candidate section heading to comparable form:
// lowercase, no markdown, no structural prefix, no trailing question count,
// no punctuation.
func normalizeHeading(line string) string {
	s := stripMarkdown(line)
	s = trailingCountRe.ReplaceAllString(s, "")
	s = headingPrefixRe.ReplaceAllString(s, "")
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '&', r == '-':
			b.WriteRune(' ')
		}
	}
	s = collapse(b.String())
	// "and" and "&" are used interchangeably in headings.
	s = strings.ReplaceAll(s, " and ", " ")
	return collapse(s)
}

// NormalizeLabel exposes heading normalisation so callers can group equivalent
// section headings, e.g. matching "PART A - Reasoning" with "Reasoning".
func NormalizeLabel(s string) string { return normalizeHeading(s) }

// normalizeForHash reduces question text to a comparison key: lowercase,
// letters and digits only. Punctuation, spacing and OCR whitespace noise
// differ between two scans of the same paper, so they are discarded.
func normalizeForHash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ContentHash fingerprints a question by its stem plus its option texts, so the
// same question arriving from a second document is recognised even when the
// options were printed in a different order.
func ContentHash(stem string, options []string) string {
	parts := make([]string, 0, len(options)+1)
	parts = append(parts, normalizeForHash(stem))

	normed := make([]string, 0, len(options))
	for _, o := range options {
		if n := normalizeForHash(o); n != "" {
			normed = append(normed, n)
		}
	}
	// Sort so option order does not change the fingerprint.
	for i := 1; i < len(normed); i++ {
		for j := i; j > 0 && normed[j] < normed[j-1]; j-- {
			normed[j], normed[j-1] = normed[j-1], normed[j]
		}
	}
	parts = append(parts, normed...)

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// looksLikeHeading reports whether a line is plausibly a heading rather than
// body text: short, not a full sentence, and not a question marker.
func looksLikeHeading(raw string) bool {
	line := stripMarkdown(raw)
	if line == "" || len([]rune(line)) > 90 {
		return false
	}
	// Markdown headings and bold-only lines are explicit signals.
	if mdHeadingRe.MatchString(raw) {
		return true
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") {
		return true
	}
	// A line ending in sentence punctuation is prose, not a heading.
	if strings.HasSuffix(line, ".") && !strings.HasSuffix(line, "...") {
		// Allow "Section A." style headings, which are short.
		if len([]rune(line)) > 40 {
			return false
		}
	}
	if strings.ContainsAny(line, "?") {
		return false
	}
	// Mostly-uppercase short lines are headings in almost every paper.
	letters, upper := 0, 0
	for _, r := range line {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letters == 0 {
		return false
	}
	if float64(upper)/float64(letters) > 0.7 {
		return true
	}
	// Otherwise short title-case lines with few words qualify.
	return len(strings.Fields(line)) <= 10
}

// dehyphenate joins words split across a line break.
func dehyphenate(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		for hyphenBreakRe.MatchString(line) && i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if next == "" {
				break
			}
			// Only join when the continuation starts lowercase, which indicates a
			// split word rather than a new sentence.
			if r := []rune(next)[0]; !unicode.IsLower(r) {
				break
			}
			line = hyphenBreakRe.ReplaceAllString(line, "$1") + next
			i++
		}
		out = append(out, line)
	}
	return out
}
