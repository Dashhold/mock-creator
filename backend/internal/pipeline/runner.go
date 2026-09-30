// Package pipeline runs the long work of the engine in the background.
//
// Uploading a document, deriving a pattern and building a paper all create a
// Job row and return immediately. Workers claim jobs, report progress as they
// go, and record the outcome. Nothing in the HTTP layer waits on conversion,
// which is what lets a 200-page scanned book be ingested without a request
// hanging for the duration.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"mockcreator/internal/config"
	"mockcreator/internal/converter"
	"mockcreator/internal/llm"
	"mockcreator/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Runner owns the worker pool.
type Runner struct {
	db   *gorm.DB
	cfg  *config.Config
	conv *converter.Client
	// llm is always non-nil but reports itself unavailable when no model is
	// configured, so every caller can ask without a nil check and the pipeline
	// behaves identically with and without a model.
	llm *llm.Client

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a runner.
func New(db *gorm.DB, cfg *config.Config, conv *converter.Client) *Runner {
	return &Runner{db: db, cfg: cfg, conv: conv, llm: llm.New(cfg.LLM)}
}

// LLM exposes the model client so handlers can report whether it is available.
func (r *Runner) LLM() *llm.Client { return r.llm }

// Start launches the workers and returns immediately.
func (r *Runner) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel

	if err := r.recoverStale(); err != nil {
		log.Printf("pipeline: could not requeue interrupted jobs: %v", err)
	}

	workers := r.cfg.Worker.Concurrency
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		r.wg.Add(1)
		go r.loop(ctx, i+1)
	}
	log.Printf("pipeline: %d worker(s) started", workers)
}

// Shutdown stops accepting work and waits for in-flight jobs, up to timeout.
func (r *Runner) Shutdown(timeout time.Duration) {
	if r.cancel != nil {
		r.cancel()
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Println("pipeline: workers stopped cleanly")
	case <-time.After(timeout):
		log.Println("pipeline: shutdown timed out; interrupted jobs will be requeued on next boot")
	}
}

// recoverStale returns jobs that were mid-flight when the process died to the
// queue, so a restart does not silently lose work.
func (r *Runner) recoverStale() error {
	cutoff := time.Now().Add(-r.cfg.Worker.StaleAfter)
	result := r.db.Model(&models.Job{}).
		Where("status = ? AND (started_at IS NULL OR started_at < ?)", models.StatusProcessing, cutoff).
		Updates(map[string]any{
			"status": models.StatusQueued,
			"stage":  "requeued after restart",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		log.Printf("pipeline: requeued %d interrupted job(s)", result.RowsAffected)
	}
	return nil
}

func (r *Runner) loop(ctx context.Context, worker int) {
	defer r.wg.Done()
	interval := r.cfg.Worker.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.claim()
		if err != nil {
			log.Printf("pipeline[%d]: claim failed: %v", worker, err)
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
			continue
		}

		r.execute(ctx, job)
	}
}

// claim atomically takes the oldest queued job.
//
// SELECT ... FOR UPDATE SKIP LOCKED is what makes several workers safe against
// each other without a separate queue service.
func (r *Runner) claim() (*models.Job, error) {
	var job models.Job
	err := r.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", models.StatusQueued).
			Order("created_at asc").
			First(&job).Error
		if err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
			"status":     models.StatusProcessing,
			"stage":      "starting",
			"progress":   1,
			"started_at": now,
			"attempts":   job.Attempts + 1,
			"error":      "",
		}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job.Status = models.StatusProcessing
	job.Attempts++
	return &job, nil
}

// execute dispatches a job to its handler and records the outcome.
func (r *Runner) execute(ctx context.Context, job *models.Job) {
	log.Printf("pipeline: job %d (%s) started", job.ID, job.Type)

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Watch for a cancellation request so a long conversion can be abandoned.
	watchDone := make(chan struct{})
	go r.watchCancel(jobCtx, job.ID, cancel, watchDone)

	var (
		result any
		err    error
	)
	switch job.Type {
	case models.JobIngest:
		result, err = r.runIngest(jobCtx, job)
	case models.JobPatternAnalysis:
		result, err = r.runPatternAnalysis(jobCtx, job)
	case models.JobPaperBuild:
		result, err = r.runPaperBuild(jobCtx, job)
	case models.JobQuestionAudit:
		result, err = r.runQuestionAudit(jobCtx, job)
	case models.JobPaperQA:
		result, err = r.runPaperQA(jobCtx, job)
	default:
		err = fmt.Errorf("unknown job type %q", job.Type)
	}

	cancel()
	<-watchDone

	switch {
	case err == nil:
		r.complete(job, result)
		log.Printf("pipeline: job %d (%s) completed", job.ID, job.Type)
	case r.wasCancelled(job.ID) || errors.Is(err, context.Canceled):
		r.finish(job, models.StatusCancelled, "cancelled", "job was cancelled")
		log.Printf("pipeline: job %d (%s) cancelled", job.ID, job.Type)
	default:
		// Retry transient failures; give up once attempts are exhausted so a
		// permanently bad document does not spin forever.
		if job.Attempts < r.cfg.Worker.MaxAttempts {
			r.requeue(job, err)
			log.Printf("pipeline: job %d (%s) failed, will retry: %v", job.ID, job.Type, err)
			return
		}
		r.finish(job, models.StatusFailed, "failed", err.Error())
		log.Printf("pipeline: job %d (%s) failed permanently: %v", job.ID, job.Type, err)
	}
}

// watchCancel polls the cancel flag and trips the context when it is set.
func (r *Runner) watchCancel(ctx context.Context, jobID uint, cancel context.CancelFunc, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.wasCancelled(jobID) {
				cancel()
				return
			}
		}
	}
}

func (r *Runner) wasCancelled(jobID uint) bool {
	var flags []bool
	// Pluck is the documented way to read a single column into a scalar slice.
	if err := r.db.Model(&models.Job{}).
		Where("id = ?", jobID).
		Pluck("cancel_requested", &flags).Error; err != nil {
		return false
	}
	return len(flags) > 0 && flags[0]
}

// progress records a stage and percentage on a job.
func (r *Runner) progress(jobID uint, stage string, percent int) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	if err := r.db.Model(&models.Job{}).Where("id = ?", jobID).Updates(map[string]any{
		"stage":    stage,
		"progress": percent,
	}).Error; err != nil {
		log.Printf("pipeline: could not record progress for job %d: %v", jobID, err)
	}
}

// counters records how much of the work is done, for jobs with a known total.
func (r *Runner) counters(jobID uint, processed, total int) {
	updates := map[string]any{"processed": processed, "total": total}
	if total > 0 {
		updates["progress"] = processed * 100 / total
	}
	_ = r.db.Model(&models.Job{}).Where("id = ?", jobID).Updates(updates).Error
}

func (r *Runner) complete(job *models.Job, result any) {
	updates := map[string]any{
		"status":      models.StatusCompleted,
		"stage":       "done",
		"progress":    100,
		"finished_at": time.Now(),
		"error":       "",
	}
	if result != nil {
		if encoded, err := json.Marshal(result); err == nil {
			updates["result"] = encoded
		}
	}
	if err := r.db.Model(&models.Job{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
		log.Printf("pipeline: could not mark job %d complete: %v", job.ID, err)
	}
}

func (r *Runner) finish(job *models.Job, status models.Status, stage, message string) {
	if err := r.db.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status":      status,
		"stage":       stage,
		"finished_at": time.Now(),
		"error":       message,
	}).Error; err != nil {
		log.Printf("pipeline: could not finalize job %d: %v", job.ID, err)
	}
}

func (r *Runner) requeue(job *models.Job, cause error) {
	if err := r.db.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status":   models.StatusQueued,
		"stage":    "waiting to retry",
		"progress": 0,
		"error":    cause.Error(),
	}).Error; err != nil {
		log.Printf("pipeline: could not requeue job %d: %v", job.ID, err)
	}
}

// Enqueue creates a job row for the workers to pick up.
func (r *Runner) Enqueue(job *models.Job) error {
	job.Status = models.StatusQueued
	job.Stage = "queued"
	job.Progress = 0
	job.CancelRequested = false
	if err := r.db.Create(job).Error; err != nil {
		return fmt.Errorf("enqueue %s job: %w", job.Type, err)
	}
	return nil
}

// RequestCancel flags a job so its worker stops at the next checkpoint.
func (r *Runner) RequestCancel(jobID uint) error {
	var job models.Job
	if err := r.db.First(&job, jobID).Error; err != nil {
		return err
	}
	if job.Status.Terminal() {
		return fmt.Errorf("job %d already finished", jobID)
	}
	return r.db.Model(&models.Job{}).Where("id = ?", jobID).
		Update("cancel_requested", true).Error
}

// decodeParams reads a job's stored parameters.
func decodeParams(job *models.Job, out any) error {
	if len(job.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(job.Params, out); err != nil {
		return fmt.Errorf("decode job params: %w", err)
	}
	return nil
}
