package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
)

type NotificationHandler struct {
	notificationService notification.Service
}

func NewNotificationHandler(svc notification.Service) *NotificationHandler {
	return &NotificationHandler{notificationService: svc}
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
// @Description Updates the authenticated user's notification channel preferences and mute status.
// @Tags Notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object{channels=array,muted=bool} true "Preferences"
// @Success 200 {object} response.Envelope{data=object{preferences=object}}
// @Failure 400 {object} response.Envelope
// @Router /notifications/preferences [put]
func (h *NotificationHandler) UpdatePreferences(c *gin.Context) {
	var req struct {
		Channels []string `json:"channels"`
		Muted    bool     `json:"muted"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"preferences": gin.H{"channels": req.Channels, "muted": req.Muted}})
}
