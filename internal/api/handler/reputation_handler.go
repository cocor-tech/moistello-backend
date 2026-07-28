package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/pkg/response"
)

// Tier defines a MoiScore tier with its score range and associated limits.
type Tier struct {
	Name        string `json:"name"`
	MinScore    int    `json:"minScore"`
	MaxScore    int    `json:"maxScore"`
	MaxCircles  int    `json:"maxCircles"`
	MaxMonthly  int    `json:"maxMonthlyUSD"`
	Description string `json:"description"`
}

// allTiers is the canonical ordered list of MoiScore tiers (Bronze → Diamond).
var allTiers = []Tier{
	{
		Name:        "Bronze",
		MinScore:    0,
		MaxScore:    200,
		MaxCircles:  2,
		MaxMonthly:  500,
		Description: "Entry tier. Up to 2 active circles and $500/month in contributions.",
	},
	{
		Name:        "Silver",
		MinScore:    201,
		MaxScore:    400,
		MaxCircles:  5,
		MaxMonthly:  2000,
		Description: "Active participant. Up to 5 circles and $2,000/month.",
	},
	{
		Name:        "Gold",
		MinScore:    401,
		MaxScore:    600,
		MaxCircles:  10,
		MaxMonthly:  10000,
		Description: "Trusted member. Up to 10 circles and $10,000/month.",
	},
	{
		Name:        "Platinum",
		MinScore:    601,
		MaxScore:    800,
		MaxCircles:  20,
		MaxMonthly:  50000,
		Description: "High-trust member. Up to 20 circles and $50,000/month.",
	},
	{
		Name:        "Diamond",
		MinScore:    801,
		MaxScore:    1000,
		MaxCircles:  50,
		MaxMonthly:  250000,
		Description: "Elite tier. Up to 50 circles and $250,000/month.",
	},
}

func tierForScore(score int) Tier {
	for i := len(allTiers) - 1; i >= 0; i-- {
		if score >= allTiers[i].MinScore {
			return allTiers[i]
		}
	}
	return allTiers[0]
}

// ReputationHandler exposes MoiScore tier endpoints.
type ReputationHandler struct {
	service reputation.Service
}

func NewReputationHandler(svc reputation.Service) *ReputationHandler {
	return &ReputationHandler{service: svc}
}

// GetTiers returns the full list of MoiScore tiers, their score ranges, and per-tier limits.
// @Summary List MoiScore tiers
// @Description Returns all available MoiScore tiers with requirements and limits.
// @Tags Reputation
// @Produce json
// @Success 200 {object} response.Envelope{data=object{tiers=array}}
// @Router /reputation/tiers [get]
func (h *ReputationHandler) GetTiers(c *gin.Context) {
	response.OK(c, gin.H{"tiers": allTiers})
}

// GetTierByAddress returns the current MoiScore tier for a given user address (user ID).
// @Summary Get tier for an address
// @Description Returns the MoiScore, current tier, and limits for the given user address.
// @Tags Reputation
// @Produce json
// @Security BearerAuth
// @Param address path string true "User ID (wallet address)"
// @Success 200 {object} response.Envelope{data=object{score=int,tier=object}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /reputation/tier/{address} [get]
func (h *ReputationHandler) GetTierByAddress(c *gin.Context) {
	address := c.Param("address")
	if address == "" {
		address = middleware.GetUserID(c)
	}

	// CalculateScore with zero parameters fetches the latest stored snapshot.
	// If no snapshot exists the service returns a fresh Bronze-level result.
	snapshot, err := h.service.CalculateScore(c.Request.Context(), address, 0, 0, 0, 0)
	if err != nil {
		response.NotFound(c, "reputation data not found for address")
		return
	}

	tier := tierForScore(snapshot.Score)
	response.OK(c, gin.H{
		"address":  address,
		"score":    snapshot.Score,
		"level":    snapshot.Level,
		"tier":     tier,
		"breakdown": snapshot.Breakdown,
	})
}
