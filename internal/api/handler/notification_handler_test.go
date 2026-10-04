package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/notification"
)

type fakeNotificationService struct {
	bulkArchiveFn func(ctx context.Context, userID string, ids []string, archived bool) ([]string, error)
	searchFn      func(ctx context.Context, userID string, f notification.SearchFilter, page, limit int) ([]notification.Notification, int, error)
}

func (s *fakeNotificationService) Search(ctx context.Context, userID string, f notification.SearchFilter, page, limit int) ([]notification.Notification, int, error) {
	if s.searchFn != nil {
		return s.searchFn(ctx, userID, f, page, limit)
	}
	return nil, 0, nil
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

// FlushDueDigests satisfies notification.Service for the digest batching
// worker (#415). The handler tests never flush, so a no-op is correct here.
func (s *fakeNotificationService) FlushDueDigests(context.Context, time.Time) (int, error) {
	return 0, nil
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
	r.GET("/notifications/search", h.SearchNotifications)
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

func TestNotificationHandler_Search_PassesFilters(t *testing.T) {
	var got notification.SearchFilter
	fakeSvc := &fakeNotificationService{
		searchFn: func(_ context.Context, _ string, f notification.SearchFilter, _, _ int) ([]notification.Notification, int, error) {
			got = f
			return []notification.Notification{{Title: "Contribution due"}}, 1, nil
		},
	}
	r := setupNotificationTestRouter(fakeSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/notifications/search?q=due&type=contribution.due&unread=true", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Contribution due")
	assert.Equal(t, notification.SearchFilter{Query: "due", Type: "contribution.due", UnreadOnly: true}, got)
}

func TestNotificationHandler_Search_RejectsLongQuery(t *testing.T) {
	r := setupNotificationTestRouter(&fakeNotificationService{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/notifications/search?q="+strings.Repeat("a", notification.MaxSearchQueryLength+1), nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
