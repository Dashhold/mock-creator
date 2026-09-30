// Package pattern infers an exam's blueprint from real question papers.
//
// A user has two ways to tell the engine how an exam is structured. They can
// type the pattern in, or they can drop in one or more past papers and let this
// package work it out: which sections exist, how many questions each carries,
// what share of the paper that is, how many options a question has, and, when
// the paper prints them, the duration and marking scheme.
//
// Nothing here knows any exam. It reasons only about what several documents
// agree on, and reports how strongly they agreed so the user can judge whether
// to trust the result.
package pattern

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"mockcreator/internal/extract"
	"mockcreator/internal/models"
)

// Observation is one analysed document contributing to a pattern.
type Observation struct {
	DocumentID uint
	Title      string
	Year       *int
	// Markdown is the converted document, scanned for printed duration and
	// marking rules. Optional.
	Markdown string
	Result   extract.ParseResult
}

// SectionDraft is one inferred section.
type SectionDraft struct {
	Name        string  `json:"name"`
	SubjectID   *uint   `json:"subject_id,omitempty"`
	SubjectCode string  `json:"subject_code,omitempty"`
	SubjectName string  `json:"subject_name,omitempty"`
	OrderIndex  int     `json:"order_index"`
	Questions   int     `json:"question_count"`
	Weightage   float64 `json:"weightage"`

	// SeenIn is how many documents contained this section, and Agreement is how
	// consistently they reported the same question count.
	SeenIn    int     `json:"seen_in"`
	Agreement float64 `json:"agreement"`
	// Counts is the per-document question count, kept so the UI can show the
	// spread rather than just a single number.
	Counts []int `json:"counts,omitempty"`
}

// Draft is an inferred pattern, ready to be reviewed and saved.
type Draft struct {
	TotalQuestions   int     `json:"total_questions"`
	DurationMin      int     `json:"duration_min"`
	TotalMarks       float64 `json:"total_marks"`
	MarksPerQuestion float64 `json:"marks_per_question"`
	NegativeMarks    float64 `json:"negative_marks"`
	OptionCount      int     `json:"option_count"`

	Sections []SectionDraft `json:"sections"`

	DerivedFromCount int      `json:"derived_from_count"`
	Confidence       float64  `json:"confidence"`
	Warnings         []string `json:"warnings,omitempty"`
	Notes            string   `json:"notes,omitempty"`

	// UnmatchedSections counts sections whose heading matched no subject, which
	// the user must resolve before the pattern can drive generation.
	UnmatchedSections int `json:"unmatched_sections"`
}

// SubjectIDs returns every subject the draft references, for wiring up the
// exam's subject list.
func (d Draft) SubjectIDs() []uint {
	seen := map[uint]bool{}
	out := []uint{}
	for _, s := range d.Sections {
		if s.SubjectID == nil || seen[*s.SubjectID] {
			continue
		}
		seen[*s.SubjectID] = true
		out = append(out, *s.SubjectID)
	}
	return out
}

// Options tunes the analysis.
type Options struct {
	// DefaultMarksPerQuestion is used when no marking scheme is printed.
	DefaultMarksPerQuestion float64
	// RoundTo snaps inferred question counts to a multiple, which removes
	// off-by-one noise from imperfect extraction. 0 disables rounding.
	RoundTo int
}

func (o Options) withDefaults() Options {
	if o.DefaultMarksPerQuestion <= 0 {
		o.DefaultMarksPerQuestion = 1
	}
	return o
}

// group accumulates one section across documents.
type group struct {
	name       string
	subjectID  *uint
	code       string
	subject    string
	counts     []int
	orderSum   int
	orderCount int
	firstSeen  int
}

// Analyze infers a pattern from one or more parsed papers.
//
// Question counts are taken as the median across documents rather than the
// mean, so one badly extracted paper cannot drag a section's count away from
// what the others agree on.
func Analyze(observations []Observation, opts Options) Draft {
	opts = opts.withDefaults()
	draft := Draft{MarksPerQuestion: opts.DefaultMarksPerQuestion}

	// Only documents that actually yielded questions can inform the pattern.
	usable := make([]Observation, 0, len(observations))
	for _, o := range observations {
		if len(o.Result.Questions) > 0 {
			usable = append(usable, o)
		}
	}
	draft.DerivedFromCount = len(usable)

	if len(usable) == 0 {
		draft.Warnings = append(draft.Warnings,
			"none of the supplied documents yielded any questions, so no pattern could be inferred")
		return draft
	}
	if skipped := len(observations) - len(usable); skipped > 0 {
		draft.Warnings = append(draft.Warnings,
			fmt.Sprintf("%d document(s) yielded no questions and were ignored", skipped))
	}

	groups := collectGroups(usable)
	totals := make([]int, 0, len(usable))
	optionCounts := make([]int, 0, len(usable))
	for _, o := range usable {
		totals = append(totals, len(o.Result.Questions))
		if o.Result.OptionCount > 0 {
			optionCounts = append(optionCounts, o.Result.OptionCount)
		}
	}

	draft.TotalQuestions = roundTo(medianInt(totals), opts.RoundTo)
	draft.OptionCount = modeInt(optionCounts)

	// Marking and timing come from the document header when it prints them.
	scheme := detectScheme(usable)
	if scheme.durationMin > 0 {
		draft.DurationMin = scheme.durationMin
	}
	if scheme.marksPerQuestion > 0 {
		draft.MarksPerQuestion = scheme.marksPerQuestion
	}
	if scheme.negativeMarks > 0 {
		draft.NegativeMarks = scheme.negativeMarks
	}
	if scheme.totalMarks > 0 {
		draft.TotalMarks = scheme.totalMarks
	}

	draft.Sections = buildSections(groups, len(usable), opts)

	// Reconcile the headline total with the sections so the two never disagree.
	sectionTotal := 0
	for _, s := range draft.Sections {
		sectionTotal += s.Questions
	}
	if sectionTotal > 0 {
		draft.TotalQuestions = sectionTotal
	}
	draft.Sections = assignWeightage(draft.Sections, draft.TotalQuestions)

	if draft.TotalMarks <= 0 {
		draft.TotalMarks = float64(draft.TotalQuestions) * draft.MarksPerQuestion
	}
	for i := range draft.Sections {
		draft.Sections[i].Weightage = round2(draft.Sections[i].Weightage)
	}

	for _, s := range draft.Sections {
		if s.SubjectID == nil {
			draft.UnmatchedSections++
		}
	}

	draft.Confidence = confidence(draft, totals, len(usable))
	draft.Warnings = append(draft.Warnings, draftWarnings(draft, usable)...)
	draft.Notes = summarize(draft, usable)
	return draft
}

// collectGroups buckets sections from every document.
//
// Sections are matched on subject when the heading resolved to one, and on the
// normalised heading text otherwise. That way "PART B - General Awareness" in
// one paper lines up with "General Awareness" in another.
func collectGroups(observations []Observation) []*group {
	index := map[string]*group{}
	order := []*group{}

	for docIdx, o := range observations {
		sections := o.Result.Sections
		// A document with no detected headings still contributes its total as a
		// single unnamed section, so single-subject papers are handled.
		if len(sections) == 0 {
			sections = []extract.ParsedSection{{
				Label:         "",
				QuestionCount: len(o.Result.Questions),
				OrderIndex:    0,
			}}
		}

		for _, s := range sections {
			key := "label:" + extract.NormalizeLabel(s.Label)
			if s.Subject != nil && s.Subject.ID != 0 {
				key = fmt.Sprintf("subject:%d", s.Subject.ID)
			}

			g, seen := index[key]
			if !seen {
				g = &group{name: s.Label, firstSeen: docIdx}
				if s.Subject != nil {
					if s.Subject.ID != 0 {
						id := s.Subject.ID
						g.subjectID = &id
					}
					g.code = s.Subject.Code
					g.subject = s.Subject.Name
					if g.name == "" {
						g.name = s.Subject.Name
					}
				}
				index[key] = g
				order = append(order, g)
			}
			// A later document may resolve a subject an earlier one could not.
			if g.subjectID == nil && s.Subject != nil && s.Subject.ID != 0 {
				id := s.Subject.ID
				g.subjectID = &id
				g.code = s.Subject.Code
				g.subject = s.Subject.Name
			}
			g.counts = append(g.counts, s.QuestionCount)
			g.orderSum += s.OrderIndex
			g.orderCount++
		}
	}
	return order
}

// buildSections turns groups into ordered section drafts.
func buildSections(groups []*group, docs int, opts Options) []SectionDraft {
	out := make([]SectionDraft, 0, len(groups))

	for _, g := range groups {
		count := roundTo(medianInt(g.counts), opts.RoundTo)
		if count <= 0 {
			continue
		}
		name := strings.TrimSpace(g.name)
		if name == "" {
			if g.subject != "" {
				name = g.subject
			} else {
				name = "Section"
			}
		}
		out = append(out, SectionDraft{
			Name:        name,
			SubjectID:   g.subjectID,
			SubjectCode: g.code,
			SubjectName: g.subject,
			Questions:   count,
			SeenIn:      len(g.counts),
			Agreement:   round2(agreement(g.counts, docs)),
			Counts:      g.counts,
			OrderIndex:  g.averageOrder(),
		})
	}

	// Order by where the section tended to appear, then by first sighting.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].OrderIndex < out[j].OrderIndex
	})
	for i := range out {
		out[i].OrderIndex = i
	}
	return out
}

func (g *group) averageOrder() int {
	if g.orderCount == 0 {
		return g.firstSeen
	}
	return int(math.Round(float64(g.orderSum) / float64(g.orderCount)))
}

// assignWeightage converts question counts to percentage shares that sum to
// exactly 100, giving the rounding remainder to the largest section.
func assignWeightage(sections []SectionDraft, total int) []SectionDraft {
	if total <= 0 || len(sections) == 0 {
		return sections
	}
	sum := 0.0
	largest, largestCount := 0, -1
	for i := range sections {
		sections[i].Weightage = float64(sections[i].Questions) / float64(total) * 100
		sum += sections[i].Weightage
		if sections[i].Questions > largestCount {
			largest, largestCount = i, sections[i].Questions
		}
	}
	if diff := 100 - sum; math.Abs(diff) > 0.001 {
		sections[largest].Weightage += diff
	}
	return sections
}

// agreement scores how consistently a section appeared and how stable its
// question count was across documents.
func agreement(counts []int, docs int) float64 {
	if len(counts) == 0 || docs == 0 {
		return 0
	}
	presence := float64(len(counts)) / float64(docs)
	if len(counts) == 1 {
		// One sighting tells us the section exists but nothing about stability.
		return presence * 0.6
	}

	mean := 0.0
	for _, c := range counts {
		mean += float64(c)
	}
	mean /= float64(len(counts))
	if mean == 0 {
		return presence * 0.5
	}

	variance := 0.0
	for _, c := range counts {
		d := float64(c) - mean
		variance += d * d
	}
	variance /= float64(len(counts))
	cv := math.Sqrt(variance) / mean

	stability := 1 - cv
	if stability < 0 {
		stability = 0
	}
	return presence * (0.5 + 0.5*stability)
}

// confidence blends section agreement, total-count stability and how much
// evidence there was.
func confidence(draft Draft, totals []int, docs int) float64 {
	if len(draft.Sections) == 0 {
		return 0
	}

	// Section agreement, weighted by how much of the paper each section is.
	weighted, weight := 0.0, 0.0
	for _, s := range draft.Sections {
		w := float64(s.Questions)
		weighted += s.Agreement * w
		weight += w
	}
	sectionScore := 0.0
	if weight > 0 {
		sectionScore = weighted / weight
	}

	// Stability of the overall question count.
	totalScore := 1.0
	if len(totals) > 1 {
		mean := 0.0
		for _, t := range totals {
			mean += float64(t)
		}
		mean /= float64(len(totals))
		if mean > 0 {
			variance := 0.0
			for _, t := range totals {
				d := float64(t) - mean
				variance += d * d
			}
			variance /= float64(len(totals))
			totalScore = 1 - math.Sqrt(variance)/mean
			if totalScore < 0 {
				totalScore = 0
			}
		}
	}

	// More documents means more trust, saturating around five.
	evidence := math.Min(1, 0.45+0.14*float64(docs))

	score := 0.55*sectionScore + 0.25*totalScore + 0.20*evidence

	// Sections that never resolved to a subject are a real gap in the pattern.
	if draft.UnmatchedSections > 0 {
		penalty := float64(draft.UnmatchedSections) / float64(len(draft.Sections))
		score *= 1 - 0.35*penalty
	}

	return round2(math.Max(0.05, math.Min(0.99, score)))
}

func draftWarnings(draft Draft, observations []Observation) []string {
	var out []string

	if draft.DerivedFromCount == 1 {
		out = append(out, "inferred from a single document; add more papers to firm up the section counts")
	}
	if draft.UnmatchedSections > 0 {
		out = append(out, fmt.Sprintf(
			"%d section(s) did not match any subject in the catalogue; assign them in the pattern editor "+
				"or add the heading as an alias on the right subject",
			draft.UnmatchedSections))
	}
	if draft.DurationMin == 0 {
		out = append(out, "no duration was printed in the documents; set it manually")
	}
	if draft.OptionCount == 0 {
		out = append(out, "could not determine how many options each question has")
	}
	if len(draft.Sections) == 1 && draft.Sections[0].SubjectID == nil {
		out = append(out, "no section headings were detected, so the whole paper was treated as one section")
	}

	// Flag documents whose yield looks far off the agreed total.
	for _, o := range observations {
		got := len(o.Result.Questions)
		if draft.TotalQuestions > 0 && math.Abs(float64(got-draft.TotalQuestions)) > float64(draft.TotalQuestions)/3 {
			title := o.Title
			if title == "" {
				title = fmt.Sprintf("document %d", o.DocumentID)
			}
			out = append(out, fmt.Sprintf("%q yielded %d questions against an expected %d and may have parsed poorly",
				title, got, draft.TotalQuestions))
		}
	}
	return out
}

func summarize(draft Draft, observations []Observation) string {
	parts := []string{fmt.Sprintf("Derived from %d document(s)", draft.DerivedFromCount)}
	years := map[int]bool{}
	for _, o := range observations {
		if o.Year != nil {
			years[*o.Year] = true
		}
	}
	if len(years) > 0 {
		list := make([]int, 0, len(years))
		for y := range years {
			list = append(list, y)
		}
		sort.Ints(list)
		strs := make([]string, 0, len(list))
		for _, y := range list {
			strs = append(strs, fmt.Sprint(y))
		}
		parts = append(parts, "years "+strings.Join(strs, ", "))
	}
	parts = append(parts, fmt.Sprintf("%d sections, %d questions", len(draft.Sections), draft.TotalQuestions))
	if draft.OptionCount > 0 {
		parts = append(parts, fmt.Sprintf("%d options per question", draft.OptionCount))
	}
	return strings.Join(parts, "; ") + "."
}

// ToModels renders the draft as an ExamPattern with its sections, ready to
// persist. The caller owns versioning and activation.
func (d Draft) ToModels(examID uint, name string, version int) (models.ExamPattern, []models.PatternSection) {
	p := models.ExamPattern{
		ExamID:           examID,
		Name:             name,
		Version:          version,
		Source:           models.PatternDerived,
		DerivedFromCount: d.DerivedFromCount,
		Confidence:       d.Confidence,
		TotalQuestions:   d.TotalQuestions,
		DurationMin:      d.DurationMin,
		TotalMarks:       d.TotalMarks,
		MarksPerQuestion: d.MarksPerQuestion,
		NegativeMarks:    d.NegativeMarks,
		OptionCount:      d.OptionCount,
		Notes:            d.Notes,
	}

	sections := make([]models.PatternSection, 0, len(d.Sections))
	for _, s := range d.Sections {
		sections = append(sections, models.PatternSection{
			SubjectID:        s.SubjectID,
			Name:             s.Name,
			OrderIndex:       s.OrderIndex,
			QuestionCount:    s.Questions,
			Weightage:        s.Weightage,
			MarksPerQuestion: d.MarksPerQuestion,
			NegativeMarks:    d.NegativeMarks,
		})
	}
	return p, sections
}

// --- small numeric helpers -------------------------------------------------

func medianInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return int(math.Round(float64(sorted[mid-1]+sorted[mid]) / 2))
}

func modeInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	counts := map[int]int{}
	best, bestCount := 0, 0
	for _, v := range values {
		counts[v]++
		if counts[v] > bestCount || (counts[v] == bestCount && v > best) {
			best, bestCount = v, counts[v]
		}
	}
	return best
}

func roundTo(value, multiple int) int {
	if multiple <= 1 || value <= 0 {
		return value
	}
	return int(math.Round(float64(value)/float64(multiple))) * multiple
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
