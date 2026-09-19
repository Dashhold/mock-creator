package handler

import (
	"strings"

	"mockcreator/internal/models"

	"github.com/gin-gonic/gin"
)

// ListJobs returns the background work feed, newest first.
//
// This is what the UI polls to show progress. Everything long-running in the
// system appears here, so there is one place to watch rather than one per
// feature.
func (h *Handler) ListJobs(c *gin.Context) {
	page, size, offset := pageParams(c)

	query := h.DB.Model(&models.Job{}).
		Preload("Document").
		Preload("Exam")
	query = filterQuery(c, query, map[string]string{
		"type":   "type",
		"status": "status",
	})
	if docID := uintQuery(c, "document_id"); docID != nil {
		query = query.Where("document_id = ?", *docID)
	}
	if examID := uintQuery(c, "exam_id"); examID != nil {
		query = query.Where("exam_id = ?", *examID)
	}
	if boolQuery(c, "active", false) {
		query = query.Where("status IN ?", []models.Status{models.StatusQueued, models.StatusProcessing})
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		serverError(c, err)
		return
	}

	var jobs []models.Job
	if err := query.Order("created_at desc").Limit(size).Offset(offset).Find(&jobs).Error; err != nil {
		serverError(c, err)
		return
	}

	// Result payloads can be large; the list omits them and the detail view has them.
	if !boolQuery(c, "with_results", false) {
		for i := range jobs {
			jobs[i].Result = nil
		}
	}

	list(c, jobs, makePage(page, size, total))
}

// GetJob returns one job including its result.
func (h *Handler) GetJob(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var job models.Job
	if err := h.DB.Preload("Document").Preload("Exam").First(&job, id).Error; err != nil {
		dbError(c, err, "job")
		return
	}
	ok(c, gin.H{"job": job, "duration_ms": job.DurationMS()})
}

// CancelJob asks a running job to stop at its next checkpoint.
func (h *Handler) CancelJob(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	if err := h.Jobs.RequestCancel(id); err != nil {
		if strings.Contains(err.Error(), "already finished") {
			conflict(c, err.Error())
			return
		}
		dbError(c, err, "job")
		return
	}
	ok(c, gin.H{"cancel_requested": id})
}

// RetryJob re-queues a failed job with its original parameters.
func (h *Handler) RetryJob(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var job models.Job
	if err := h.DB.First(&job, id).Error; err != nil {
		dbError(c, err, "job")
		return
	}
	if !job.Status.Terminal() {
		conflict(c, "this job is still running")
		return
	}

	retry := &models.Job{
		Type:       job.Type,
		DocumentID: job.DocumentID,
		ExamID:     job.ExamID,
		Params:     job.Params,
	}
	if err := h.Jobs.Enqueue(retry); err != nil {
		serverError(c, err)
		return
	}
	accepted(c, retry)
}

// jobSummary counts jobs by status, for the dashboard.
type jobSummary struct {
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
}

func (h *Handler) countJobs() jobSummary {
	var out jobSummary
	for status, target := range map[models.Status]*int64{
		models.StatusQueued:     &out.Queued,
		models.StatusProcessing: &out.Processing,
		models.StatusCompleted:  &out.Completed,
		models.StatusFailed:     &out.Failed,
	} {
		h.DB.Model(&models.Job{}).Where("status = ?", status).Count(target)
	}
	return out
}
