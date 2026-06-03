package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

type VerificationHandler struct {
	verificationSvc *auth.VerificationService
	userSvc         user.Service
}

func NewVerificationHandler(svc *auth.VerificationService, userSvc user.Service) *VerificationHandler {
	return &VerificationHandler{verificationSvc: svc, userSvc: userSvc}
}

func (h *VerificationHandler) SendCode(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "A valid email is required.")
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Check for duplicate email before sending code
	if h.userSvc != nil {
		taken, err := h.userSvc.IsEmailTaken(c.Request.Context(), req.Email)
		if err != nil {
			response.InternalError(c, "failed to check email availability")
			return
		}
		if taken {
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "This email is already registered. Try logging in instead."})
			return
		}
	}

	result, err := h.verificationSvc.SendCode(c.Request.Context(), req.Email)
	if err != nil {
		var rateErr *auth.RateLimitError
		if errors.As(err, &rateErr) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success":    false,
				"error":      rateErr.Message,
				"retryAfter": rateErr.RetryAfter.Seconds(),
			})
			return
		}
		response.InternalError(c, "Failed to send verification code.")
		return
	}

	response.OK(c, result)
}

func (h *VerificationHandler) VerifyCode(c *gin.Context) {
	var req struct {
		VerificationID string `json:"verificationId" binding:"required,uuid"`
		Code           string `json:"code" binding:"required,len=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "A valid verificationId and 6-digit code are required.")
		return
	}

	result, err := h.verificationSvc.VerifyCode(c.Request.Context(), req.VerificationID, req.Code)
	if err != nil {
		var verifyErr *auth.VerifyError
		if errors.As(err, &verifyErr) {
			status := verifyErr.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{
				"success":   false,
				"error":     verifyErr.Message,
				"remaining": verifyErr.Remaining,
			})
			return
		}
		response.InternalError(c, "Failed to verify code.")
		return
	}

	response.OK(c, result)
}

func (h *VerificationHandler) ResendCode(c *gin.Context) {
	var req struct {
		VerificationID string `json:"verificationId" binding:"required,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "A valid verificationId is required.")
		return
	}

	result, err := h.verificationSvc.ResendCode(c.Request.Context(), req.VerificationID)
	if err != nil {
		var rateErr *auth.RateLimitError
		if errors.As(err, &rateErr) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success":    false,
				"error":      rateErr.Message,
				"retryAfter": rateErr.RetryAfter.Seconds(),
			})
			return
		}
		var verifyErr *auth.VerifyError
		if errors.As(err, &verifyErr) {
			c.JSON(verifyErr.StatusCode, gin.H{
				"success": false,
				"error":   verifyErr.Message,
			})
			return
		}
		response.InternalError(c, "Failed to resend code.")
		return
	}

	response.OK(c, result)
}
