package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"src/repository"
)

type AdminHandler struct {
	adminRepo *repository.AdminRepository
}

func NewAdminHandler(adminRepo *repository.AdminRepository) *AdminHandler {
	return &AdminHandler{adminRepo: adminRepo}
}

// GetAuditLogs returns paginated real system audit events.
func (h *AdminHandler) GetAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	if limit > 100 {
		limit = 100
	}

	logs, err := h.adminRepo.GetAuditLogs(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch audit records"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": logs,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
		},
	})
}

// GetMetrics returns real-time platform database statistics.
func (h *AdminHandler) GetMetrics(c *gin.Context) {
	metrics, err := h.adminRepo.GetPlatformMetrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute platform metrics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"metrics": metrics})
}