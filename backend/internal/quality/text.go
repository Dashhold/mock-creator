// Package quality decides whether content is fit to deliver.
//
// Two things are separated on purpose. The extractor reports how cleanly it
// could read a document; this package reports whether what was read is a usable
// question. A perfectly extracted question can still be unusable because its
// answer is missing, and a question with a low extraction score can be fine.
//
// Everything here is deterministic and exam-agnostic. No check knows the name of
// an examination, a subject or a board: they test structural and textual
// properties that hold for any multiple-choice material in any language. The
// judgement calls that genuinely need understanding are left to the optional
// model layer, which reports its findings in the same vocabulary.
package quality

import (
	"regexp"
	"strings"
	"unicode"
)

// RulesVersion identifies this generation of the deterministic checks. It is
// stored with every verdict so content validated by older rules can be found and
// re-checked rather than being trusted indefinitely.
const RulesVersion = 1

// UnreadableRune is what the converter substitutes for a character the source
// fonts never mapped to Unicode. Its presence means part of the content is
// genuinely missing, so it is always critical.
const UnreadableRune = '\uFFFD'

var (
	// Placeholder markers left behind by converters and templates. Any of these
	// reaching a student is an obvious defect.
	placeholderRe = regexp.MustCompile(`(?i)` +
		`<!--[^>]*-->|` + // HTML comment, e.g. <!-- formula-not-decoded -->
		`\{\{[^}]*\}\}|` + // moustache template
		`\$\{[^}]*\}|` + // shell or JS template
		`\[(?:tbd|todo|fixme|xxx|placeholder|image|figure|diagram)\]|` +
		`\b(?:lorem ipsum|todo|fixme)\b`)

	// Three or more backslashes in a row is not text. It is what a converter
	// emits when it cannot render a run of underscores or a rule.
	backslashRunRe = regexp.MustCompile(`\\{3,}`)

	// HTML entities that were never decoded read as literal "&amp;" on a paper.
	htmlEntityRe = regexp.MustCompile(`&(?:amp|lt|gt|quot|apos|nbsp|#\d{2,5}|#x[0-9a-fA-F]{2,4});`)

	// A replacement character that has itself been misdecoded, which is the one
	// unambiguous literal signature worth matching.
	//
	// A bare U+FFFD is deliberately not matched here: the converter writes it
	// where a font left a glyph unmapped, and that is already reported as
	// unreadable content. Reporting the same hole twice under two names makes a
	// review queue harder to work through, not safer.
	replacementRe = regexp.MustCompile(`ï¿½`)

	// An option label sequence appearing inside a field that should hold one
	// value: "1. foo 2. bar". Two or more is evidence of absorbed content.
	inlineLabelRunRe = regexp.MustCompile(`(?:^|\s)\(?[1-9a-hA-H]\)?[.)]\s+\S+.*?(?:\s)\(?[1-9a-hA-H]\)?[.)]\s+\S+`)

	// A sentence-ending question mark followed by more sentence, which in an
	// option means a whole question got pulled in.
	embeddedQuestionRe = regexp.MustCompile(`\?\s+[A-Z][a-z]+\s+\S+`)

	// Options that refer to enumerated items in the stem: "1 and 2", "Only 1 and
	// 3", "b-c-a", "2, 1, 3, 4". Universal across exam boards.
	enumReferenceRe = regexp.MustCompile(`(?i)^(?:only\s+)?(?:` +
		`\d(?:\s*[,&]\s*|\s+and\s+|\s*[-–]\s*)\d(?:(?:\s*[,&]\s*|\s+and\s+|\s*[-–]\s*)\d)*|` +
		`[a-e](?:\s*[-–,]\s*[a-e]){1,4}|` +
		`(?:all|none|neither|both)\s+(?:of\s+)?(?:the\s+)?(?:above|these|them|statements?)` +
		`)(?:\s+(?:are|is)\s+correct)?\.?$`)

	// An enumeration in the stem that the options can refer to: "1.", "(a)",
	// "Statement 1", "I.".
	stemEnumerationRe = regexp.MustCompile(`(?i)` +
		`statements?\s+(?:1|i|a)\b|` +
		`\b(?:1|i|a)[.)]\s+\S+.*\b(?:2|ii|b)[.)]\s+\S+`)

	// A stem pointing at an indexed part of material that is not in the stem:
	// "fill in blank number 5", "the error in sentence 3", "part 2 of the
	// paragraph". These come from cloze and passage-based sets, where the numbered
	// item lives in the shared passage. Without that passage the question cannot
	// be answered, however clean its own text is.
	externalReferenceRe = regexp.MustCompile(`(?i)\b(?:blank|sentence|statement|part|paragraph|line|item|word)\s+(?:number\s+|no\.?\s*)?\d{1,2}\b`)
)

// finding is an internal defect record before it is graded.
type finding struct {
	code     string
	message  string
	evidence string
}

// Defect codes. These are stable identifiers: the UI groups by them and the
// review queue filters on them, so they are part of the contract.
const (
	CodeUnreadableChars   = "unreadable_chars"
	CodePlaceholder       = "placeholder"
	CodeBackslashRun      = "backslash_run"
	CodeHTMLEntity        = "html_entity"
	CodeMojibake          = "mojibake"
	CodeControlChars      = "control_chars"
	CodeCharacterRun      = "character_run"
	CodeForeignContent    = "foreign_content"
	CodeEmbeddedQuestion  = "embedded_question"
	CodeMarkdownResidue   = "markdown_residue"
	CodeUnbalancedBracket = "unbalanced_bracket"
)

// inspectText runs every corruption check over one field and returns what it
// found. The caller decides severity, because the same defect matters more in a
// stem than in an explanation.
func inspectText(text string) []finding {
	var out []finding
	if strings.TrimSpace(text) == "" {
		return out
	}

	if n := strings.Count(text, string(UnreadableRune)); n > 0 {
		out = append(out, finding{
			code:     CodeUnreadableChars,
			message:  pluralise(n, "character", "characters") + " could not be read from the source font",
			evidence: excerptAround(text, strings.IndexRune(text, UnreadableRune)),
		})
	}
	if m := placeholderRe.FindString(text); m != "" {
		out = append(out, finding{
			code:     CodePlaceholder,
			message:  "contains an unresolved placeholder",
			evidence: trim(m, 80),
		})
	}
	if m := backslashRunRe.FindString(text); m != "" {
		out = append(out, finding{
			code:     CodeBackslashRun,
			message:  "contains a run of backslashes where the source had a rule or blank",
			evidence: trim(m, 40),
		})
	}
	if m := htmlEntityRe.FindString(text); m != "" {
		out = append(out, finding{
			code:     CodeHTMLEntity,
			message:  "contains an undecoded HTML entity",
			evidence: trim(m, 40),
		})
	}
	if m := findMojibake(text); m != "" {
		out = append(out, finding{
			code:     CodeMojibake,
			message:  "text was decoded with the wrong character encoding",
			evidence: trim(m, 40),
		})
	}
	if idx := indexControl(text); idx >= 0 {
		out = append(out, finding{
			code:     CodeControlChars,
			message:  "contains control characters",
			evidence: excerptAround(text, idx),
		})
	}
	if m := repeatedCharRun(text, 6); m != "" {
		out = append(out, finding{
			code:     CodeCharacterRun,
			message:  "contains an implausible run of one character",
			evidence: trim(m, 40),
		})
	}
	if m := markdownResidue(text); m != "" {
		out = append(out, finding{
			code:     CodeMarkdownResidue,
			message:  "contains leftover markup from the converter",
			evidence: trim(m, 40),
		})
	}
	return out
}

// findMojibake returns the first sequence that looks like UTF-8 decoded through
// the wrong code page, or "".
//
// The test is structural rather than a list of known bad strings. Mojibake always
// produces two or more adjacent characters from the upper half of a single-byte
// code page, because each original byte becomes its own character. Real prose in
// any language essentially never does that: an accented letter is followed by an
// ASCII letter, not by another symbol from the same block.
//
// Both families that actually turn up are covered: UTF-8 read as Latin-1 or
// CP1252 ("Ã—", "â€œ", "Â "), and UTF-8 read as a DOS code page, which produces
// box-drawing characters ("├ù", "Γé╣", "┬²").
func findMojibake(text string) string {
	runes := []rune(text)
	for i := 0; i+1 < len(runes); i++ {
		if !mojibakeLead(runes[i]) {
			continue
		}
		if !mojibakeFollow(runes[i+1]) {
			continue
		}
		end := i + 2
		for end < len(runes) && mojibakeFollow(runes[end]) {
			end++
		}
		return string(runes[i:end])
	}
	if m := replacementRe.FindString(text); m != "" {
		return m
	}
	return ""
}

// mojibakeLead reports whether a rune is one of the characters a misdecoded
// multi-byte sequence starts with.
func mojibakeLead(r rune) bool {
	switch r {
	// Latin-1 and CP1252 leads: the first byte of a UTF-8 sequence read as one
	// character. Only the uppercase forms and "â" are listed. Lowercase accented
	// letters are technically valid lead bytes too, but they are also ordinary
	// letters in several languages, and flagging those would put real content in
	// the review queue.
	case 'Ã', 'Â', 'â', 'Î', 'Ð', 'Ñ':
		return true
	// Greek capital gamma, which is what byte 0xE2 becomes in several DOS code
	// pages and is therefore the commonest lead in that family.
	case '\u0393':
		return true
	}
	// Box-drawing and block characters, the rest of the DOS-code-page family.
	return r >= '\u2500' && r <= '\u259f'
}

// mojibakeFollow reports whether a rune is a plausible continuation byte of a
// misdecoded sequence.
func mojibakeFollow(r rune) bool {
	switch {
	case r >= '\u0080' && r <= '\u00ff': // Latin-1 supplement
		return true
	case r >= '\u2000' && r <= '\u206f': // general punctuation, e.g. â€” and â‚¬
		return true
	case r >= '\u20a0' && r <= '\u20bf': // currency symbols
		return true
	case r >= '\u2500' && r <= '\u259f': // box drawing and blocks
		return true
	case r == '\u0152' || r == '\u0153' || r == '\u0161' || r == '\u017e': // CP1252 extras
		return true
	}
	return false
}

// repeatedCharRun returns the first run of one character repeated at least min
// times, or "".
//
// Go's regexp engine has no backreferences, so this is a scan rather than a
// pattern. Characters that legitimately repeat are excluded: digits, the
// underscores and dots used for blanks and leaders, and the dashes and equals
// signs used for rules.
func repeatedCharRun(text string, min int) string {
	const allowed = " \t\n\r_.-*=~0123456789"
	var previous rune = -1
	count := 0
	for i, r := range text {
		if r == previous {
			count++
			if count >= min && !strings.ContainsRune(allowed, r) {
				// Return the run plus a little context.
				end := i + len(string(r))
				start := end - count*len(string(r))
				if start < 0 {
					start = 0
				}
				return text[start:end]
			}
			continue
		}
		previous = r
		count = 1
	}
	return ""
}

// indexControl returns the first control character position, or -1. Tab, newline
// and carriage return are allowed.
func indexControl(text string) int {
	for i, r := range text {
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if unicode.IsControl(r) {
			return i
		}
	}
	return -1
}

// markdownResidue finds converter markup that should never survive into content.
func markdownResidue(text string) string {
	for _, token := range []string{"|---", "<td", "<tr", "</table", "```", "![", "](http"} {
		if strings.Contains(text, token) {
			return token
		}
	}
	return ""
}

// hasForeignContent reports whether a field that should hold a single value looks
// like it absorbed neighbouring material.
//
// This is the check that catches an option holding the next question, a page
// header, or three other options. It is structural, not a word list, so it works
// for any exam and any language that numbers its choices.
func hasForeignContent(text string) (string, bool) {
	if m := inlineLabelRunRe.FindString(text); m != "" {
		return trim(strings.TrimSpace(m), 90), true
	}
	return "", false
}

// hasEmbeddedQuestion reports whether a field contains what reads as a complete
// second question.
func hasEmbeddedQuestion(text string) (string, bool) {
	if m := embeddedQuestionRe.FindString(text); m != "" {
		return trim(strings.TrimSpace(m), 90), true
	}
	return "", false
}

// referencesEnumeration reports whether an option is a pointer into a list the
// stem is supposed to contain, such as "Only 1 and 3".
func referencesEnumeration(text string) bool {
	return enumReferenceRe.MatchString(strings.TrimSpace(text))
}

// stemHasEnumeration reports whether a stem actually contains the numbered items
// its options refer to.
func stemHasEnumeration(text string) bool {
	return stemEnumerationRe.MatchString(text)
}

// referencesExternalMaterial reports whether a stem points at an indexed item it
// does not contain, such as "blank number 5".
func referencesExternalMaterial(text string) (string, bool) {
	if m := externalReferenceRe.FindString(text); m != "" {
		return m, true
	}
	return "", false
}

// normalizeForCompare reduces text to letters and digits in lower case, which is
// how two options are told apart from the same option typed twice.
func normalizeForCompare(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wordCount counts whitespace-separated words.
func wordCount(s string) int { return len(strings.Fields(s)) }

// trim shortens a string for use as evidence.
func trim(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "\u2026"
}

// excerptAround returns a short window of text centred on a byte offset, so a
// reviewer sees the defect in context.
func excerptAround(text string, index int) string {
	if index < 0 {
		return ""
	}
	runes := []rune(text)
	// Convert the byte offset to a rune offset.
	pos := len([]rune(text[:index]))
	start := pos - 30
	if start < 0 {
		start = 0
	}
	end := pos + 30
	if end > len(runes) {
		end = len(runes)
	}
	return trim(string(runes[start:end]), 70)
}

func pluralise(n int, singular, plural string) string {
	word := plural
	if n == 1 {
		word = singular
	}
	return itoa(n) + " " + word
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
