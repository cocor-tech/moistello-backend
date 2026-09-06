package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/moistello/backend/internal/domain/incentives"
	"github.com/moistello/backend/pkg/response"
)

type IncentivesHandler struct {
	service incentives.Service
}

func NewIncentivesHandler(service incentives.Service) *IncentivesHandler {
	return &IncentivesHandler{service: service}
}

// GenerateReferralCode generates a unique referral code for the user
// @Summary Generate referral code
// @Description Generate a unique referral code for the authenticated user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=map[string]string}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/referral-code [post]
func (h *IncentivesHandler) GenerateReferralCode(c *gin.Context) {
	userID := c.GetString("user_id")

	code, err := h.service.GenerateReferralCode(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to generate referral code", err)
		return
	}

	response.Success(c, gin.H{"referralCode": code})
}

// ApplyReferralCode applies a referral code for the user
// @Summary Apply referral code
// @Description Apply a referral code to receive bonus
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body map[string]string true "referral code"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/apply-referral [post]
func (h *IncentivesHandler) ApplyReferralCode(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		Code string `json:"code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid request body", err)
		return
	}

	if err := h.service.ApplyReferralCode(c.Request.Context(), userID, req.Code); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to apply referral code", err)
		return
	}

	response.Success(c, gin.H{"message": "Referral code applied successfully"})
}

// GetReferrals returns the user's referral history
// @Summary Get referrals
// @Description Get the authenticated user's referral history
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=[]incentives.Referral}
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/referrals [get]
func (h *IncentivesHandler) GetReferrals(c *gin.Context) {
	userID := c.GetString("user_id")

	referrals, err := h.service.GetReferrals(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to get referrals", err)
		return
	}

	response.Success(c, referrals)
}

// GrantCircleCompletionReward grants a reward for completing a circle
// @Summary Grant circle completion reward
// @Description Grant a reward to a user for completing a circle (admin only)
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body map[string]string true "user ID and circle ID"
// @Success 200 {object} response.Response{data=incentives.Incentive}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 403 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/circle-completion [post]
func (h *IncentivesHandler) GrantCircleCompletionReward(c *gin.Context) {
	var req struct {
		UserID   string `json:"userId" binding:"required"`
		CircleID string `json:"circleId" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid request body", err)
		return
	}

	incentive, err := h.service.GrantCircleCompletionReward(c.Request.Context(), req.UserID, req.CircleID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to grant circle completion reward", err)
		return
	}

	response.Success(c, incentive)
}

// GrantContributionMatch grants a contribution match bonus
// @Summary Grant contribution match
// @Description Grant a contribution match bonus to a user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body map[string]interface{} true "user ID, circle ID, and amount"
// @Success 200 {object} response.Response{data=incentives.Incentive}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/contribution-match [post]
func (h *IncentivesHandler) GrantContributionMatch(c *gin.Context) {
	var req struct {
		UserID   string  `json:"userId" binding:"required"`
		CircleID string  `json:"circleId" binding:"required"`
		Amount   float64 `json:"amount" binding:"required,gt=0"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid request body", err)
		return
	}

	incentive, err := h.service.GrantContributionMatch(c.Request.Context(), req.UserID, req.CircleID, req.Amount)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to grant contribution match", err)
		return
	}

	response.Success(c, incentive)
}

// RecordContribution records a contribution for streak tracking
// @Summary Record contribution
// @Description Record a contribution to update savings streak
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=incentives.SavingsStreak}
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/contribution [post]
func (h *IncentivesHandler) RecordContribution(c *gin.Context) {
	userID := c.GetString("user_id")

	streak, err := h.service.RecordContribution(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to record contribution", err)
		return
	}

	response.Success(c, streak)
}

// GrantStreakBonus grants a streak bonus
// @Summary Grant streak bonus
// @Description Grant a savings streak bonus to the user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=incentives.Incentive}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/streak-bonus [post]
func (h *IncentivesHandler) GrantStreakBonus(c *gin.Context) {
	userID := c.GetString("user_id")

	incentive, err := h.service.GrantStreakBonus(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to grant streak bonus", err)
		return
	}

	response.Success(c, incentive)
}

// GrantFirstDepositBonus grants a first deposit bonus
// @Summary Grant first deposit bonus
// @Description Grant a first deposit bonus to a user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body map[string]interface{} true "user ID and deposit amount"
// @Success 200 {object} response.Response{data=incentives.Incentive}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/first-deposit [post]
func (h *IncentivesHandler) GrantFirstDepositBonus(c *gin.Context) {
	var req struct {
		UserID        string  `json:"userId" binding:"required"`
		DepositAmount float64 `json:"depositAmount" binding:"required,gt=0"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid request body", err)
		return
	}

	incentive, err := h.service.GrantFirstDepositBonus(c.Request.Context(), req.UserID, req.DepositAmount)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to grant first deposit bonus", err)
		return
	}

	response.Success(c, incentive)
}

// ClaimIncentive claims a pending incentive
// @Summary Claim incentive
// @Description Claim a pending incentive for the authenticated user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "incentive ID"
// @Success 200 {object} response.Response
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 404 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/{id}/claim [post]
func (h *IncentivesHandler) ClaimIncentive(c *gin.Context) {
	userID := c.GetString("user_id")
	incentiveID := c.Param("id")

	if _, err := uuid.Parse(incentiveID); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid incentive ID", err)
		return
	}

	if err := h.service.ClaimIncentive(c.Request.Context(), userID, incentiveID); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Failed to claim incentive", err)
		return
	}

	response.Success(c, gin.H{"message": "Incentive claimed successfully"})
}

// GetUserIncentives returns all incentives for the user
// @Summary Get user incentives
// @Description Get all incentives for the authenticated user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param status query string false "filter by status (pending, claimed, expired, cancelled)"
// @Success 200 {object} response.Response{data=[]incentives.Incentive}
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives [get]
func (h *IncentivesHandler) GetUserIncentives(c *gin.Context) {
	userID := c.GetString("user_id")

	userIncentives, err := h.service.GetUserIncentives(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to get incentives", err)
		return
	}

	// Filter by status if provided
	statusFilter := c.Query("status")
	if statusFilter != "" {
		filtered := make([]incentives.Incentive, 0)
		for _, inc := range userIncentives {
			if string(inc.Status) == statusFilter {
				filtered = append(filtered, inc)
			}
		}
		userIncentives = filtered
	}

	response.Success(c, userIncentives)
}

// GetPendingIncentives returns pending incentives for the user
// @Summary Get pending incentives
// @Description Get pending incentives for the authenticated user
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=[]incentives.Incentive}
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/pending [get]
func (h *IncentivesHandler) GetPendingIncentives(c *gin.Context) {
	userID := c.GetString("user_id")

	incentives, err := h.service.GetPendingIncentives(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to get pending incentives", err)
		return
	}

	response.Success(c, incentives)
}

// GetUserSummary returns a summary of user's incentives
// @Summary Get user incentive summary
// @Description Get a summary of the authenticated user's incentives
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=incentives.UserIncentiveSummary}
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/summary [get]
func (h *IncentivesHandler) GetUserSummary(c *gin.Context) {
	userID := c.GetString("user_id")

	summary, err := h.service.GetUserSummary(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to get user summary", err)
		return
	}

	response.Success(c, summary)
}

// GetConfig returns the current incentive configuration
// @Summary Get incentive config
// @Description Get the current incentive configuration (admin only)
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=incentives.IncentiveConfig}
// @Failure 401 {object} response.Response
// @Failure 403 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /admin/incentives/config [get]
func (h *IncentivesHandler) GetConfig(c *gin.Context) {
	config, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to get config", err)
		return
	}

	response.Success(c, config)
}

// UpdateConfig updates the incentive configuration
// @Summary Update incentive config
// @Description Update the incentive configuration (admin only)
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param config body incentives.IncentiveConfig true "incentive configuration"
// @Success 200 {object} response.Response{data=incentives.IncentiveConfig}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 403 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /admin/incentives/config [put]
func (h *IncentivesHandler) UpdateConfig(c *gin.Context) {
	var config incentives.IncentiveConfig

	if err := c.ShouldBindJSON(&config); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid request body", err)
		return
	}

	if err := h.service.UpdateConfig(c.Request.Context(), &config); err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to update config", err)
		return
	}

	response.Success(c, config)
}

// CalculateContributionMatch calculates the contribution match amount
// @Summary Calculate contribution match
// @Description Calculate the contribution match amount for a given deposit
// @Tags incentives
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body map[string]interface{} true "user ID and amount"
// @Success 200 {object} response.Response{data=map[string]float64}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /incentives/calculate-match [post]
func (h *IncentivesHandler) CalculateContributionMatch(c *gin.Context) {
	var req struct {
		UserID string  `json:"userId" binding:"required"`
		Amount float64 `json:"amount" binding:"required,gt=0"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "ERROR", "Invalid request body", err)
		return
	}

	matchAmount, err := h.service.CalculateContributionMatch(c.Request.Context(), req.UserID, req.Amount)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "ERROR", "Failed to calculate match", err)
		return
	}

	response.Success(c, gin.H{"matchAmount": matchAmount})
}
