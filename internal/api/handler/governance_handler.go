package handler

import (
	"net/http"

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
	response.Created(c, gin.H{"proposal": proposal})
}

func (h *GovernanceHandler) ListProposals(c *gin.Context) {
	proposals, err := h.service.ListProposals(c.Request.Context())
	if err != nil {
		response.InternalError(c, "failed to list proposals")
		return
	}
	response.OK(c, gin.H{"proposals": proposals})
}

func (h *GovernanceHandler) GetProposal(c *gin.Context) {
	proposal, err := h.service.GetProposal(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.NotFound(c, "proposal not found")
		return
	}
	response.OK(c, gin.H{"proposal": proposal})
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
