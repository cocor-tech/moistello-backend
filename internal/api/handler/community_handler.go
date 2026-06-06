package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/community"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
	"github.com/moistello/backend/pkg/validator"
)

type CommunityHandler struct {
	communitySvc community.Service
}

func NewCommunityHandler(svc community.Service) *CommunityHandler {
	return &CommunityHandler{communitySvc: svc}
}

func (h *CommunityHandler) Create(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var input community.CreateCommunityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validator.Validate.Struct(input); err != nil {
		response.ValidationErrors(c, err.Error())
		return
	}
	com, err := h.communitySvc.Create(c.Request.Context(), userID, input)
	if err != nil {
		response.InternalError(c, "failed to create community: "+err.Error())
		return
	}
	response.Created(c, gin.H{"community": com})
}

func (h *CommunityHandler) Get(c *gin.Context) {
	id := c.Param("id")
	com, err := h.communitySvc.Get(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "community not found")
		return
	}
	response.OK(c, gin.H{"community": com})
}

func (h *CommunityHandler) GetBySlug(c *gin.Context) {
	slug := c.Param("slug")
	com, err := h.communitySvc.GetBySlug(c.Request.Context(), slug)
	if err != nil {
		response.NotFound(c, "community not found")
		return
	}
	response.OK(c, gin.H{"community": com})
}

func (h *CommunityHandler) List(c *gin.Context) {
	page, limit, _ := pagination.Parse(c)
	filter := community.CommunityFilter{
		Search:   c.Query("search"),
		Category: c.Query("category"),
		Page:     page,
		Limit:    limit,
	}
	communities, total, err := h.communitySvc.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "failed to list communities")
		return
	}
	response.OKWithMeta(c, gin.H{"communities": communities}, response.NewPaginationMeta(page, limit, total))
}

func (h *CommunityHandler) Update(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	var input community.UpdateCommunityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	com, err := h.communitySvc.Update(c.Request.Context(), id, userID, input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"community": com})
}

func (h *CommunityHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.communitySvc.Delete(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) Join(c *gin.Context) {
	communityID := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.communitySvc.Join(c.Request.Context(), communityID, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) Leave(c *gin.Context) {
	communityID := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.communitySvc.Leave(c.Request.Context(), communityID, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) GetMembers(c *gin.Context) {
	communityID := c.Param("id")
	members, err := h.communitySvc.GetMembers(c.Request.Context(), communityID)
	if err != nil {
		response.InternalError(c, "failed to get members")
		return
	}
	response.OK(c, gin.H{"members": members})
}

func (h *CommunityHandler) IsMember(c *gin.Context) {
	communityID := c.Param("id")
	userID := middleware.GetUserID(c)
	isMember, err := h.communitySvc.IsMember(c.Request.Context(), communityID, userID)
	if err != nil {
		response.InternalError(c, "failed to check membership")
		return
	}
	response.OK(c, gin.H{"isMember": isMember})
}

func (h *CommunityHandler) CreateAnnouncement(c *gin.Context) {
	communityID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	a, err := h.communitySvc.CreateAnnouncement(c.Request.Context(), communityID, userID, req.Content)
	if err != nil {
		response.InternalError(c, "failed to create announcement")
		return
	}
	response.Created(c, gin.H{"announcement": a})
}

func (h *CommunityHandler) GetAnnouncements(c *gin.Context) {
	communityID := c.Param("id")
	announcements, err := h.communitySvc.GetAnnouncements(c.Request.Context(), communityID)
	if err != nil {
		response.InternalError(c, "failed to get announcements")
		return
	}
	response.OK(c, gin.H{"announcements": announcements})
}

func (h *CommunityHandler) DeleteAnnouncement(c *gin.Context) {
	id := c.Param("announcementId")
	userID := middleware.GetUserID(c)
	if err := h.communitySvc.DeleteAnnouncement(c.Request.Context(), id, userID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) LikeAnnouncement(c *gin.Context) {
	id := c.Param("announcementId")
	if err := h.communitySvc.LikeAnnouncement(c.Request.Context(), id); err != nil {
		response.InternalError(c, "failed to like announcement")
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) PinAnnouncement(c *gin.Context) {
	id := c.Param("announcementId")
	userID := middleware.GetUserID(c)
	var req struct {
		Pinned bool `json:"pinned"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.communitySvc.PinAnnouncement(c.Request.Context(), id, userID, req.Pinned); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) RemoveMember(c *gin.Context) {
	communityID := c.Param("id")
	userID := middleware.GetUserID(c)
	targetID := c.Param("memberId")
	if err := h.communitySvc.RemoveMember(c.Request.Context(), communityID, userID, targetID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) TransferOwnership(c *gin.Context) {
	communityID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		NewOwnerID string `json:"newOwnerId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.communitySvc.TransferOwnership(c.Request.Context(), communityID, userID, req.NewOwnerID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *CommunityHandler) GetActivity(c *gin.Context) {
	communityID := c.Param("id")
	events, err := h.communitySvc.GetActivity(c.Request.Context(), communityID, 50)
	if err != nil {
		response.InternalError(c, "failed to get activity")
		return
	}
	response.OK(c, gin.H{"events": events})
}

func (h *CommunityHandler) GetMyCommunities(c *gin.Context) {
	userID := middleware.GetUserID(c)
	communities, err := h.communitySvc.GetMyCommunities(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to get communities")
		return
	}
	response.OK(c, gin.H{"communities": communities})
}
