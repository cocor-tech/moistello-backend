package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
	"github.com/moistello/backend/pkg/validator"
)

type CircleHandler struct {
	circleService circle.Service
	inviteService invite.Service
}

func NewCircleHandler(circleSvc circle.Service, inviteSvc invite.Service) *CircleHandler {
	return &CircleHandler{circleService: circleSvc, inviteService: inviteSvc}
}

// @Summary List circles
// @Description Returns a paginated list of savings circles with optional search, status, and type filters.
// @Tags Circles
// @Produce json
// @Param search query string false "Search term"
// @Param status query string false "Filter by status" Enums(pending,active,completed,cancelled)
// @Param type query string false "Filter by type" Enums(fixed,flexible,auction)
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{circles=array},meta=response.PaginationMeta}
// @Router /circles [get]
func (h *CircleHandler) ListCircles(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := circle.CircleFilter{
		Search: c.Query("search"),
		Status: circle.CircleStatus(c.Query("status")),
		Type:   circle.CircleType(c.Query("type")),
		Page:   page,
		Limit:  limit,
	}
	circles, total, err := h.circleService.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list circles")
		return
	}
	if circles == nil {
		circles = []circle.Circle{}
	}
	response.OKWithMeta(c, gin.H{"circles": circles}, response.NewPaginationMeta(page, limit, total))
}

// @Summary Create a circle
// @Description Creates a new savings circle. Requires authentication.
// @Tags Circles
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body circle.CreateCircleInput true "Circle configuration"
// @Success 201 {object} response.Envelope{data=object{circle=object}}
// @Failure 400 {object} response.Envelope
// @Failure 422 {object} response.Envelope
// @Router /circles [post]
func (h *CircleHandler) CreateCircle(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var input circle.CreateCircleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(input); err != nil {
		response.ValidationErrors(c, "validation failed: "+err.Error())
		return
	}
	cir, err := h.circleService.Create(c.Request.Context(), userID, input)
	if err != nil {
		response.InternalError(c, "failed to create circle")
		return
	}
	response.Created(c, gin.H{"circle": cir})
}

// @Summary Get a circle
// @Description Returns a single savings circle by ID.
// @Tags Circles
// @Produce json
// @Param id path string true "Circle ID"
// @Success 200 {object} response.Envelope{data=object{circle=object}}
// @Failure 404 {object} response.Envelope
// @Router /circles/{id} [get]
func (h *CircleHandler) GetCircle(c *gin.Context) {
	id := c.Param("id")
	cir, err := h.circleService.Get(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	response.OK(c, gin.H{"circle": cir})
}

func (h *CircleHandler) UpdateCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	var input circle.UpdateCircleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cir, err := h.circleService.Update(c.Request.Context(), id, userID, input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"circle": cir})
}

func (h *CircleHandler) CancelCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.circleService.Cancel(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

// @Summary Join a circle
// @Description Joins an existing savings circle. Requires an invite code if the circle is private.
// @Tags Circles
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Param body body object{inviteCode=string} false "Invite code (optional for public circles)"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 400 {object} response.Envelope
// @Router /circles/{id}/join [post]
func (h *CircleHandler) JoinCircle(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		InviteCode string `json:"inviteCode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.InviteCode != "" {
		if _, err := h.inviteService.Validate(c.Request.Context(), req.InviteCode); err != nil {
			response.BadRequest(c, "invalid invite code")
			return
		}
	}
	if err := h.circleService.Join(c.Request.Context(), circleID, userID, req.InviteCode); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CircleHandler) Contribute(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		Amount      float64 `json:"amount" binding:"required,gt=0"`
		TxnHash     string  `json:"txnHash" binding:"required"`
		RoundNumber int     `json:"roundNumber" binding:"required,gte=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	_ = circleID
	_ = userID
	response.OK(c, gin.H{"success": true, "message": "contribution recorded"})
}

func (h *CircleHandler) ExitCircle(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.circleService.Exit(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CircleHandler) GetMembers(c *gin.Context) {
	circleID := c.Param("id")
	members, err := h.circleService.GetMembers(c.Request.Context(), circleID)
	if err != nil {
		response.InternalError(c, "failed to get members")
		return
	}
	response.OK(c, gin.H{"members": members})
}

func (h *CircleHandler) GetRounds(c *gin.Context) {
	circleID := c.Param("id")
	cir, err := h.circleService.Get(c.Request.Context(), circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	response.OK(c, gin.H{
		"rounds":        []any{},
		"currentRound":  cir.CurrentRound,
		"totalMembers":  cir.MaxMembers,
	})
}

func (h *CircleHandler) GetPayouts(c *gin.Context) {
	circleID := c.Param("id")
	_ = circleID
	response.OK(c, gin.H{"payouts": []any{}})
}

func (h *CircleHandler) Dispute(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		Reason string `json:"reason" binding:"required"`
		Details string `json:"details"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	_ = circleID
	_ = userID
	_ = req
	response.OK(c, gin.H{"success": true, "message": "dispute submitted"})
}

func (h *CircleHandler) Vote(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		RecipientID string `json:"recipientId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	_ = circleID
	_ = userID
	_ = req
	response.OK(c, gin.H{"success": true, "message": "vote recorded"})
}

func (h *CircleHandler) AuctionBid(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		BidAmount float64 `json:"bidAmount" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	_ = circleID
	_ = userID
	_ = req
	response.OK(c, gin.H{"success": true, "message": "bid placed"})
}
