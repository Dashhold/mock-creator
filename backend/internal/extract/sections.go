package extract

import (
	"encoding/json"
	"strings"

	"mockcreator/internal/models"
)

// SubjectRef is a resolved subject together with how confident the match was.
type SubjectRef struct {
	ID           uint    `json:"id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Confidence   float64 `json:"confidence"`
	MatchedAlias string  `json:"matched_alias,omitempty"`
}

// TopicRef is a resolved topic.
type TopicRef struct {
	ID         uint    `json:"id"`
	SubjectID  uint    `json:"subject_id"`
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
}

// aliasEntry is one catalogue row expanded into its searchable forms.
type aliasEntry struct {
	id        uint
	subjectID uint
	code      string
	name      string
	aliases   []string // normalised
	tokens    [][]string
}

// aliasIndex matches free text against catalogue aliases. It is built per
// parse run from whatever is currently in the database, which is what keeps
// subject recognition out of the code.
type aliasIndex struct {
	exact   map[string]*aliasEntry
	entries []*aliasEntry
}

func newAliasIndex() *aliasIndex {
	return &aliasIndex{exact: map[string]*aliasEntry{}}
}

func (ix *aliasIndex) add(e *aliasEntry, forms []string) {
	seen := map[string]bool{}
	for _, form := range forms {
		norm := normalizeHeading(form)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		e.aliases = append(e.aliases, norm)
		e.tokens = append(e.tokens, strings.Fields(norm))
		// First writer wins so a user's own subject is not shadowed by a
		// starter-catalogue duplicate.
		if _, exists := ix.exact[norm]; !exists {
			ix.exact[norm] = e
		}
	}
	ix.entries = append(ix.entries, e)
}

// lookup finds the best entry for a line of text.
func (ix *aliasIndex) lookup(line string) (*aliasEntry, string, float64) {
	norm := normalizeHeading(line)
	if norm == "" {
		return nil, "", 0
	}

	// Exact heading match is the strongest signal.
	if e, hit := ix.exact[norm]; hit {
		return e, norm, 1.0
	}

	lineTokens := strings.Fields(norm)
	if len(lineTokens) == 0 {
		return nil, "", 0
	}
	lineSet := make(map[string]bool, len(lineTokens))
	for _, t := range lineTokens {
		lineSet[t] = true
	}

	var (
		bestEntry *aliasEntry
		bestAlias string
		bestScore float64
	)

	for _, e := range ix.entries {
		for i, alias := range e.aliases {
			// Containment: the heading carries extra words around a known alias,
			// e.g. "Part B - General Awareness (25 Questions)".
			if len(alias) >= 4 && strings.Contains(norm, alias) {
				// Longer aliases inside shorter headings are more convincing.
				score := 0.70 + 0.25*float64(len(alias))/float64(len(norm))
				if score > 0.95 {
					score = 0.95
				}
				if score > bestScore {
					bestEntry, bestAlias, bestScore = e, alias, score
				}
				continue
			}

			// Token overlap catches word-order and filler differences.
			aliasTokens := e.tokens[i]
			if len(aliasTokens) == 0 {
				continue
			}
			matched := 0
			for _, t := range aliasTokens {
				if lineSet[t] {
					matched++
				}
			}
			if matched == 0 {
				continue
			}
			// Require most of the alias to be present, and also require the line to
			// be mostly the alias. Coverage alone is not enough: a one-word alias
			// like "english" covers itself completely inside any sentence that
			// happens to mention English, and treating that as a section heading
			// ends the question being read and breaks the numbering for everything
			// after it.
			coverage := float64(matched) / float64(len(aliasTokens))
			if coverage < 0.75 {
				continue
			}
			density := float64(matched) / float64(len(lineTokens))
			if density < 0.6 {
				continue
			}
			score := 0.5*coverage + 0.45*density
			if score > bestScore {
				bestEntry, bestAlias, bestScore = e, alias, score
			}
		}
	}

	if bestScore < minAliasScore {
		return nil, "", 0
	}
	return bestEntry, bestAlias, bestScore
}

// minAliasScore is how convincing an alias match has to be.
//
// It is set high on purpose. A missed section heading costs one subject
// assignment, which a user fixes by adding an alias; a false one closes the
// question being read and desynchronises the numbering for the rest of the
// document. The two errors are not remotely equal, so the threshold is set where
// only a heading that really is mostly a subject name gets through.
const minAliasScore = 0.75

// SubjectMatcher recognises section headings as subjects.
type SubjectMatcher struct {
	ix *aliasIndex
}

// NewSubjectMatcher builds a matcher from the subject catalogue. Each subject
// contributes its name, its code and every alias stored against it.
func NewSubjectMatcher(subjects []models.Subject) *SubjectMatcher {
	ix := newAliasIndex()
	for i := range subjects {
		s := subjects[i]
		entry := &aliasEntry{id: s.ID, code: s.Code, name: s.Name}
		forms := []string{s.Name, s.Code}
		forms = append(forms, decodeAliases(s.Aliases)...)
		ix.add(entry, forms)
	}
	return &SubjectMatcher{ix: ix}
}

// Match resolves a line to a subject. It only accepts lines that look like
// headings unless the text matches an alias exactly, which keeps a passing
// mention of "geography" inside a question from starting a new section.
func (m *SubjectMatcher) Match(line string) (SubjectRef, bool) {
	if m == nil || m.ix == nil {
		return SubjectRef{}, false
	}
	entry, alias, score := m.ix.lookup(line)
	if entry == nil {
		return SubjectRef{}, false
	}
	if score < 1.0 && !looksLikeHeading(line) {
		return SubjectRef{}, false
	}
	return SubjectRef{
		ID:           entry.id,
		Code:         entry.code,
		Name:         entry.name,
		Confidence:   score,
		MatchedAlias: alias,
	}, true
}

// Empty reports whether the catalogue had nothing to match against.
func (m *SubjectMatcher) Empty() bool {
	return m == nil || m.ix == nil || len(m.ix.entries) == 0
}

// TopicMatcher recognises topic names, used to tag questions after extraction.
type TopicMatcher struct {
	ix *aliasIndex
}

// NewTopicMatcher builds a matcher from topic rows.
func NewTopicMatcher(topics []models.Topic) *TopicMatcher {
	ix := newAliasIndex()
	for i := range topics {
		t := topics[i]
		entry := &aliasEntry{id: t.ID, subjectID: t.SubjectID, code: t.Code, name: t.Name}
		forms := []string{t.Name, t.Code}
		forms = append(forms, decodeAliases(t.Aliases)...)
		ix.add(entry, forms)
	}
	return &TopicMatcher{ix: ix}
}

// Match resolves text to a topic, optionally restricted to one subject.
func (m *TopicMatcher) Match(text string, subjectID uint) (TopicRef, bool) {
	if m == nil || m.ix == nil {
		return TopicRef{}, false
	}
	entry, _, score := m.ix.lookup(text)
	if entry == nil {
		return TopicRef{}, false
	}
	if subjectID != 0 && entry.subjectID != subjectID {
		return TopicRef{}, false
	}
	return TopicRef{
		ID:         entry.id,
		SubjectID:  entry.subjectID,
		Code:       entry.code,
		Name:       entry.name,
		Confidence: score,
	}, true
}

// Empty reports whether there are any topics to match.
func (m *TopicMatcher) Empty() bool {
	return m == nil || m.ix == nil || len(m.ix.entries) == 0
}

// decodeAliases reads the JSON alias array stored on a catalogue row. Malformed
// data is ignored rather than failing the parse.
func decodeAliases(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	cleaned := make([]string, 0, len(out))
	for _, a := range out {
		if a = strings.TrimSpace(a); a != "" {
			cleaned = append(cleaned, a)
		}
	}
	return cleaned
}

// EncodeAliases serialises an alias list for storage.
func EncodeAliases(aliases []string) ([]byte, error) {
	cleaned := make([]string, 0, len(aliases))
	seen := map[string]bool{}
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		key := strings.ToLower(a)
		if seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, a)
	}
	return json.Marshal(cleaned)
}
