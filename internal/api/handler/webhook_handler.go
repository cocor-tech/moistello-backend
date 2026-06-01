package handler

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/pkg/response"
)

type webhookRecord struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Events []string `json:"events"`
	Active bool   `json:"active"`
	UserID string `json:"userId"`
}

type WebhookHandler struct {
	mu       sync.RWMutex
	webhooks map[string]webhookRecord
}

func NewWebhookHandler() *WebhookHandler {
	return &WebhookHandler{
		webhooks: make(map[string]webhookRecord),
	}
}

// @Summary Register a webhook
// @Description Registers a new webhook endpoint to receive event notifications.
// @Tags Webhooks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object{url=string,events=array} true "Webhook config"
// @Success 201 {object} response.Envelope{data=object{webhook=object}}
// @Failure 400 {object} response.Envelope
// @Router /webhooks [post]
func (h *WebhookHandler) RegisterWebhook(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		URL    string   `json:"url" binding:"required"`
		Events []string `json:"events" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	id := uuid.New().String()
	record := webhookRecord{
		ID:     id,
		URL:    req.URL,
		Events: req.Events,
		Active: true,
		UserID: userID,
	}
	h.mu.Lock()
	h.webhooks[id] = record
	h.mu.Unlock()
	response.Created(c, gin.H{"webhook": record})
}

// @Summary List webhooks
// @Description Lists all registered webhooks for the authenticated user.
// @Tags Webhooks
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{webhooks=array}}
// @Router /webhooks [get]
func (h *WebhookHandler) ListWebhooks(c *gin.Context) {
	userID := middleware.GetUserID(c)
	h.mu.RLock()
	var result []webhookRecord
	for _, wh := range h.webhooks {
		if wh.UserID == userID {
			result = append(result, wh)
		}
	}
	h.mu.RUnlock()
	if result == nil {
		result = []webhookRecord{}
	}
	response.OK(c, gin.H{"webhooks": result})
}

// @Summary Delete a webhook
// @Description Deletes a registered webhook by ID.
// @Tags Webhooks
// @Produce json
// @Security BearerAuth
// @Param id path string true "Webhook ID"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 404 {object} response.Envelope
// @Router /webhooks/{id} [delete]
func (h *WebhookHandler) DeleteWebhook(c *gin.Context) {
	id := c.Param("id")
	userID := middleware.GetUserID(c)
	h.mu.Lock()
	wh, exists := h.webhooks[id]
	if !exists || wh.UserID != userID {
		h.mu.Unlock()
		response.NotFound(c, "webhook not found")
		return
	}
	delete(h.webhooks, id)
	h.mu.Unlock()
	response.OK(c, gin.H{"success": true})
}
