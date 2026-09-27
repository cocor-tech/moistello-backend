package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/notification"
)

type fakeNotificationService struct {
	bulkArchiveFn func(ctx context.Context, userID string, ids []string, archived bool) ([]string, error)
}

func (s *fakeNotificationService) Create(context.Context, notification.CreateInput) (*notification.Notification, error) {
	return nil, nil
}

func (s *fakeNotificationService) List(context.Context, string, int, int, bool) ([]notification.Notification, int, error) {
	return nil, 0, nil
}

func (s *fakeNotificationService) MarkRead(context.Context, string, string) error {
	return nil
}

func (s *fakeNotificationService) MarkAllRead(context.Context, string) error {
	return nil
}

func (s *fakeNotificationService) BulkArchive(ctx context.Context, userID string, ids []string, archived bool) ([]string, error) {
	if s.bulkArchiveFn != nil {
		return s.bulkArchiveFn(ctx, userID, ids, archived)
	}
	return ids, nil
}

func setupNotificationTestRouter(svc notification.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewNotificationHandler(svc, &fakeUserService{})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})
	r.POST("/notifications/bulk-archive", h.BulkArchive)
	r.POST("/notifications/bulk-unarchive", h.BulkUnarchive)
	return r
}

func TestNotificationHandler_BulkArchive_Success(t *testing.T) {
	fakeSvc := &fakeNotificationService{
		bulkArchiveFn: func(ctx context.Context, userID string, ids []string, archived bool) ([]string, error) {
			assert.True(t, archived)
			return ids, nil
		},
	}
	r := setupNotificationTestRouter(fakeSvc)

	body, _ := json.Marshal(map[string]any{
		"ids": []string{"00000000-0000-0000-0000-000000000002"},
	})
	req, _ := http.NewRequest("POST", "/notifications/bulk-archive", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, float64(1), data["updated"])
}

func TestNotificationHandler_BulkUnarchive_Success(t *testing.T) {
	fakeSvc := &fakeNotificationService{
		bulkArchiveFn: func(ctx context.Context, userID string, ids []string, archived bool) ([]string, error) {
			assert.False(t, archived)
			return ids, nil
		},
	}
	r := setupNotificationTestRouter(fakeSvc)

	body, _ := json.Marshal(map[string]any{
		"ids": []string{"00000000-0000-0000-0000-000000000002"},
	})
	req, _ := http.NewRequest("POST", "/notifications/bulk-unarchive", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, float64(1), data["updated"])
}
