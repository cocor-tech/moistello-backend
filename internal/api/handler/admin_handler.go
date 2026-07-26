package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
)

type AdminHandler struct {
	userService   user.Service
	userRepo      user.Repository
	circleService circle.Service
	auditRepo     audit.Repository
}

func NewAdminHandler(userSvc user.Service, userRepo user.Repository, circleSvc circle.Service, auditRepo audit.Repository) *AdminHandler {
	return &AdminHandler{
		userService:   userSvc,
		userRepo:      userRepo,
		circleService: circleSvc,
		auditRepo:     auditRepo,
	}
}

// @Summary [Admin] List users
// @Description Lists all users with pagination and search. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param search query string false "Search by wallet or email"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{users=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /admin/users [get]
func (h *AdminHandler) ListUsers(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := user.UserFilter{
		Search: c.Query("search"),
		Page:   page,
		Limit:  limit,
	}
	users, err := h.userRepo.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list users")
		return
	}
	total, err := h.userRepo.Count(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to count users")
		return
	}
	response.OKWithMeta(c, gin.H{"users": users}, response.NewPaginationMeta(page, limit, total))
}

// @Summary [Admin] List all circles
// @Description Lists all circles with pagination, search, and status filter. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param search query string false "Search term"
// @Param status query string false "Filter by status"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{circles=array},meta=response.PaginationMeta}
// @Router /admin/circles [get]
func (h *AdminHandler) ListCircles(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := circle.CircleFilter{
		Search: c.Query("search"),
		Status: circle.CircleStatus(c.Query("status")),
		Page:   page,
		Limit:  limit,
	}
	circles, total, err := h.circleService.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list circles")
		return
	}
	response.OKWithMeta(c, gin.H{"circles": circles}, response.NewPaginationMeta(page, limit, total))
}

// @Summary [Admin] Get audit log
// @Description Returns a paginated system audit log. Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{entries=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /admin/audit-log [get]
func (h *AdminHandler) GetAuditLog(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	entries, total, err := h.auditRepo.List(c.Request.Context(), page, limit)
	if err != nil {
		response.InternalError(c, "failed to fetch audit log")
		return
	}
	if entries == nil {
		entries = []audit.AuditEntry{}
	}
	response.OKWithMeta(c, gin.H{"entries": entries}, response.NewPaginationMeta(page, limit, total))
}

// @Summary [Admin] Get system metrics
// @Description Returns platform-wide metrics (users, circles, volume). Admin only.
// @Tags Admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{totalUsers=number,totalCircles=number,activeCircles=number,totalVolumeUSD=number}}
// @Failure 500 {object} response.Envelope
// @Router /admin/metrics [get]
func (h *AdminHandler) GetMetrics(c *gin.Context) {
	totalUsers, err := h.userRepo.Count(c.Request.Context(), user.UserFilter{})
	if err != nil {
		response.InternalError(c, "failed to fetch user count")
		return
	}

	allFilter := circle.CircleFilter{Page: 1, Limit: 1}
	_, totalCircles, err := h.circleService.List(c.Request.Context(), allFilter)
	if err != nil {
		response.InternalError(c, "failed to fetch circle count")
		return
	}

	activeFilter := circle.CircleFilter{Status: circle.CircleStatusActive, Page: 1, Limit: 1}
	_, activeCircles, err := h.circleService.List(c.Request.Context(), activeFilter)
	if err != nil {
		response.InternalError(c, "failed to fetch active circle count")
		return
	}

	response.OK(c, gin.H{
		"totalUsers":     totalUsers,
		"totalCircles":   totalCircles,
		"activeCircles":  activeCircles,
		"totalVolumeUSD": 0, // On-chain volume is tracked by the indexer; expose via dedicated endpoint when ready.
	})
}

// @Summary [Admin] Update feature flag
// @Description Enables or disables a feature flag. Admin only.
// @Tags Admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object{flag=string,value=bool} true "Feature flag name and value"
// @Success 200 {object} response.Envelope{data=object{flag=string,value=bool}}
// @Failure 400 {object} response.Envelope
// @Router /admin/feature-flags [post]
func (h *AdminHandler) UpdateFeatureFlag(c *gin.Context) {
	var req struct {
		Flag  string `json:"flag" binding:"required"`
		Value bool   `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	// Feature-flag persistence (e.g. DB or Redis) is not yet implemented.
	// Return 501 so callers know the operation was not performed.
	c.JSON(http.StatusNotImplemented, gin.H{
		"success": false,
		"error":   "feature flag persistence not yet implemented",
	})
}
