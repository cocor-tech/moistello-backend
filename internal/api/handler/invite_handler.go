package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/pkg/response"
)

type InviteHandler struct {
	inviteService invite.Service
}

func NewInviteHandler(svc invite.Service) *InviteHandler {
	return &InviteHandler{inviteService: svc}
}

// @Summary Create an invite
// @Description Generates a new invite code for a circle. Circle owner only.
// @Tags Invites
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Param body body invite.GenerateInput true "Invite settings (maxUses, expiresInDays)"
// @Success 201 {object} response.Envelope{data=object{invite=object}}
// @Failure 400 {object} response.Envelope
// @Router /circles/{id}/invites [post]
func (h *InviteHandler) CreateInvite(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var input invite.GenerateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	input.CircleID = circleID
	input.UserID = userID
	inv, err := h.inviteService.Generate(c.Request.Context(), input)
	if err != nil {
		response.InternalError(c, "failed to create invite")
		return
	}
	response.Created(c, gin.H{"invite": inv})
}

// @Summary List circle invites
// @Description Lists all invite codes for a circle. Circle owner only.
// @Tags Invites
// @Produce json
// @Security BearerAuth
// @Param id path string true "Circle ID"
// @Success 200 {object} response.Envelope{data=object{invites=array}}
// @Failure 500 {object} response.Envelope
// @Router /circles/{id}/invites [get]
func (h *InviteHandler) ListInvites(c *gin.Context) {
	circleID := c.Param("id")
	invites, err := h.inviteService.List(c.Request.Context(), circleID)
	if err != nil {
		response.InternalError(c, "failed to list invites")
		return
	}
	response.OK(c, gin.H{"invites": invites})
}

// @Summary Revoke an invite
// @Description Revokes an invite code so it can no longer be used.
// @Tags Invites
// @Produce json
// @Security BearerAuth
// @Param code path string true "Invite code"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 400 {object} response.Envelope
// @Router /invites/{code} [delete]
func (h *InviteHandler) RevokeInvite(c *gin.Context) {
	code := c.Param("code")
	userID := middleware.GetUserID(c)
	if err := h.inviteService.Revoke(c.Request.Context(), code, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}
