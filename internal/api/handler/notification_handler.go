package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
	"strings"
)

type NotificationHandler struct {
	notificationService notification.Service
	userService         user.Service
}

func NewNotificationHandler(svc notification.Service, userSvc user.Service) *NotificationHandler {
	return &NotificationHandler{notificationService: svc, userService: userSvc}
}

// @Summary List notifications
// @Description Returns paginated notifications for the authenticated user. Use ?unread=true to filter unread only.
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param unread query bool false "Filter unread only"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{notifications=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /notifications [get]
func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	userID := middleware.GetUserID(c)
	unreadOnly := c.Query("unread") == "true"
	page, limit, _ := pagination.Parse(c)
	notifications, total, err := h.notificationService.List(c.Request.Context(), userID, page, limit, unreadOnly)
	if err != nil {
		response.InternalError(c, "failed to list notifications")
		return
	}
	response.OKWithMeta(c, gin.H{"notifications": notifications}, response.NewPaginationMeta(page, limit, total))
}

// @Summary Search notifications
// @Description Returns paginated notifications filtered by free text, type, read and archive state.
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param q query string false "Case-insensitive text match on title or body"
// @Param type query string false "Notification type, e.g. contribution.due"
// @Param unread query bool false "Only unread notifications"
// @Param archived query bool false "Include archived notifications"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{notifications=array},meta=response.PaginationMeta}
// @Failure 400 {object} response.Envelope
// @Failure 500 {object} response.Envelope
// @Router /notifications/search [get]
func (h *NotificationHandler) SearchNotifications(c *gin.Context) {
	filter := notification.SearchFilter{
		Query:           strings.TrimSpace(c.Query("q")),
		Type:            notification.NotificationType(c.Query("type")),
		UnreadOnly:      c.Query("unread") == "true",
		IncludeArchived: c.Query("archived") == "true",
	}
	if len(filter.Query) > notification.MaxSearchQueryLength {
		response.BadRequest(c, "search query is too long")
		return
	}
	page, limit, _ := pagination.Parse(c)
	notifications, total, err := h.notificationService.Search(c.Request.Context(), middleware.GetUserID(c), filter, page, limit)
	if err != nil {
		response.InternalError(c, "failed to search notifications")
		return
	}
	response.OKWithMeta(c, gin.H{"notifications": notifications}, response.NewPaginationMeta(page, limit, total))
}

// @Summary Mark notification as read
// @Description Marks a single notification as read.
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Param id path string true "Notification ID"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 500 {object} response.Envelope
// @Router /notifications/{id}/read [patch]
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	if err := h.notificationService.MarkRead(c.Request.Context(), id, userID); err != nil {
		response.InternalError(c, "failed to mark notification as read")
		return
	}
	response.OK(c, gin.H{"success": true})
}

// @Summary Mark all notifications as read
// @Description Marks every unread notification for the authenticated user as read.
// @Tags Notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 500 {object} response.Envelope
// @Router /notifications/read-all [patch]
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if err := h.notificationService.MarkAllRead(c.Request.Context(), userID); err != nil {
		response.InternalError(c, "failed to mark all notifications as read")
		return
	}
	response.OK(c, gin.H{"success": true})
}

// @Summary Update notification preferences
// @Description Persists the authenticated user's notification channel preferences and mute status.
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body user.NotificationPrefsInput true "Preferences (channels: [\"inapp\",\"email\",\"sms\",\"push\"], muted: bool, digestEnabled: bool, digestIntervalMinutes: int)"
// @Success 200 {object} response.Envelope{data=object{preferences=object}}
// @Failure 400 {object} response.Envelope
// @Failure 500 {object} response.Envelope
// @Router /notifications/preferences [put]
func (h *NotificationHandler) UpdatePreferences(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req user.NotificationPrefsInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	u, err := h.userService.UpdateNotificationPreferences(c.Request.Context(), userID, req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	minCadence, maxCadence := user.NotificationDigestCadenceBounds()

	response.OK(c, gin.H{
		"preferences": gin.H{
			"channels": u.NotificationChannels,
			"muted":    u.NotificationsMuted,
			// Digest settings (#415). The cadence bounds are echoed back so a
			// client can validate input without hard-coding the window.
			"digestEnabled":         u.DigestEnabled,
			"digestIntervalMinutes": u.DigestIntervalMinutes,
			"digestCadenceRange": gin.H{
				"minMinutes": minCadence,
				"maxMinutes": maxCadence,
			},
		},
	})
}

type BulkArchiveRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

// @Summary Bulk archive notifications
// @Description Archives multiple notifications by ID for the authenticated user.
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body BulkArchiveRequest true "Notification IDs"
// @Success 200 {object} response.Envelope{data=object{updated=int,ids=array}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /notifications/bulk-archive [post]
func (h *NotificationHandler) BulkArchive(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "unauthorized")
		return
	}

	var req BulkArchiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updated, err := h.notificationService.BulkArchive(c.Request.Context(), userID, req.IDs, true)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.OK(c, gin.H{"updated": len(updated), "ids": updated})
}

// @Summary Bulk unarchive notifications
// @Description Unarchives multiple notifications by ID for the authenticated user.
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body BulkArchiveRequest true "Notification IDs"
// @Success 200 {object} response.Envelope{data=object{updated=int,ids=array}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /notifications/bulk-unarchive [post]
func (h *NotificationHandler) BulkUnarchive(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "unauthorized")
		return
	}

	var req BulkArchiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updated, err := h.notificationService.BulkArchive(c.Request.Context(), userID, req.IDs, false)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.OK(c, gin.H{"updated": len(updated), "ids": updated})
}
