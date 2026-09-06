package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/pkg/pagination"
	"github.com/moistello/backend/pkg/response"
	"github.com/moistello/backend/webhook"
)

type WebhookHandler struct {
	repo webhook.WebhookRepository
}

func NewWebhookHandler(repo webhook.WebhookRepository) *WebhookHandler {
	return &WebhookHandler{repo: repo}
}

// @Summary Register a webhook
// @Description Registers a new webhook endpoint to receive event notifications. The events array controls which event types are delivered; an empty array subscribes to all events.
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
		Events []string `json:"events"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(req.Events) == 0 {
		response.BadRequest(c, "events must not be empty; subscribe to at least one event type")
		return
	}

	secret, err := newWebhookSecret()
	if err != nil {
		response.InternalError(c, "failed to generate webhook secret")
		return
	}

	// compute secret hash (sha256 hex) and persist only the hash
	// compute SHA256 hex of secret for storage
	sum := sha256.Sum256([]byte(secret))
	hsh := hex.EncodeToString(sum[:])
	record := &webhook.WebhookRegistration{
		ID:         uuid.New().String(),
		UserID:     userID,
		TargetURL:  req.URL,
		Secret:     secret, // in-memory only
		SecretHash: hsh,
		Events:     req.Events,
		IsActive:   true,
	}
	// The secret must never be persisted — clear it before handing the record
	// to the repository so only secret_hash is stored.
	record.Secret = ""
	if err := h.repo.Register(c.Request.Context(), record); err != nil {
		response.InternalError(c, "failed to register webhook")
		return
	}
	// Return the plaintext secret to the client exactly once at registration.
	// The secret is NOT persisted; only its SHA-256 hash (secret_hash) is stored.
	// Clients must save the secret now — it cannot be retrieved later.
	response.Created(c, gin.H{
		"webhook": record,
		"secret":  secret,
	})
}

// @Summary List webhooks
// @Description Lists all registered webhooks for the authenticated user.
// @Tags Webhooks
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{webhooks:array}}
// @Router /webhooks [get]
func (h *WebhookHandler) ListWebhooks(c *gin.Context) {
	userID := middleware.GetUserID(c)
	webhooks, err := h.repo.GetByUserID(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to list webhooks")
		return
	}
	if webhooks == nil {
		webhooks = []webhook.WebhookRegistration{}
	}
	response.OK(c, gin.H{"webhooks": webhooks})
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
	wh, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil || wh == nil {
		response.NotFound(c, "webhook not found")
		return
	}
	userID := middleware.GetUserID(c)
	if wh.UserID != userID {
		response.NotFound(c, "webhook not found")
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		response.InternalError(c, "failed to delete webhook")
		return
	}
	response.OK(c, gin.H{"success": true})
}

// @Summary List webhook deliveries
// @Description Returns a paginated delivery history (attempts, statuses, errors) for a webhook owned by the authenticated user.
// @Tags Webhooks
// @Produce json
// @Security BearerAuth
// @Param id path string true "Webhook ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{deliveries=array},meta=response.PaginationMeta}
// @Failure 404 {object} response.Envelope
// @Router /webhooks/{id}/deliveries [get]
func (h *WebhookHandler) ListDeliveries(c *gin.Context) {
	id := c.Param("id")
	wh, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil || wh == nil {
		response.NotFound(c, "webhook not found")
		return
	}
	userID := middleware.GetUserID(c)
	if wh.UserID != userID {
		response.NotFound(c, "webhook not found")
		return
	}

	page, limit, _ := pagination.Parse(c)
	deliveries, total, err := h.repo.ListDeliveries(c.Request.Context(), id, page, limit)
	if err != nil {
		response.InternalError(c, "failed to list webhook deliveries")
		return
	}
	if deliveries == nil {
		deliveries = []webhook.DeliveryLog{}
	}
	response.OKWithMeta(c, gin.H{"deliveries": deliveries}, response.NewPaginationMeta(page, limit, total))
}

// IncomingWebhookHandler receives and verifies incoming webhook deliveries.
type IncomingWebhookHandler struct {
	repo webhook.WebhookRepository
}

func NewIncomingWebhookHandler(repo webhook.WebhookRepository) *IncomingWebhookHandler {
	return &IncomingWebhookHandler{repo: repo}
}

// ReceiveWebhook verifies the webhook signature and returns 200 on success.
// @Summary Receive incoming webhook
// @Description Accepts an incoming webhook delivery and validates its HMAC-SHA256 signature.
// @Tags Webhooks
// @Accept json
// @Produce json
// @Param id path string true "Webhook ID"
// @Param X-Moistello-Signature header string true "HMAC-SHA256 hex signature"
// @Success 200 {object} response.Envelope
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /webhooks/incoming/{id} [post]
func (h *IncomingWebhookHandler) ReceiveWebhook(c *gin.Context) {
	id := c.Param("id")
	wh, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		response.InternalError(c, "failed to look up webhook")
		return
	}
	if wh == nil {
		response.NotFound(c, "webhook not found")
		return
	}

	signature := c.GetHeader("X-Moistello-Signature")
	if signature == "" {
		response.Unauthorized(c, "missing webhook signature")
		return
	}

	body, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "failed to read request body")
		return
	}

	// We only store the secret_hash; use it directly as the HMAC key.
	// SignWebhookPayload detects a 64-char hex digest and decodes it to raw
	// bytes before computing the HMAC, so verification is consistent.
	if !webhook.VerifyWebhookSignature(body, signature, wh.SecretHash) {
		response.Unauthorized(c, "invalid webhook signature")
		return
	}

	response.OK(c, gin.H{"received": true})
}

// newWebhookSecret returns a random 32-byte hex secret used to sign deliveries.
func newWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
