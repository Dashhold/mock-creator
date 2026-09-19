package llm

import (
	"strings"
	"unicode"
)

// Grounding is the guarantee that a model did not invent content.
//
// Prompts are asked politely not to fabricate, and models mostly comply. That is
// not good enough for material a student will be graded on, so compliance is
// checked rather than assumed: every fragment a model returns as *content* - a
// corrected stem, a recovered option, a quoted piece of evidence - has to be
// findable in the source text it was shown. A fragment that is not there means
// the model wrote it, and the whole reply is refused.
//
// The check is deliberately tolerant about form and strict about substance. Case,
// punctuation, spacing and hyphenation are ignored, because a model that tidies
// "letter- cluster" into "letter-cluster" has not invented anything. Word choice
// is not ignored: every word has to come from the source, in order.
type Grounding struct {
	// Verified is true when every checked fragment was found in the source.
	Verified bool
	// Ungrounded lists the fragments that were not, so the failure can be shown
	// rather than merely counted.
	Ungrounded []string
}

// tokenize reduces text to comparable words: lower case, letters and digits only.
//
// Splitting on anything that is not alphanumeric is what makes the comparison
// immune to the cosmetic differences a model introduces, while still requiring
// the same words in the same order.
func tokenize(text string) []string {
	var out []string
	var current strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
			continue
		}
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// Source is a prepared body of text that fragments are checked against.
//
// Preparing it once matters: a paper audit checks dozens of fragments against
// the same source, and re-tokenising for each one turns a cheap check into a
// quadratic one.
type Source struct {
	tokens []string
	// index maps each token to the positions it occurs at, so a candidate match
	// starts from a real occurrence rather than scanning the whole document.
	index  map[string][]int
	joined string
}

// NewSource prepares source text for grounding checks.
func NewSource(parts ...string) *Source {
	tokens := tokenize(strings.Join(parts, "\n"))
	index := make(map[string][]int, len(tokens))
	for i, token := range tokens {
		index[token] = append(index[token], i)
	}
	return &Source{tokens: tokens, index: index, joined: strings.Join(tokens, " ")}
}

// Contains reports whether a fragment appears in the source.
//
// A fragment is grounded when its words appear consecutively in the source. Very
// short fragments are accepted, because a one or two word answer carries too
// little signal to distinguish quotation from coincidence, and rejecting them
// would make the check useless on option text like "Mizoram".
func (s *Source) Contains(fragment string) bool {
	want := tokenize(fragment)
	if len(want) == 0 {
		return true
	}
	if len(s.tokens) == 0 {
		return false
	}
	if len(want) <= 2 {
		// Short fragments only need their words present somewhere.
		for _, token := range want {
			if len(s.index[token]) == 0 {
				return false
			}
		}
		return true
	}
	// A contiguous run is the normal case: the model quoted a span of the source.
	if strings.Contains(s.joined, strings.Join(want, " ")) {
		return true
	}
	// Failing that, allow the words to appear in order with small gaps. This
	// covers a model dropping a stray artefact from the middle of a quote, which
	// is a repair rather than an invention.
	return s.containsInOrder(want, len(want)/3+2)
}

// containsInOrder reports whether every word appears in order, skipping at most
// maxSkips source words in total.
func (s *Source) containsInOrder(want []string, maxSkips int) bool {
	for _, start := range s.index[want[0]] {
		pos := start
		skips := 0
		matched := 1
		for _, token := range want[1:] {
			found := false
			for next := pos + 1; next < len(s.tokens) && next <= pos+1+maxSkips-skips; next++ {
				if s.tokens[next] == token {
					skips += next - pos - 1
					pos = next
					matched++
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
		if matched == len(want) {
			return true
		}
	}
	return false
}

// Check verifies every fragment against the source and reports the result.
func (s *Source) Check(fragments ...string) Grounding {
	out := Grounding{Verified: true}
	for _, fragment := range fragments {
		if strings.TrimSpace(fragment) == "" {
			continue
		}
		if !s.Contains(fragment) {
			out.Verified = false
			out.Ungrounded = append(out.Ungrounded, trimTo(fragment, 160))
		}
	}
	return out
}

// CoverageOf reports what share of a fragment's words exist in the source. It is
// used for reporting rather than gating, so a near miss can be described to a
// reviewer instead of appearing as a bare failure.
func (s *Source) CoverageOf(fragment string) float64 {
	want := tokenize(fragment)
	if len(want) == 0 {
		return 1
	}
	found := 0
	for _, token := range want {
		if len(s.index[token]) > 0 {
			found++
		}
	}
	return float64(found) / float64(len(want))
}
