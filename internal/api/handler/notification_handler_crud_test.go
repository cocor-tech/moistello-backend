package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/user"
)

// Covers ListNotifications, MarkRead, MarkAllRead and UpdatePreferences
// (issues #841, #842, #843, #844).

const notifTestUserID = "00000000-0000-0000-0000-000000000001"

// prefsUserService overrides only UpdateNotificationPreferences on top of the
// shared fakeUserService. The shared fake returns (nil, nil), which would make
// the handler dereference a nil *user.User on the success path.
type prefsUserService struct {
	fakeUserService
	updatePrefsFn func(ctx context.Context, id string, prefs user.NotificationPrefsInput) (*user.User, error)
}

func (s *prefsUserService) UpdateNotificationPreferences(ctx context.Context, id string, prefs user.NotificationPrefsInput) (*user.User, error) {
	if s.updatePrefsFn != nil {
		return s.updatePrefsFn(ctx, id, prefs)
	}
	return &user.User{}, nil
}

func setupNotificationCRUDRouter(svc notification.Service, userSvc user.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewNotificationHandler(svc, userSvc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", notifTestUserID)
		c.Next()
	})
	r.GET("/notifications", h.ListNotifications)
	r.PATCH("/notifications/:id/read", h.MarkRead)
	r.PATCH("/notifications/read-all", h.MarkAllRead)
	r.PUT("/notifications/preferences", h.UpdatePreferences)
	return r
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body: %s", w.Body.String())
	return resp
}

// ─── ListNotifications ───────────────────────────────────────────────────────

func TestNotificationHandler_ListNotifications_PassesQueryAndReturnsMeta(t *testing.T) {
	var gotUser string
	var gotPage, gotLimit int
	var gotUnread bool
	svc := &fakeNotificationService{
		listFn: func(_ context.Context, userID string, page, limit int, unreadOnly bool) ([]notification.Notification, int, error) {
			gotUser, gotPage, gotLimit, gotUnread = userID, page, limit, unreadOnly
			return []notification.Notification{{Title: "one"}, {Title: "two"}}, 7, nil
		},
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/notifications?unread=true&page=2&limit=5", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, notifTestUserID, gotUser)
	assert.Equal(t, 2, gotPage)
	assert.Equal(t, 5, gotLimit)
	assert.True(t, gotUnread)

	resp := decodeEnvelope(t, w)
	data := resp["data"].(map[string]any)
	assert.Len(t, data["notifications"], 2)

	meta := resp["meta"].(map[string]any)
	assert.Equal(t, float64(2), meta["page"])
	assert.Equal(t, float64(5), meta["limit"])
	assert.Equal(t, float64(7), meta["total"])
	assert.Equal(t, float64(2), meta["totalPages"]) // ceil(7/5)
}

func TestNotificationHandler_ListNotifications_DefaultsWhenNoQuery(t *testing.T) {
	var gotPage, gotLimit int
	var gotUnread bool
	svc := &fakeNotificationService{
		listFn: func(_ context.Context, _ string, page, limit int, unreadOnly bool) ([]notification.Notification, int, error) {
			gotPage, gotLimit, gotUnread = page, limit, unreadOnly
			return nil, 0, nil
		},
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/notifications", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, gotPage)
	assert.Equal(t, 20, gotLimit)
	assert.False(t, gotUnread, "unread must default to false unless unread=true")
}

func TestNotificationHandler_ListNotifications_NonTrueUnreadIsIgnored(t *testing.T) {
	var gotUnread bool
	svc := &fakeNotificationService{
		listFn: func(_ context.Context, _ string, _, _ int, unreadOnly bool) ([]notification.Notification, int, error) {
			gotUnread = unreadOnly
			return nil, 0, nil
		},
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/notifications?unread=yes", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, gotUnread, `only the literal "true" enables the unread filter`)
}

func TestNotificationHandler_ListNotifications_ServiceErrorReturns500(t *testing.T) {
	svc := &fakeNotificationService{
		listFn: func(context.Context, string, int, int, bool) ([]notification.Notification, int, error) {
			return nil, 0, assert.AnError
		},
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/notifications", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to list notifications")
	assert.NotContains(t, w.Body.String(), assert.AnError.Error(), "internal error text must not leak")
}

// ─── MarkRead ────────────────────────────────────────────────────────────────

func TestNotificationHandler_MarkRead_Success(t *testing.T) {
	var gotID, gotUser string
	svc := &fakeNotificationService{
		markReadFn: func(_ context.Context, id, userID string) error {
			gotID, gotUser = id, userID
			return nil
		},
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/notifications/notif-123/read", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "notif-123", gotID)
	assert.Equal(t, notifTestUserID, gotUser, "must only mark the caller's own notification")
	data := decodeEnvelope(t, w)["data"].(map[string]any)
	assert.Equal(t, true, data["success"])
}

func TestNotificationHandler_MarkRead_ServiceErrorReturns500(t *testing.T) {
	svc := &fakeNotificationService{
		markReadFn: func(context.Context, string, string) error { return assert.AnError },
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/notifications/notif-123/read", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to mark notification as read")
}

// ─── MarkAllRead ─────────────────────────────────────────────────────────────

func TestNotificationHandler_MarkAllRead_Success(t *testing.T) {
	var gotUser string
	calls := 0
	svc := &fakeNotificationService{
		markAllReadFn: func(_ context.Context, userID string) error {
			gotUser = userID
			calls++
			return nil
		},
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/notifications/read-all", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, calls)
	assert.Equal(t, notifTestUserID, gotUser)
	data := decodeEnvelope(t, w)["data"].(map[string]any)
	assert.Equal(t, true, data["success"])
}

func TestNotificationHandler_MarkAllRead_ServiceErrorReturns500(t *testing.T) {
	svc := &fakeNotificationService{
		markAllReadFn: func(context.Context, string) error { return assert.AnError },
	}
	r := setupNotificationCRUDRouter(svc, &prefsUserService{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/notifications/read-all", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to mark all notifications as read")
}

// ─── UpdatePreferences ───────────────────────────────────────────────────────

func doPreferencesRequest(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/notifications/preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestNotificationHandler_UpdatePreferences_Success(t *testing.T) {
	var gotID string
	var gotPrefs user.NotificationPrefsInput
	userSvc := &prefsUserService{
		updatePrefsFn: func(_ context.Context, id string, prefs user.NotificationPrefsInput) (*user.User, error) {
			gotID, gotPrefs = id, prefs
			return &user.User{
				NotificationChannels:  []string{"inapp", "email"},
				NotificationsMuted:    true,
				DigestEnabled:         true,
				DigestIntervalMinutes: 60,
			}, nil
		},
	}
	r := setupNotificationCRUDRouter(&fakeNotificationService{}, userSvc)

	w := doPreferencesRequest(r, `{"channels":["inapp","email"],"muted":true,"digestEnabled":true,"digestIntervalMinutes":60}`)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, notifTestUserID, gotID)
	assert.Equal(t, []string{"inapp", "email"}, gotPrefs.Channels)
	assert.True(t, gotPrefs.Muted)
	require.NotNil(t, gotPrefs.DigestEnabled)
	assert.True(t, *gotPrefs.DigestEnabled)
	require.NotNil(t, gotPrefs.DigestIntervalMinutes)
	assert.Equal(t, 60, *gotPrefs.DigestIntervalMinutes)

	prefs := decodeEnvelope(t, w)["data"].(map[string]any)["preferences"].(map[string]any)
	assert.Equal(t, []any{"inapp", "email"}, prefs["channels"])
	assert.Equal(t, true, prefs["muted"])
	assert.Equal(t, true, prefs["digestEnabled"])
	assert.Equal(t, float64(60), prefs["digestIntervalMinutes"])

	minC, maxC := user.NotificationDigestCadenceBounds()
	rng := prefs["digestCadenceRange"].(map[string]any)
	assert.Equal(t, float64(minC), rng["minMinutes"])
	assert.Equal(t, float64(maxC), rng["maxMinutes"])
}

func TestNotificationHandler_UpdatePreferences_OmittedDigestFieldsArePassedAsNil(t *testing.T) {
	var gotPrefs user.NotificationPrefsInput
	userSvc := &prefsUserService{
		updatePrefsFn: func(_ context.Context, _ string, prefs user.NotificationPrefsInput) (*user.User, error) {
			gotPrefs = prefs
			return &user.User{NotificationChannels: prefs.Channels}, nil
		},
	}
	r := setupNotificationCRUDRouter(&fakeNotificationService{}, userSvc)

	w := doPreferencesRequest(r, `{"channels":["push"]}`)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, gotPrefs.DigestEnabled, "omitted digestEnabled must stay nil so the stored value is kept")
	assert.Nil(t, gotPrefs.DigestIntervalMinutes, "omitted digestIntervalMinutes must stay nil so the stored value is kept")
	assert.False(t, gotPrefs.Muted)
}

func TestNotificationHandler_UpdatePreferences_MalformedJSONReturns400(t *testing.T) {
	called := false
	userSvc := &prefsUserService{
		updatePrefsFn: func(context.Context, string, user.NotificationPrefsInput) (*user.User, error) {
			called = true
			return &user.User{}, nil
		},
	}
	r := setupNotificationCRUDRouter(&fakeNotificationService{}, userSvc)

	w := doPreferencesRequest(r, `{"channels":`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, called, "service must not be called for an unparseable body")
}

func TestNotificationHandler_UpdatePreferences_MissingOrEmptyChannelsReturns400(t *testing.T) {
	cases := map[string]string{
		"missing channels": `{"muted":true}`,
		"empty channels":   `{"channels":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			userSvc := &prefsUserService{
				updatePrefsFn: func(context.Context, string, user.NotificationPrefsInput) (*user.User, error) {
					called = true
					return &user.User{}, nil
				},
			}
			r := setupNotificationCRUDRouter(&fakeNotificationService{}, userSvc)

			w := doPreferencesRequest(r, body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.False(t, called, "binding validation must reject before the service runs")
		})
	}
}

func TestNotificationHandler_UpdatePreferences_ServiceErrorReturns400WithMessage(t *testing.T) {
	userSvc := &prefsUserService{
		updatePrefsFn: func(context.Context, string, user.NotificationPrefsInput) (*user.User, error) {
			return nil, errors.New(`invalid notification channel "fax": must be one of inapp, email, sms, push`)
		},
	}
	r := setupNotificationCRUDRouter(&fakeNotificationService{}, userSvc)

	w := doPreferencesRequest(r, `{"channels":["fax"]}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `invalid notification channel \"fax\"`)
}

func TestNotificationHandler_UpdatePreferences_UserNotFoundReturns400(t *testing.T) {
	userSvc := &prefsUserService{
		updatePrefsFn: func(context.Context, string, user.NotificationPrefsInput) (*user.User, error) {
			return nil, user.ErrUserNotFound
		},
	}
	r := setupNotificationCRUDRouter(&fakeNotificationService{}, userSvc)

	w := doPreferencesRequest(r, `{"channels":["inapp"]}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), user.ErrUserNotFound.Error())
}
