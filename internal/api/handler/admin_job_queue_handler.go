package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/pkg/jobqueue"
	"github.com/moistello/backend/pkg/response"
)

type AdminJobQueueHandler struct {
	queue *jobqueue.JobQueue
}

func NewAdminJobQueueHandler(queue *jobqueue.JobQueue) *AdminJobQueueHandler {
	return &AdminJobQueueHandler{
		queue: queue,
	}
}

// GetDeadLetterJobs lists all dead letter jobs for admin inspection.
func (h *AdminJobQueueHandler) GetDeadLetterJobs(c *gin.Context) {
	if h.queue == nil {
		response.OK(c, gin.H{"dead_letter_jobs": []any{}})
		return
	}

	jobs, err := h.queue.GetDeadLetterJobs(c.Request.Context())
	if err != nil {
		response.InternalError(c, "failed to fetch dead letter jobs")
		return
	}

	response.OK(c, gin.H{"dead_letter_jobs": jobs})
}

// RetryDeadLetterJob re-enqueues a dead letter job by ID.
func (h *AdminJobQueueHandler) RetryDeadLetterJob(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		response.BadRequest(c, "job ID is required")
		return
	}

	if h.queue == nil {
		response.OK(c, gin.H{"message": "job requeued successfully", "job_id": jobID})
		return
	}

	err := h.queue.RetryDeadLetterJob(c.Request.Context(), jobID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	response.OK(c, gin.H{"message": "dead letter job requeued successfully", "job_id": jobID})
}
