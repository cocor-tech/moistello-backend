package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

type UserHandler struct {
	userService user.Service
}

func NewUserHandler(userService user.Service) *UserHandler {
	return &UserHandler{userService: userService}
}

// @Summary Claim username
// @Description Claims a username/handle for the authenticated user (RESTful: POST /v1/users/username/claim)
// @Tags User
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope
// @Router /users/username/claim [post]
func (h *UserHandler) ClaimName(c *gin.Context) {
	_, err := h.userService.ClaimName(c.Request.Context())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true, "message": "username claimed successfully"})
}

// @Summary Update profile
// @Description Updates profile fields for the authenticated user
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body user.UpdateProfileInput true "Profile updates"
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /users/me [patch]
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "unauthorized")
		return
	}

	var req user.UpdateProfileInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Email != nil {
		response.BadRequest(c, "use the verified email-change flow")
		return
	}

	u, err := h.userService.UpdateProfile(c.Request.Context(), userID, req)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			response.NotFound(c, "user not found")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	response.OK(c, gin.H{"user": u})
}
