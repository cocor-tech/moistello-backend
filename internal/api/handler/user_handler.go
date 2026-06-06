package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

type UserHandler struct {
	userService user.Service
}

func NewUserHandler(svc user.Service) *UserHandler {
	return &UserHandler{userService: svc}
}

// @Summary Get my profile
// @Description Returns the authenticated user's profile.
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 404 {object} response.Envelope
// @Router /users/me [get]
func (h *UserHandler) GetMe(c *gin.Context) {
	userID := middleware.GetUserID(c)
	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}
	response.OK(c, gin.H{"user": u})
}

// @Summary Update my profile
// @Description Updates the authenticated user's profile fields (displayName, email, countryCode, preferredLanguage).
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body user.UpdateProfileInput true "Profile updates"
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 400 {object} response.Envelope
// @Router /users/me [patch]
func (h *UserHandler) UpdateMe(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var updates user.UpdateProfileInput
	if err := c.ShouldBindJSON(&updates); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	u, err := h.userService.UpdateProfile(c.Request.Context(), userID, updates)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"user": u})
}

// @Summary Get user by ID
// @Description Returns a user's public profile by wallet address or user ID.
// @Tags Users
// @Produce json
// @Param id path string true "User ID or wallet address"
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 404 {object} response.Envelope
// @Router /users/{id} [get]
func (h *UserHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	u, err := h.userService.GetByID(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}
	response.OK(c, gin.H{"user": u})
}

// @Summary Get my reputation score
// @Description Returns the authenticated user's MoiScore reputation.
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{reputation=number}}
// @Failure 500 {object} response.Envelope
// @Router /users/me/reputation [get]
func (h *UserHandler) GetReputation(c *gin.Context) {
	userID := middleware.GetUserID(c)
	score, err := h.userService.GetMoiScore(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to get reputation score")
		return
	}
	response.OK(c, gin.H{"reputation": score})
}

// @Summary Get my circles
// @Description Returns all circles the authenticated user belongs to.
// @Tags Users
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{circles=array}}
// @Failure 500 {object} response.Envelope
// @Router /users/me/circles [get]
func (h *UserHandler) ClaimName(c *gin.Context) {
	name, err := h.userService.ClaimName(c.Request.Context())
	if err != nil {
		response.InternalError(c, "failed to generate name")
		return
	}
	response.OK(c, gin.H{"name": name})
}

func (h *UserHandler) GetMyCircles(c *gin.Context) {
	userID := middleware.GetUserID(c)
	circles, err := h.userService.GetCircles(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to get circles")
		return
	}
	response.OK(c, gin.H{"circles": circles})
}
