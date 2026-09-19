package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mockcreator/internal/blueprint"
	"mockcreator/internal/models"

	"gorm.io/gorm"
)

// PaperBuildResult is what a paper-build job returns.
type PaperBuildResult struct {
	PaperID        uint                  `json:"paper_id"`
	Title          string                `json:"title"`
	TotalQuestions int                   `json:"total_questions"`
	TotalMarks     float64               `json:"total_marks"`
	Seed           int64                 `json:"seed"`
	Analytics      models.PaperAnalytics `json:"analytics"`

	// QualityStatus and Publishable are the headline: whether this paper may
	// actually be delivered. They are part of the build result rather than
	// something to look up afterwards, so "the build succeeded" can never be
	// mistaken for "the paper is good".
	QualityStatus models.QualityStatus `json:"quality_status"`
	Publishable   bool                 `json:"publishable"`
	QA            *PaperQAResult       `json:"qa,omitempty"`

	Warnings   []string `json:"warnings,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

// runPaperBuild assembles a paper from the warehouse and saves it.
//
// Generation runs as a job rather than inline because selecting from a large
// warehouse across many sections is not instant, and because the UI already
// tracks jobs; a paper build shows up in the same activity feed as an ingest.
func (r *Runner) runPaperBuild(ctx context.Context, job *models.Job) (any, error) {
	started := time.Now()

	if job.ExamID == nil {
		return nil, errors.New("paper build job has no exam")
	}

	var req blueprint.Request
	if err := decodeParams(job, &req); err != nil {
		return nil, err
	}
	req.ExamID = *job.ExamID

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	r.progress(job.ID, "selecting questions", 20)

	built, err := blueprint.Build(r.db, req)
	if err != nil {
		return nil, err
	}

	r.progress(job.ID, "saving paper", 75)

	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&built.Paper).Error; err != nil {
			return fmt.Errorf("create paper: %w", err)
		}
		for i := range built.Items {
			built.Items[i].TestPaperID = built.Paper.ID
		}
		if len(built.Items) > 0 {
			if err := tx.CreateInBatches(built.Items, 200).Error; err != nil {
				return fmt.Errorf("create paper items: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := r.db.Model(&models.Job{}).Where("id = ?", job.ID).
		Update("paper_id", built.Paper.ID).Error; err != nil {
		return nil, fmt.Errorf("attach paper to job: %w", err)
	}

	result := PaperBuildResult{
		PaperID:        built.Paper.ID,
		Title:          built.Paper.Title,
		TotalQuestions: built.Paper.TotalQuestions,
		TotalMarks:     built.Paper.TotalMarks,
		Seed:           built.Paper.Seed,
		Analytics:      built.Analytics,
		Warnings:       built.Warnings,
	}

	// QA runs as part of the build, not afterwards on request. A paper that exists
	// without a verdict is a paper someone can export, and "nobody checked it
	// yet" is indistinguishable from "it is fine" once it has left the building.
	r.progress(job.ID, "quality review", 85)
	qa, err := r.checkPaper(ctx, built.Paper.ID, false, func(stage string, pct int) {
		r.progress(job.ID, stage, 85+pct/8)
	})
	if err != nil {
		// The paper is saved; failing the job would lose it. The unchecked status
		// already blocks publication, so the honest outcome is to report this.
		result.Warnings = append(result.Warnings,
			"the paper was built but its quality review could not run: "+err.Error())
		result.QA = &PaperQAResult{
			PaperID:     built.Paper.ID,
			Status:      models.QualityUnchecked,
			Publishable: false,
			Explanation: "not checked yet, so it cannot be published",
		}
		return result, nil
	}

	result.QA = qa
	result.QualityStatus = qa.Status
	result.Publishable = qa.Publishable
	if !qa.Publishable {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"this paper is %s: %d critical and %d major issue(s) must be resolved before it can be published",
			qa.Status, qa.Critical, qa.Major))
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}
