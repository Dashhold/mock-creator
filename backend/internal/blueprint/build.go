// Package blueprint assembles test papers from the question warehouse.
//
// A paper is built from an exam's pattern: each section asks for a number of
// questions from one subject, and the generator fills it from questions that
// exam is allowed to use. That pool is the exam's own extracted questions plus
// anything it borrows through an association, which is what makes a brand new
// exam usable on day one.
//
// Generation is deterministic for a given seed, pattern and warehouse state, so
// a paper can be explained and rebuilt rather than being a one-off accident.
package blueprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"mockcreator/internal/models"

	"gorm.io/gorm"
)

// Request describes the paper to build.
type Request struct {
	ExamID    uint   `json:"exam_id"`
	PatternID *uint  `json:"pattern_id,omitempty"`
	Title     string `json:"title"`

	Type models.PaperType `json:"type"`
	// Seed makes the selection reproducible. Zero asks for a random seed.
	Seed int64 `json:"seed,omitempty"`

	// DifficultyMix overrides the per-section mix from the pattern.
	DifficultyMix *models.DifficultyMix `json:"difficulty_mix,omitempty"`

	// TotalQuestions overrides the pattern's total, scaling every section by its
	// weightage. Zero uses the pattern as it stands.
	TotalQuestions int `json:"total_questions,omitempty"`
	DurationMin    int `json:"duration_min,omitempty"`

	// IncludeBorrowed allows questions reached through an association. Turning
	// this off restricts the paper to the exam's own material.
	IncludeBorrowed bool `json:"include_borrowed"`
	// OnlyApproved restricts to reviewed questions.
	OnlyApproved bool `json:"only_approved"`
	// RequireAnswer keeps questions with no known correct option out, which is
	// almost always what you want in a paper someone will attempt.
	RequireAnswer bool `json:"require_answer"`
	// AllowUnvetted lets questions that have not passed the quality gate into the
	// paper. It exists for internal drafting and diagnosis only: a paper built
	// this way can never be published, and it is marked as such.
	//
	// The default of false is deliberate. A generator that silently accepts
	// unvalidated content is how corrupted questions reach a client.
	AllowUnvetted bool `json:"allow_unvetted,omitempty"`

	YearFrom *int `json:"year_from,omitempty"`
	YearTo   *int `json:"year_to,omitempty"`

	ExcludeQuestionIDs []uint `json:"exclude_question_ids,omitempty"`
	// ExcludePaperIDs avoids questions used by existing papers, so a series of
	// mock papers does not repeat itself.
	ExcludePaperIDs []uint `json:"exclude_paper_ids,omitempty"`

	Status models.Status `json:"status,omitempty"`
	Notes  string        `json:"notes,omitempty"`
}

// WithDefaults fills in sensible values for anything left unset.
func (r Request) WithDefaults() Request {
	if r.Type == "" {
		r.Type = models.PaperBalanced
	}
	if r.Status == "" {
		r.Status = models.StatusDraft
	}
	if r.Seed == 0 {
		r.Seed = rand.Int63()
	}
	return r
}

// Result is a built paper, ready to persist.
type Result struct {
	Paper     models.TestPaper       `json:"paper"`
	Items     []models.TestPaperItem `json:"-"`
	Analytics models.PaperAnalytics  `json:"analytics"`
	Warnings  []string               `json:"warnings,omitempty"`
}

// candidate is one question the generator may choose.
type candidate struct {
	ID         uint
	SubjectID  uint
	Difficulty models.Difficulty
	Year       *int
	LinkType   models.LinkType
	Relevance  float64
}

// sectionPlan is one section's target after scaling.
type sectionPlan struct {
	section models.PatternSection
	name    string
	want    int
	mix     models.DifficultyMix
}

// Build selects questions and returns an unsaved paper.
func Build(db *gorm.DB, req Request) (*Result, error) {
	req = req.WithDefaults()

	var exam models.Exam
	if err := db.First(&exam, req.ExamID).Error; err != nil {
		return nil, fmt.Errorf("load exam %d: %w", req.ExamID, err)
	}

	plan, sections, err := loadPattern(db, req)
	if err != nil {
		return nil, err
	}

	plans, warnings := scaleSections(req, plan, sections)
	if len(plans) == 0 {
		return nil, errors.New("this pattern has no sections with a question count; " +
			"edit the pattern or derive a new one from a past paper")
	}

	excluded, err := resolveExclusions(db, req)
	if err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewSource(req.Seed))
	used := make(map[uint]bool, len(excluded))
	for id := range excluded {
		used[id] = true
	}

	analytics := models.PaperAnalytics{
		DifficultySpread: map[string]int{},
		SubjectSpread:    map[string]int{},
		YearSpread:       map[string]int{},
		BorrowedFrom:     map[string]int{},
	}

	var (
		items    []models.TestPaperItem
		sequence int
		total    float64
	)

	for _, sp := range plans {
		pool, err := loadCandidates(db, req, sp.section)
		if err != nil {
			return nil, err
		}

		available := 0
		for _, c := range pool {
			if !used[c.ID] {
				available++
			}
		}

		chosen, shortfall := selectForSection(pool, used, sp, rng)

		fill := models.SectionFill{Section: sp.name, Requested: sp.want, Filled: len(chosen)}
		for _, c := range chosen {
			used[c.ID] = true
			sequence++

			marks := sp.section.MarksPerQuestion
			if marks <= 0 {
				marks = plan.MarksPerQuestion
			}
			if marks <= 0 {
				marks = 1
			}
			negative := sp.section.NegativeMarks
			if negative <= 0 {
				negative = plan.NegativeMarks
			}

			item := models.TestPaperItem{
				QuestionID:    c.ID,
				SequenceNo:    sequence,
				SectionName:   sp.name,
				Marks:         marks,
				NegativeMarks: negative,
				SourceKind:    c.LinkType,
			}
			if sp.section.SubjectID != nil {
				item.SubjectID = sp.section.SubjectID
			}
			items = append(items, item)
			total += marks

			analytics.DifficultySpread[string(c.Difficulty)]++
			analytics.SubjectSpread[sp.name]++
			if c.Year != nil {
				analytics.YearSpread[fmt.Sprint(*c.Year)]++
			}
			if c.LinkType == models.LinkAssociated {
				analytics.BorrowedCount++
				fill.Borrowed++
			}
		}
		analytics.SectionFill = append(analytics.SectionFill, fill)

		if shortfall > 0 {
			analytics.Shortfalls = append(analytics.Shortfalls, models.SectionShortfall{
				Section:   sp.name,
				Requested: sp.want,
				Available: available,
				Reason:    shortfallReason(req, available, sp.want),
			})
		}
	}

	if len(items) == 0 {
		return nil, errors.New("no questions matched this pattern; upload material for these " +
			"subjects, or associate an exam that already has some")
	}

	// Difficulty is a placeholder on machine-extracted questions, so say so
	// rather than implying the requested mix was actually honoured.
	var unrated int64
	if err := db.Model(&models.Question{}).
		Where("id IN ? AND difficulty_confidence <= 0", itemQuestionIDs(items)).
		Count(&unrated).Error; err == nil && unrated > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d of %d selected questions have no reviewed difficulty, so the difficulty mix is approximate",
			unrated, len(items)))
	}

	analytics.BorrowedFrom = borrowedBreakdown(db, items)
	analytics.EstimatedMinutes = estimateMinutes(db, items, plan.DurationMin)

	duration := req.DurationMin
	if duration <= 0 {
		duration = plan.DurationMin
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("%s mock paper", exam.Name)
	}

	encodedAnalytics, err := json.Marshal(analytics)
	if err != nil {
		return nil, fmt.Errorf("encode analytics: %w", err)
	}

	paper := models.TestPaper{
		ExamID:         req.ExamID,
		PatternID:      &plan.ID,
		Title:          title,
		Type:           req.Type,
		DurationMin:    duration,
		TotalQuestions: len(items),
		TotalMarks:     total,
		NegativeMarks:  plan.NegativeMarks,
		Status:         req.Status,
		Seed:           req.Seed,
		Analytics:      encodedAnalytics,
		Notes:          req.Notes,
	}

	return &Result{Paper: paper, Items: items, Analytics: analytics, Warnings: warnings}, nil
}

// loadPattern resolves the pattern to build against.
func loadPattern(db *gorm.DB, req Request) (models.ExamPattern, []models.PatternSection, error) {
	var plan models.ExamPattern
	query := db.Where("exam_id = ?", req.ExamID)
	if req.PatternID != nil {
		query = query.Where("id = ?", *req.PatternID)
	} else {
		query = query.Where("is_active = ?", true)
	}
	if err := query.First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return plan, nil, errors.New("this exam has no active pattern; add one by hand " +
				"or let the engine derive it from a past paper")
		}
		return plan, nil, fmt.Errorf("load pattern: %w", err)
	}

	var sections []models.PatternSection
	if err := db.Where("exam_pattern_id = ?", plan.ID).
		Order("order_index asc").Find(&sections).Error; err != nil {
		return plan, nil, fmt.Errorf("load pattern sections: %w", err)
	}
	return plan, sections, nil
}

// scaleSections converts pattern sections into targets, honouring a requested
// total by distributing it across sections in proportion to their weightage.
func scaleSections(
	req Request,
	plan models.ExamPattern,
	sections []models.PatternSection,
) ([]sectionPlan, []string) {
	var warnings []string
	usable := make([]models.PatternSection, 0, len(sections))
	for _, s := range sections {
		if s.QuestionCount <= 0 {
			continue
		}
		if s.SubjectID == nil {
			warnings = append(warnings, fmt.Sprintf(
				"section %q is not mapped to a subject and was skipped; assign it in the pattern editor",
				s.Name))
			continue
		}
		usable = append(usable, s)
	}
	if len(usable) == 0 {
		return nil, warnings
	}

	patternTotal := 0
	for _, s := range usable {
		patternTotal += s.QuestionCount
	}

	target := req.TotalQuestions
	if target <= 0 {
		target = patternTotal
	}

	defaultMix := models.DefaultMixFor(req.Type)
	if req.DifficultyMix != nil {
		defaultMix = req.DifficultyMix.Normalized()
	}

	plans := make([]sectionPlan, 0, len(usable))
	assigned := 0
	for i, s := range usable {
		want := s.QuestionCount
		if target != patternTotal && patternTotal > 0 {
			want = int(math.Round(float64(s.QuestionCount) / float64(patternTotal) * float64(target)))
			if want < 1 {
				want = 1
			}
		}
		// The last section absorbs any rounding drift so the total lands exactly.
		if i == len(usable)-1 && target != patternTotal {
			want = target - assigned
			if want < 0 {
				want = 0
			}
		}
		assigned += want

		mix := defaultMix
		if req.DifficultyMix == nil && len(s.DifficultyMix) > 0 {
			var sectionMix models.DifficultyMix
			if err := json.Unmarshal(s.DifficultyMix, &sectionMix); err == nil {
				if sectionMix.Easy+sectionMix.Medium+sectionMix.Hard > 0 {
					mix = sectionMix.Normalized()
				}
			}
		}

		name := s.Name
		if name == "" {
			name = fmt.Sprintf("Section %d", i+1)
		}

		plans = append(plans, sectionPlan{section: s, name: name, want: want, mix: mix})
	}
	return plans, warnings
}

// loadCandidates fetches the questions an exam may use for one section.
//
// The join on question_exam_links is what enforces association rules: a question
// is reachable only if a link row exists for this exam.
func loadCandidates(db *gorm.DB, req Request, section models.PatternSection) ([]candidate, error) {
	query := db.Table("questions AS q").
		Select(`q.id, q.subject_id, q.difficulty, q.year,
		        l.link_type AS link_type, l.relevance AS relevance`).
		Joins("JOIN question_exam_links l ON l.question_id = q.id").
		Where("q.deleted_at IS NULL").
		Where("l.exam_id = ?", req.ExamID).
		Where("q.subject_id = ?", *section.SubjectID)

	if !req.IncludeBorrowed {
		query = query.Where("l.link_type = ?", models.LinkDirect)
	}
	if req.OnlyApproved {
		query = query.Where("q.status = ?", models.StatusApproved)
	} else {
		query = query.Where("q.status <> ?", models.StatusRejected)
	}
	if req.RequireAnswer {
		query = query.Where("q.has_answer = ?", true)
	}

	// The quality gate. This is the single condition that keeps a defective
	// question out of a paper, and it is applied in the query rather than in
	// application code so no caller can forget it.
	//
	// "unchecked" is excluded along with "failed" and "review": a question nobody
	// has validated is not known to be sound, and treating unknown as acceptable
	// is how the previous generator shipped corrupted content.
	if !req.AllowUnvetted {
		query = query.Where("q.quality_status = ?", models.QualityPass)
	}
	if req.YearFrom != nil {
		query = query.Where("q.year IS NULL OR q.year >= ?", *req.YearFrom)
	}
	if req.YearTo != nil {
		query = query.Where("q.year IS NULL OR q.year <= ?", *req.YearTo)
	}

	var out []candidate
	if err := query.Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load candidates for section %q: %w", section.Name, err)
	}
	return out, nil
}

// resolveExclusions gathers question ids that must not be selected.
func resolveExclusions(db *gorm.DB, req Request) (map[uint]bool, error) {
	excluded := make(map[uint]bool, len(req.ExcludeQuestionIDs))
	for _, id := range req.ExcludeQuestionIDs {
		excluded[id] = true
	}
	if len(req.ExcludePaperIDs) == 0 {
		return excluded, nil
	}

	var ids []uint
	if err := db.Model(&models.TestPaperItem{}).
		Where("test_paper_id IN ?", req.ExcludePaperIDs).
		Distinct().
		Pluck("question_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("load questions used by earlier papers: %w", err)
	}
	for _, id := range ids {
		excluded[id] = true
	}
	return excluded, nil
}

// selectForSection picks questions for one section honouring its difficulty mix.
//
// Buckets that cannot be filled fall back to the nearest available difficulty
// rather than leaving the section short, and the shortfall is reported so the
// user learns what to upload next.
func selectForSection(
	pool []candidate,
	used map[uint]bool,
	sp sectionPlan,
	rng *rand.Rand,
) ([]candidate, int) {
	buckets := map[models.Difficulty][]candidate{}
	for _, c := range pool {
		if used[c.ID] {
			continue
		}
		difficulty := c.Difficulty
		if !difficulty.Valid() {
			difficulty = models.DifficultyMedium
		}
		buckets[difficulty] = append(buckets[difficulty], c)
	}

	// Prefer closer matches, then shuffle deterministically within equal
	// relevance so repeat builds with the same seed agree.
	for difficulty := range buckets {
		list := buckets[difficulty]
		rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		sort.SliceStable(list, func(i, j int) bool { return list[i].Relevance > list[j].Relevance })
		buckets[difficulty] = list
	}

	targets := map[models.Difficulty]int{
		models.DifficultyEasy:   sp.want * sp.mix.Easy / 100,
		models.DifficultyMedium: sp.want * sp.mix.Medium / 100,
		models.DifficultyHard:   sp.want * sp.mix.Hard / 100,
	}
	// Rounding leaves a remainder; medium absorbs it.
	assigned := targets[models.DifficultyEasy] + targets[models.DifficultyMedium] + targets[models.DifficultyHard]
	targets[models.DifficultyMedium] += sp.want - assigned

	var chosen []candidate
	take := func(difficulty models.Difficulty, count int) {
		list := buckets[difficulty]
		for count > 0 && len(list) > 0 {
			chosen = append(chosen, list[0])
			list = list[1:]
			count--
		}
		buckets[difficulty] = list
	}

	for _, difficulty := range models.AllDifficulties() {
		take(difficulty, targets[difficulty])
	}

	// Backfill from whatever is left, nearest difficulty first.
	fallbackOrder := []models.Difficulty{
		models.DifficultyMedium, models.DifficultyEasy, models.DifficultyHard,
	}
	for len(chosen) < sp.want {
		progressed := false
		for _, difficulty := range fallbackOrder {
			if len(buckets[difficulty]) == 0 {
				continue
			}
			take(difficulty, 1)
			progressed = true
			if len(chosen) >= sp.want {
				break
			}
		}
		if !progressed {
			break
		}
	}

	return chosen, sp.want - len(chosen)
}

func shortfallReason(req Request, available, want int) string {
	switch {
	case available == 0 && !req.IncludeBorrowed:
		return "no questions for this subject are attached to this exam; " +
			"upload material or enable borrowing from an associated exam"
	case available == 0 && !req.AllowUnvetted:
		return "no questions for this subject have passed the quality checks; " +
			"clear the review queue for this subject or upload better source material"
	case available == 0:
		return "no questions for this subject are available to this exam"
	case req.RequireAnswer:
		return fmt.Sprintf("only %d of %d needed questions have a known answer; "+
			"upload the answer key for this subject", available, want)
	case req.OnlyApproved:
		return fmt.Sprintf("only %d of %d needed questions are approved", available, want)
	default:
		return fmt.Sprintf("only %d of %d needed questions exist", available, want)
	}
}

func itemQuestionIDs(items []models.TestPaperItem) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.QuestionID)
	}
	return ids
}

// SectionAvailability reports what the warehouse can currently supply for one
// section, so the UI can show whether a paper is buildable before asking for it.
type SectionAvailability struct {
	Section     string `json:"section"`
	SubjectID   *uint  `json:"subject_id,omitempty"`
	SubjectName string `json:"subject_name,omitempty"`
	Requested   int    `json:"requested"`
	Own         int    `json:"own"`
	Borrowed    int    `json:"borrowed"`
	Answered    int    `json:"answered"`
	Approved    int    `json:"approved"`
	// Deliverable is how many have passed the quality checks, and HeldForReview
	// how many are waiting. Together they turn "not enough questions" into an
	// actionable answer: upload more, or clear the review queue.
	Deliverable   int  `json:"deliverable"`
	HeldForReview int  `json:"held_for_review"`
	Sufficient    bool `json:"sufficient"`
}

// Availability describes whether an exam can be built into a paper right now.
type Availability struct {
	ExamID         uint                  `json:"exam_id"`
	PatternID      uint                  `json:"pattern_id"`
	PatternName    string                `json:"pattern_name"`
	TotalRequested int                   `json:"total_requested"`
	TotalAvailable int                   `json:"total_available"`
	Buildable      bool                  `json:"buildable"`
	Sections       []SectionAvailability `json:"sections"`
	Warnings       []string              `json:"warnings,omitempty"`
}

// Inspect reports per-section supply without building anything.
func Inspect(db *gorm.DB, req Request) (*Availability, error) {
	req = req.WithDefaults()

	plan, sections, err := loadPattern(db, req)
	if err != nil {
		return nil, err
	}
	plans, warnings := scaleSections(req, plan, sections)

	out := &Availability{
		ExamID:      req.ExamID,
		PatternID:   plan.ID,
		PatternName: plan.Name,
		Warnings:    warnings,
		Buildable:   true,
	}

	subjectNames := map[uint]string{}
	var subjects []models.Subject
	if err := db.Find(&subjects).Error; err == nil {
		for _, s := range subjects {
			subjectNames[s.ID] = s.Name
		}
	}

	for _, sp := range plans {
		// The pool is counted under the same quality gate the build will apply, so
		// availability reports what is actually usable rather than what exists.
		// Reporting the larger number is how a user plans a paper around questions
		// the generator will then refuse to pick.
		pool, err := loadCandidates(db, Request{
			ExamID:          req.ExamID,
			IncludeBorrowed: true,
			OnlyApproved:    false,
			AllowUnvetted:   req.AllowUnvetted,
			YearFrom:        req.YearFrom,
			YearTo:          req.YearTo,
		}, sp.section)
		if err != nil {
			return nil, err
		}

		entry := SectionAvailability{
			Section:   sp.name,
			SubjectID: sp.section.SubjectID,
			Requested: sp.want,
		}
		if sp.section.SubjectID != nil {
			entry.SubjectName = subjectNames[*sp.section.SubjectID]
		}
		for _, c := range pool {
			if c.LinkType == models.LinkAssociated {
				entry.Borrowed++
			} else {
				entry.Own++
			}
		}

		// Answered and approved counts are what actually limit a usable paper.
		countWith := func(extra func(*gorm.DB) *gorm.DB) int {
			q := db.Table("questions AS q").
				Joins("JOIN question_exam_links l ON l.question_id = q.id").
				Where("q.deleted_at IS NULL AND l.exam_id = ? AND q.subject_id = ?",
					req.ExamID, *sp.section.SubjectID)
			if !req.IncludeBorrowed {
				q = q.Where("l.link_type = ?", models.LinkDirect)
			}
			var n int64
			if err := extra(q).Count(&n).Error; err != nil {
				return 0
			}
			return int(n)
		}
		if sp.section.SubjectID != nil {
			entry.Answered = countWith(func(q *gorm.DB) *gorm.DB {
				return q.Where("q.has_answer = ?", true)
			})
			entry.Approved = countWith(func(q *gorm.DB) *gorm.DB {
				return q.Where("q.status = ?", models.StatusApproved)
			})
			entry.Deliverable = countWith(func(q *gorm.DB) *gorm.DB {
				return q.Where("q.quality_status = ?", models.QualityPass)
			})
			entry.HeldForReview = countWith(func(q *gorm.DB) *gorm.DB {
				return q.Where("q.quality_status <> ?", models.QualityPass)
			})
		}

		usable := entry.Own
		if req.IncludeBorrowed {
			usable += entry.Borrowed
		}
		if req.RequireAnswer && entry.Answered < usable {
			usable = entry.Answered
		}
		if req.OnlyApproved && entry.Approved < usable {
			usable = entry.Approved
		}

		entry.Sufficient = usable >= sp.want
		if !entry.Sufficient {
			out.Buildable = false
		}

		out.TotalRequested += sp.want
		out.TotalAvailable += usable
		out.Sections = append(out.Sections, entry)
	}

	return out, nil
}

// borrowedBreakdown reports how many questions came from each source exam.
func borrowedBreakdown(db *gorm.DB, items []models.TestPaperItem) map[string]int {
	var borrowedIDs []uint
	for _, item := range items {
		if item.SourceKind == models.LinkAssociated {
			borrowedIDs = append(borrowedIDs, item.QuestionID)
		}
	}
	if len(borrowedIDs) == 0 {
		return nil
	}

	type row struct {
		Name  string
		Count int
	}
	var rows []row
	err := db.Table("questions AS q").
		Select("COALESCE(e.name, 'unattributed') AS name, COUNT(*) AS count").
		Joins("LEFT JOIN exams e ON e.id = q.origin_exam_id").
		Where("q.id IN ?", borrowedIDs).
		Group("e.name").
		Scan(&rows).Error
	if err != nil {
		return nil
	}

	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Name] = r.Count
	}
	return out
}

// estimateMinutes sums per-question solve estimates, falling back to the
// pattern's duration when questions carry no estimate.
func estimateMinutes(db *gorm.DB, items []models.TestPaperItem, fallback int) int {
	var totalSeconds int
	if err := db.Model(&models.Question{}).
		Where("id IN ?", itemQuestionIDs(items)).
		Select("COALESCE(SUM(estimated_solve_sec), 0)").
		Scan(&totalSeconds).Error; err != nil || totalSeconds == 0 {
		return fallback
	}
	return int(math.Ceil(float64(totalSeconds) / 60))
}
