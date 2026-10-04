package handler

import (
	"errors"
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

// proposalResponse carries both the timelock fields (#414) and the snapshotted
// voting weight (#418) on a proposal payload: a client can render the execution
// countdown, see when the cancellation window closes, and verify exactly what
// weight each vote was counted against, without having to infer any of it.
func proposalResponse(p *governance.Proposal) gin.H {
	return gin.H{
		"id":                p.ID,
		"title":             p.Title,
		"description":       p.Description,
		"proposalType":      p.ProposalType,
		"creatorId":         p.CreatorID,
		"status":            p.Status,
		"forVotes":          p.ForVotes,
		"againstVotes":      p.AgainstVotes,
		"forWeight":         p.ForWeight,
		"againstWeight":     p.AgainstWeight,
		"executedAt":        p.ExecutedAt,
		"executableAt":      p.ExecutableAt,
		"cancelledAt":       p.CancelledAt,
		"cancellationVotes": p.CancellationVotes,
		"createdAt":         p.CreatedAt,
		"updatedAt":         p.UpdatedAt,
		// Timelock: the rule itself plus the window it opened (#414).
		"timelockRule": governance.TimelockRule,
		// Snapshot: the rule itself, plus the per-voter snapshot, so a reader can
		// verify exactly what weight each vote was cast against (#418).
		"weightSnapshotRule":  governance.SnapshotRule,
		"snapshotAt":          p.SnapshotAt,
		"snapshotTotalWeight": p.SnapshotTotalWeight,
		"weightSnapshot":      p.WeightSnapshot,
	}
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

func (h *GovernanceHandler) ListProposals(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	proposals, total, err := h.service.ListProposals(c.Request.Context(), page, limit)
	if err != nil {
		response.InternalError(c, "failed to list proposals")
		return
	}
	// Both rules are stated once at the top level rather than per proposal,
	// since they are identical for every proposal in the list (#414, #418).
	responses := make([]gin.H, 0, len(proposals))
	for i := range proposals {
		responses = append(responses, proposalResponse(&proposals[i]))
	}
	response.OK(c, gin.H{
		"proposals":          responses,
		"total":              total,
		"page":               page,
		"limit":              limit,
		"weightSnapshotRule": governance.SnapshotRule,
		"timelockRule":       governance.TimelockRule,
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

// ExecuteProposal advances a proposal toward execution. It is two-phase
// (#414): the first call evaluates the vote and, if it carried, starts the
// timelock rather than executing. A second call once executableAt has passed
// performs the execution.
//
// A call that arrives inside the timelock is answered 409 with executable_at so
// the client can show a countdown rather than treating it as a failure.
func (h *GovernanceHandler) ExecuteProposal(c *gin.Context) {
	id := c.Param("id")
	if err := h.service.ExecuteProposal(c.Request.Context(), id); err != nil {
		if errors.Is(err, governance.ErrTimelockActive) {
			h.respondTimelock(c, id)
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	// Re-read so the response reports what actually happened: the proposal may
	// now be executed, rejected, or sitting in its timelock.
	proposal, err := h.service.GetProposal(c.Request.Context(), id)
	if err != nil {
		response.OK(c, gin.H{"success": true})
		return
	}
	response.OK(c, gin.H{
		"success":  true,
		"proposal": proposalResponse(proposal),
	})
}

// respondTimelock answers a too-early execute with the countdown data.
func (h *GovernanceHandler) respondTimelock(c *gin.Context, id string) {
	proposal, err := h.service.GetProposal(c.Request.Context(), id)
	if err != nil {
		response.Conflict(c, governance.ErrTimelockActive.Error())
		return
	}
	c.JSON(http.StatusConflict, gin.H{
		"success":      false,
		"error":        governance.ErrTimelockActive.Error(),
		"executableAt": proposal.ExecutableAt,
		"proposal":     proposalResponse(proposal),
	})
}

// CancelProposal records the authenticated member's vote to cancel a proposal
// inside its timelock window (#414). The response says whether this call is
// the one that cancelled it, or merely counted toward the threshold.
func (h *GovernanceHandler) CancelProposal(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id := c.Param("id")

	cancelled, err := h.service.CancelProposal(c.Request.Context(), id, userID)
	if err != nil {
		if errors.Is(err, governance.ErrNotCancellable) {
			response.Conflict(c, err.Error())
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	proposal, getErr := h.service.GetProposal(c.Request.Context(), id)
	if getErr != nil {
		response.OK(c, gin.H{"success": true, "cancelled": cancelled})
		return
	}
	response.OK(c, gin.H{
		"success":   true,
		"cancelled": cancelled,
		"proposal":  proposalResponse(proposal),
	})
}

func (h *GovernanceHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
