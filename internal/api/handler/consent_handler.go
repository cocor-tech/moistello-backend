package handler

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/pkg/response"
)

// ConsentRecord stores a user's cookie consent preferences.
type ConsentRecord struct {
	ID        string    `json:"id"         db:"id"`
	UserID    *string   `json:"userId"     db:"user_id"`
	SessionID *string   `json:"sessionId"  db:"session_id"`
	Analytics bool      `json:"analytics"  db:"analytics"`
	Marketing bool      `json:"marketing"  db:"marketing"`
	IPAddress *string   `json:"-"          db:"ip_address"`
	UserAgent *string   `json:"-"          db:"user_agent"`
	CreatedAt time.Time `json:"createdAt"  db:"created_at"`
	UpdatedAt time.Time `json:"updatedAt"  db:"updated_at"`
}

// ConsentHandler handles GDPR cookie consent endpoints.
type ConsentHandler struct {
	db *sql.DB
}

// NewConsentHandler creates a new ConsentHandler.
func NewConsentHandler(db *sql.DB) *ConsentHandler {
	return &ConsentHandler{db: db}
}

// SaveConsent stores or updates a user's cookie consent preferences.
//
// @Summary Save cookie consent
// @Description Saves or updates the authenticated (or anonymous) user's cookie consent preferences.
//
//	Only load analytics (Yandex Metrika) and marketing scripts after this returns analytics: true.
//
// @Tags GDPR
// @Accept json
// @Produce json
// @Param body body object true "Consent preferences" example({"analytics":true,"marketing":false,"sessionId":"anon-abc123"})
// @Success 200 {object} response.Envelope{data=ConsentRecord}
// @Failure 400 {object} response.Envelope
// @Router /v1/consent [post]
func (h *ConsentHandler) SaveConsent(c *gin.Context) {
	var req struct {
		Analytics bool   `json:"analytics"`
		Marketing bool   `json:"marketing"`
		SessionID string `json:"sessionId"` // client-generated anonymous ID before login
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	// Prefer authenticated user ID; fall back to session ID for anonymous visitors.
	userID := middleware.GetUserID(c) // empty string when not authenticated
	if userID == "" && req.SessionID == "" {
		response.BadRequest(c, "sessionId is required for anonymous consent")
		return
	}

	ipAddr := c.ClientIP()
	ua := c.Request.UserAgent()
	now := time.Now().UTC()
	id := uuid.New().String()

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var rec ConsentRecord

	if userID != "" {
		// Authenticated path: upsert by user_id
		err := h.db.QueryRowContext(ctx, `
			INSERT INTO cookie_consents (id, user_id, analytics, marketing, ip_address, user_agent, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
			ON CONFLICT (user_id) WHERE user_id IS NOT NULL
			DO UPDATE SET
				analytics  = EXCLUDED.analytics,
				marketing  = EXCLUDED.marketing,
				ip_address = EXCLUDED.ip_address,
				user_agent = EXCLUDED.user_agent,
				updated_at = EXCLUDED.updated_at
			RETURNING id, user_id::text, session_id, analytics, marketing, created_at, updated_at`,
			id, userID, req.Analytics, req.Marketing, ipAddr, ua, now,
		).Scan(&rec.ID, &rec.UserID, &rec.SessionID, &rec.Analytics, &rec.Marketing, &rec.CreatedAt, &rec.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save consent"})
			return
		}
	} else {
		// Anonymous path: upsert by session_id
		err := h.db.QueryRowContext(ctx, `
			INSERT INTO cookie_consents (id, session_id, analytics, marketing, ip_address, user_agent, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
			ON CONFLICT (session_id) WHERE session_id IS NOT NULL
			DO UPDATE SET
				analytics  = EXCLUDED.analytics,
				marketing  = EXCLUDED.marketing,
				ip_address = EXCLUDED.ip_address,
				user_agent = EXCLUDED.user_agent,
				updated_at = EXCLUDED.updated_at
			RETURNING id, user_id::text, session_id, analytics, marketing, created_at, updated_at`,
			id, req.SessionID, req.Analytics, req.Marketing, ipAddr, ua, now,
		).Scan(&rec.ID, &rec.UserID, &rec.SessionID, &rec.Analytics, &rec.Marketing, &rec.CreatedAt, &rec.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save consent"})
			return
		}
	}

	response.OK(c, rec)
}

// GetConsent retrieves the current user's cookie consent preferences.
//
// @Summary Get cookie consent
// @Description Returns the stored cookie consent preferences for the current user or session.
// @Tags GDPR
// @Produce json
// @Param sessionId query string false "Anonymous session ID (for unauthenticated users)"
// @Success 200 {object} response.Envelope{data=ConsentRecord}
// @Failure 404 {object} response.Envelope
// @Router /v1/consent [get]
func (h *ConsentHandler) GetConsent(c *gin.Context) {
	userID := middleware.GetUserID(c)
	sessionID := c.Query("sessionId")

	if userID == "" && sessionID == "" {
		response.NotFound(c, "no consent record found")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var rec ConsentRecord
	var err error

	if userID != "" {
		err = h.db.QueryRowContext(ctx,
			`SELECT id, user_id::text, session_id, analytics, marketing, created_at, updated_at
			 FROM cookie_consents WHERE user_id = $1 LIMIT 1`, userID,
		).Scan(&rec.ID, &rec.UserID, &rec.SessionID, &rec.Analytics, &rec.Marketing, &rec.CreatedAt, &rec.UpdatedAt)
	} else {
		err = h.db.QueryRowContext(ctx,
			`SELECT id, user_id::text, session_id, analytics, marketing, created_at, updated_at
			 FROM cookie_consents WHERE session_id = $1 LIMIT 1`, sessionID,
		).Scan(&rec.ID, &rec.UserID, &rec.SessionID, &rec.Analytics, &rec.Marketing, &rec.CreatedAt, &rec.UpdatedAt)
	}

	if err == sql.ErrNoRows {
		response.NotFound(c, "no consent record found")
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve consent"})
		return
	}

	response.OK(c, rec)
}
