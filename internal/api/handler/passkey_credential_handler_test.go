package handler_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
)

func TestNewPasskeyCredentialHandler(t *testing.T) {
	mockDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = mockDB.Close() }()

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")

	tests := []struct {
		name    string
		db      *sqlx.DB
		wantNil bool
	}{
		{
			name:    "with valid sqlx db",
			db:      sqlxDB,
			wantNil: false,
		},
		{
			name:    "with nil db",
			db:      nil,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := handler.NewPasskeyCredentialHandler(tt.db)
			if tt.wantNil {
				assert.Nil(t, h)
			} else {
				assert.NotNil(t, h)
			}
		})
	}
}

func TestPasskeyCredentialHandler_StoreCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		requestBody    any
		setupMock      func(mock sqlmock.Sqlmock)
		expectedStatus int
		expectContains string
	}{
		{
			name: "success store credential",
			requestBody: map[string]any{
				"credentialId": "cred-id-123",
				"publicKey":    []byte("public-key-bytes"),
				"counter":      1,
				"transports":   []string{"usb", "nfc"},
				"emailHash":    "hash-123",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO passkey_credentials").
					WithArgs("cred-id-123", []byte("public-key-bytes"), 1, sqlmock.AnyArg(), "hash-123").
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedStatus: http.StatusOK,
			expectContains: `"success":true`,
		},
		{
			name: "missing required credentialId",
			requestBody: map[string]any{
				"publicKey": []byte("public-key-bytes"),
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				// No DB call expected
			},
			expectedStatus: http.StatusBadRequest,
			expectContains: `"success":false`,
		},
		{
			name: "database execution error",
			requestBody: map[string]any{
				"credentialId": "cred-id-456",
				"publicKey":    []byte("key-bytes"),
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO passkey_credentials").
					WillReturnError(errors.New("db error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectContains: "failed to store credential",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = mockDB.Close() }()

			sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
			tt.setupMock(mock)

			h := handler.NewPasskeyCredentialHandler(sqlxDB)
			r := gin.New()
			r.POST("/credentials", h.StoreCredential)

			body, _ := json.Marshal(tt.requestBody)
			w := httptest.NewRecorder()
			req, err := http.NewRequest(http.MethodPost, "/credentials", bytes.NewBuffer(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectContains != "" {
				assert.Contains(t, w.Body.String(), tt.expectContains)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPasskeyCredentialHandler_GetCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		pathID         string
		setupMock      func(mock sqlmock.Sqlmock)
		expectedStatus int
		expectContains string
	}{
		{
			name:   "success get credential",
			pathID: "cred-abc-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"credential_id", "public_key", "counter", "transports", "email_hash"}).
					AddRow("cred-abc-123", []byte("pubkey-bytes"), 5, "{usb,internal}", "email-hash-val")
				mock.ExpectQuery(`SELECT credential_id, public_key, counter, transports, email_hash FROM passkey_credentials WHERE credential_id = \$1`).
					WithArgs("cred-abc-123").
					WillReturnRows(rows)
			},
			expectedStatus: http.StatusOK,
			expectContains: "cred-abc-123",
		},
		{
			name:   "credential not found",
			pathID: "non-existent-id",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT credential_id, public_key, counter, transports, email_hash FROM passkey_credentials WHERE credential_id = \$1`).
					WithArgs("non-existent-id").
					WillReturnError(sql.ErrNoRows)
			},
			expectedStatus: http.StatusNotFound,
			expectContains: "credential not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = mockDB.Close() }()

			sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
			tt.setupMock(mock)

			h := handler.NewPasskeyCredentialHandler(sqlxDB)
			r := gin.New()
			r.GET("/credentials/:id", h.GetCredential)

			w := httptest.NewRecorder()
			req, err := http.NewRequest(http.MethodGet, "/credentials/"+tt.pathID, nil)
			require.NoError(t, err)

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectContains != "" {
				assert.Contains(t, w.Body.String(), tt.expectContains)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
