package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/moistello/backend/internal/domain/circle"
	ws "github.com/moistello/backend/internal/websocket"
)

type Option func(*WebSocketHandler)

// WithCircleService wires a circle.Service into the handler so that
// subscribe authorization can enforce circle membership.
func WithCircleService(svc circle.Service) Option {
	return func(h *WebSocketHandler) {
		if h.hub != nil {
			h.hub.SetSubscriptionAuthorizer(&circleSubscriptionAuthorizer{service: svc})
		}
	}
}

type circleSubscriptionAuthorizer struct {
	service circle.Service
}

func (a *circleSubscriptionAuthorizer) CanSubscribe(ctx context.Context, circleID, userID string) (bool, error) {
	c, err := a.service.Get(ctx, circleID)
	if err != nil {
		return false, err
	}
	if c.CircleType == circle.CircleTypePublic {
		return true, nil
	}
	return a.service.IsMember(ctx, circleID, userID)
}

// WebSocketHandler handles HTTP-to-WebSocket upgrades and manages client
// connections via the Hub.
type WebSocketHandler struct {
	hub            *ws.Hub
	upgrader       websocket.Upgrader
	maxMessageSize int64
}

// NewWebSocketHandler creates a new WebSocketHandler backed by the given Hub.
// The upgrader is configured with generous buffer sizes and strict origin checking.
func NewWebSocketHandler(hub *ws.Hub, allowedOrigins []string, opts ...Option) *WebSocketHandler {
	h := &WebSocketHandler{
		hub: hub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				for _, allowed := range allowedOrigins {
					if origin == allowed {
						return true
					}
				}
				return false
			},
		},
		maxMessageSize: ws.DefaultMaxMessageSize,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithMaxMessageSize sets the largest inbound frame, in bytes, a connection may
// send (#461). Non-positive values keep ws.DefaultMaxMessageSize.
func WithMaxMessageSize(size int64) Option {
	return func(h *WebSocketHandler) {
		if size > 0 {
			h.maxMessageSize = size
		}
	}
}

// HandleWebSocket upgrades an authenticated HTTP connection to a WebSocket and
// registers the new client with the Hub.
func (h *WebSocketHandler) HandleWebSocket(c *gin.Context) {
	userID := c.GetString("userID")
	if userID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "authentication required",
		})
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	client := ws.NewClient(h.hub, conn, userID)
	client.SetMaxMessageSize(h.maxMessageSize)
	h.hub.Register(client)

	go client.WritePump()
	go client.ReadPump()
}

// Hub returns the underlying Hub instance for metrics and integration.
func (h *WebSocketHandler) Hub() *ws.Hub {
	return h.hub
}
