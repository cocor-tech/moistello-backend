package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
)

type ContributionHandler struct {
	contribService contribution.Service
	contribRepo    contribution.Repository
}

func NewContributionHandler(svc contribution.Service, repo contribution.Repository) *ContributionHandler {
	return &ContributionHandler{contribService: svc, contribRepo: repo}
}

// @Summary List contributions
// @Description Returns paginated contributions filtered by userId, circleId, or the authenticated user's own contributions.
// @Tags Contributions
// @Produce json
// @Security BearerAuth
// @Param userId query string false "Filter by user ID"
// @Param circleId query string false "Filter by circle ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{contributions=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /contributions [get]
func (h *ContributionHandler) ListContributions(c *gin.Context) {
	userIDFilter := c.Query("userId")
	circleIDFilter := c.Query("circleId")
	page, limit, _ := pagination.Parse(c)

	if circleIDFilter != "" {
		contribs, total, err := h.contribService.GetCircleHistory(c.Request.Context(), circleIDFilter, page, limit)
		if err != nil {
			response.InternalError(c, "failed to list contributions")
			return
		}
		response.OKWithMeta(c, gin.H{"contributions": contribs}, response.NewPaginationMeta(page, limit, total))
		return
	}

	if userIDFilter != "" {
		contribs, total, err := h.contribService.GetUserHistory(c.Request.Context(), userIDFilter, page, limit)
		if err != nil {
			response.InternalError(c, "failed to list contributions")
			return
		}
		response.OKWithMeta(c, gin.H{"contributions": contribs}, response.NewPaginationMeta(page, limit, total))
		return
	}

	userID := middleware.GetUserID(c)
	contribs, total, err := h.contribService.GetUserHistory(c.Request.Context(), userID, page, limit)
	if err != nil {
		response.InternalError(c, "failed to list contributions")
		return
	}
	response.OKWithMeta(c, gin.H{"contributions": contribs}, response.NewPaginationMeta(page, limit, total))
}

// @Summary Get a contribution
// @Description Returns a single contribution by ID.
// @Tags Contributions
// @Produce json
// @Security BearerAuth
// @Param id path string true "Contribution ID (UUID)"
// @Success 200 {object} response.Envelope{data=object{contribution=object}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /contributions/{id} [get]
func (h *ContributionHandler) GetContribution(c *gin.Context) {
	id := c.Param("id")
	uid, err := uuid.Parse(id)
	if err != nil {
		response.BadRequest(c, "invalid contribution ID")
		return
	}
	contrib, err := h.contribRepo.FindByID(c.Request.Context(), uid)
	if err != nil {
		response.NotFound(c, "contribution not found")
		return
	}
	response.OK(c, gin.H{"contribution": contrib})
}
