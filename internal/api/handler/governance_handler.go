package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/governance"
	"github.com/moistello/backend/pkg/response"
)

type GovernanceHandler struct {
	service governance.Service
}

func NewGovernanceHandler(service governance.Service) *GovernanceHandler {
	return &GovernanceHandler{service: service}
}

func (h *GovernanceHandler) CreateProposal(c *gin.Context) {
	var input governance.CreateProposalInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if input.CreatorID == "" {
		input.CreatorID = middleware.GetUserID(c)
	}
	proposal, err := h.service.CreateProposal(c.Request.Context(), input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, gin.H{"proposal": proposalResponse(proposal)})
}

// proposalResponse wraps a proposal with the voting-weight rule that governs
// it (#418), so a client reading a proposal can see the rule rather than having
// to infer it from the numbers.
func proposalResponse(p *governance.Proposal) gin.H {
	return gin.H{
		"id":                  p.ID,
		"title":               p.Title,
		"description":         p.Description,
		"proposalType":        p.ProposalType,
		"creatorId":           p.CreatorID,
		"status":              p.Status,
		"forVotes":            p.ForVotes,
		"againstVotes":        p.AgainstVotes,
		"forWeight":           p.ForWeight,
		"againstWeight":       p.AgainstWeight,
		"snapshotAt":          p.SnapshotAt,
		"snapshotTotalWeight": p.SnapshotTotalWeight,
		"executedAt":          p.ExecutedAt,
		"createdAt":           p.CreatedAt,
		"updatedAt":           p.UpdatedAt,
		// The rule itself, plus the per-voter snapshot, so a reader can verify
		// exactly what weight each vote was cast against (#418).
		"weightSnapshotRule": governance.SnapshotRule,
		"weightSnapshot":     p.WeightSnapshot,
	}
}

func (h *GovernanceHandler) ListProposals(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	proposals, total, err := h.service.ListProposals(c.Request.Context(), page, limit)
	if err != nil {
		response.InternalError(c, "failed to list proposals")
		return
	}
	// The rule is included once at the top level rather than per proposal, since
	// it is identical for every proposal in the list (#418).
	responses := make([]gin.H, 0, len(proposals))
	for i := range proposals {
		responses = append(responses, proposalResponse(&proposals[i]))
	}
	response.OK(c, gin.H{
		"proposals":         responses,
		"total":             total,
		"page":              page,
		"limit":             limit,
		"weightSnapshotRule": governance.SnapshotRule,
	})
}

func (h *GovernanceHandler) GetProposal(c *gin.Context) {
	proposal, err := h.service.GetProposal(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.NotFound(c, "proposal not found")
		return
	}
	response.OK(c, gin.H{"proposal": proposalResponse(proposal)})
}

func (h *GovernanceHandler) VoteProposal(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var input governance.VoteProposalInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.service.VoteProposal(c.Request.Context(), c.Param("id"), userID, input.Vote); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *GovernanceHandler) ExecuteProposal(c *gin.Context) {
	if err := h.service.ExecuteProposal(c.Request.Context(), c.Param("id")); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *GovernanceHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
