package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/domain/swap"
	"github.com/moistello/backend/pkg/response"
)

type SwapHandler struct {
	swapService *swap.Service
}

func NewSwapHandler(swapService *swap.Service) *SwapHandler {
	return &SwapHandler{
		swapService: swapService,
	}
}

// CreateSwapOffer godoc
// @Summary      Create a new swap offer
// @Description  Creates a P2P swap offer between circle members with zero spread
// @Tags         swaps
// @Accept       json
// @Produce      json
// @Param        request body swap.SwapOfferRequest true "Swap offer creation request"
// @Success      201  {object}  swap.SwapOffer
// @Failure      400  {object}  response.ErrorResponse
// @Failure      401  {object}  response.ErrorResponse
// @Failure      403  {object}  response.ErrorResponse
// @Router       /v1/swap/offer [post]
func (h *SwapHandler) CreateSwapOffer(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		response.Unauthorized(c, "unauthorized")
		return
	}

	var req swap.SwapOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}

	// Set default expiration if not provided
	if req.ExpiresIn == 0 {
		req.ExpiresIn = 24 // Default 24 hours
	}

	offer, err := h.swapService.CreateSwapOffer(c.Request.Context(), userID.(string), req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Created(c, offer)
}

// AcceptSwapOffer godoc
// @Summary      Accept an existing swap offer
// @Description  Accepts a P2P swap offer and executes the atomic swap with zero spread
// @Tags         swaps
// @Accept       json
// @Produce      json
// @Param        request body swap.SwapAcceptRequest true "Swap acceptance request"
// @Success      200  {object}  swap.SwapOffer
// @Failure      400  {object}  response.ErrorResponse
// @Failure      401  {object}  response.ErrorResponse
// @Failure      403  {object}  response.ErrorResponse
// @Failure      404  {object}  response.ErrorResponse
// @Router       /v1/swap/accept [post]
func (h *SwapHandler) AcceptSwapOffer(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		response.Unauthorized(c, "unauthorized")
		return
	}

	var req swap.SwapAcceptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}

	offer, err := h.swapService.AcceptSwapOffer(c.Request.Context(), userID.(string), req.SwapOfferID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.OK(c, offer)
}

// CancelSwapOffer godoc
// @Summary      Cancel a swap offer
// @Description  Cancels an open swap offer owned by the authenticated user
// @Tags         swaps
// @Produce      json
// @Param        request body object{swap_offer_id=string} true "Swap offer to cancel"
// @Success      200  {object}  swap.SwapOffer
// @Failure      400  {object}  response.ErrorResponse
// @Failure      401  {object}  response.ErrorResponse
// @Router       /v1/swap/cancel [post]
func (h *SwapHandler) CancelSwapOffer(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		response.Unauthorized(c, "unauthorized")
		return
	}

	var req struct {
		SwapOfferID string `json:"swap_offer_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}

	offer, err := h.swapService.CancelSwapOffer(c.Request.Context(), userID.(string), req.SwapOfferID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.OK(c, offer)
}

// GetSwapHistory godoc
// @Summary      Get swap history
// @Description  Retrieves the swap history for the authenticated user
// @Tags         swaps
// @Accept       json
// @Produce      json
// @Param        limit query int false "Number of items to return (default: 20, max: 100)"
// @Param        offset query int false "Number of items to skip (default: 0)"
// @Param        circleId query string false "Filter by circle ID"
// @Param        status query string false "Filter by swap status"
// @Success      200  {object}  swap.SwapHistoryResponse
// @Failure      401  {object}  response.ErrorResponse
// @Router       /v1/swap/history [get]
func (h *SwapHandler) GetSwapHistory(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		response.Unauthorized(c, "unauthorized")
		return
	}

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit == 0 || limit > 100 {
		limit = 20
	}

	offset, _ := strconv.Atoi(c.Query("offset"))

	filter := swap.SwapHistoryFilter{
		Limit:  limit,
		Offset: offset,
	}

	if circleID := c.Query("circleId"); circleID != "" {
		filter.CircleID = &circleID
	}

	if status := c.Query("status"); status != "" {
		filter.Status = &status
	}

	history, total, err := h.swapService.GetSwapHistory(c.Request.Context(), userID.(string), filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.OK(c, swap.SwapHistoryResponse{Swaps: history, Total: total, Limit: filter.Limit, Offset: filter.Offset})
}
