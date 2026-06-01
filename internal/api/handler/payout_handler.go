package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
)

type PayoutHandler struct {
	payoutService payout.Service
	payoutRepo    payout.Repository
}

func NewPayoutHandler(svc payout.Service, repo payout.Repository) *PayoutHandler {
	return &PayoutHandler{payoutService: svc, payoutRepo: repo}
}

// @Summary List payouts
// @Description Returns paginated payouts filtered by userId, circleId, or the authenticated user's own payouts.
// @Tags Payouts
// @Produce json
// @Security BearerAuth
// @Param userId query string false "Filter by user ID"
// @Param circleId query string false "Filter by circle ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{payouts=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /payouts [get]
func (h *PayoutHandler) ListPayouts(c *gin.Context) {
	userIDFilter := c.Query("userId")
	circleIDFilter := c.Query("circleId")
	page, limit, _ := pagination.Parse(c)

	if circleIDFilter != "" {
		payouts, total, err := h.payoutService.GetCircleHistory(c.Request.Context(), circleIDFilter, page, limit)
		if err != nil {
			response.InternalError(c, "failed to list payouts")
			return
		}
		response.OKWithMeta(c, gin.H{"payouts": payouts}, response.NewPaginationMeta(page, limit, total))
		return
	}

	if userIDFilter != "" {
		payouts, total, err := h.payoutService.GetUserHistory(c.Request.Context(), userIDFilter, page, limit)
		if err != nil {
			response.InternalError(c, "failed to list payouts")
			return
		}
		response.OKWithMeta(c, gin.H{"payouts": payouts}, response.NewPaginationMeta(page, limit, total))
		return
	}

	userID := middleware.GetUserID(c)
	payouts, total, err := h.payoutService.GetUserHistory(c.Request.Context(), userID, page, limit)
	if err != nil {
		response.InternalError(c, "failed to list payouts")
		return
	}
	response.OKWithMeta(c, gin.H{"payouts": payouts}, response.NewPaginationMeta(page, limit, total))
}

// @Summary Get a payout
// @Description Returns a single payout by ID.
// @Tags Payouts
// @Produce json
// @Security BearerAuth
// @Param id path string true "Payout ID (UUID)"
// @Success 200 {object} response.Envelope{data=object{payout=object}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /payouts/{id} [get]
func (h *PayoutHandler) GetPayout(c *gin.Context) {
	id := c.Param("id")
	uid, err := uuid.Parse(id)
	if err != nil {
		response.BadRequest(c, "invalid payout ID")
		return
	}
	p, err := h.payoutRepo.FindByID(c.Request.Context(), uid)
	if err != nil {
		response.NotFound(c, "payout not found")
		return
	}
	response.OK(c, gin.H{"payout": p})
}
