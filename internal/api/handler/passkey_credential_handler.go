package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type PasskeyCredentialHandler struct {
	db *sqlx.DB
}

func NewPasskeyCredentialHandler(db *sqlx.DB) *PasskeyCredentialHandler {
	return &PasskeyCredentialHandler{db: db}
}

func (h *PasskeyCredentialHandler) StoreCredential(c *gin.Context) {
	var req struct {
		CredentialID string   `json:"credentialId" binding:"required"`
		PublicKey    []byte   `json:"publicKey" binding:"required"`
		Counter      int      `json:"counter"`
		Transports   []string `json:"transports"`
		EmailHash    string   `json:"emailHash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	query := `
		INSERT INTO passkey_credentials (credential_id, public_key, counter, transports, email_hash)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (credential_id) DO UPDATE SET
			public_key = EXCLUDED.public_key,
			counter = EXCLUDED.counter,
			transports = EXCLUDED.transports,
			email_hash = EXCLUDED.email_hash,
			updated_at = NOW()
	`
	_, err := h.db.ExecContext(c.Request.Context(), query,
		req.CredentialID, req.PublicKey, req.Counter, pq.Array(req.Transports), req.EmailHash,
	)
	if err != nil {
		log.Printf("StoreCredential error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to store credential"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PasskeyCredentialHandler) GetCredential(c *gin.Context) {
	credentialID := c.Param("id")
	if credentialID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "missing credential id"})
		return
	}

	var row struct {
		CredentialID string         `db:"credential_id"`
		PublicKey    []byte         `db:"public_key"`
		Counter      int            `db:"counter"`
		Transports   pq.StringArray `db:"transports"`
		EmailHash    *string        `db:"email_hash"`
	}

	query := `SELECT credential_id, public_key, counter, transports, email_hash FROM passkey_credentials WHERE credential_id = $1`
	err := h.db.GetContext(c.Request.Context(), &row, query, credentialID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "credential not found"})
		return
	}

	emailHash := ""
	if row.EmailHash != nil {
		emailHash = *row.EmailHash
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"credentialId": row.CredentialID,
			"publicKey": row.PublicKey,
			"counter":    row.Counter,
			"transports": row.Transports,
			"emailHash":  emailHash,
		},
	})
}
