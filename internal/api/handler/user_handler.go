package handler

import (
	"github.com/gin-gonic/gin"
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
