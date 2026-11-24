package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/incentives"
)

// referralStub overrides only the incentives methods the referral handler uses.
type referralStub struct {
	*stubIncentivesService
	code        string
	codeErr     error
	summary     *incentives.UserIncentiveSummary
	summaryErr  error
	referrals   []incentives.Referral
	referralErr error
}

func (s *referralStub) GenerateReferralCode(context.Context, string) (string, error) {
	return s.code, s.codeErr
}

func (s *referralStub) GetUserSummary(context.Context, string) (*incentives.UserIncentiveSummary, error) {
	return s.summary, s.summaryErr
}

func (s *referralStub) GetReferrals(context.Context, string) ([]incentives.Referral, error) {
	return s.referrals, s.referralErr
}

func newReferralRouter(svc incentives.Service, handlerFn func(*handler.ReferralHandler) gin.HandlerFunc, method, path string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	r.Handle(method, path, handlerFn(handler.NewReferralHandler(svc)))
	return r
}

func TestReferralHandler_GenerateCode(t *testing.T) {
	gen := func(h *handler.ReferralHandler) gin.HandlerFunc { return h.GenerateCode }

	t.Run("created", func(t *testing.T) {
		svc := &referralStub{stubIncentivesService: &stubIncentivesService{}, code: "ref-123"}
		w := httptest.NewRecorder()
		newReferralRouter(svc, gen, http.MethodPost, "/referral/code").
			ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/referral/code", nil))

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Contains(t, w.Body.String(), "ref-123")
	})

	t.Run("service error is a bad request", func(t *testing.T) {
		svc := &referralStub{stubIncentivesService: &stubIncentivesService{}, codeErr: errors.New("code space exhausted")}
		w := httptest.NewRecorder()
		newReferralRouter(svc, gen, http.MethodPost, "/referral/code").
			ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/referral/code", nil))

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "code space exhausted")
	})
}

func TestReferralHandler_GetStats(t *testing.T) {
	stats := func(h *handler.ReferralHandler) gin.HandlerFunc { return h.GetStats }

	t.Run("ok", func(t *testing.T) {
		svc := &referralStub{
			stubIncentivesService: &stubIncentivesService{},
			summary:               &incentives.UserIncentiveSummary{ReferralCount: 3},
		}
		w := httptest.NewRecorder()
		newReferralRouter(svc, stats, http.MethodGet, "/referral/stats").
			ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/referral/stats", nil))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"totalReferrals":3`)
	})

	t.Run("service error is internal", func(t *testing.T) {
		svc := &referralStub{stubIncentivesService: &stubIncentivesService{}, summaryErr: errors.New("boom")}
		w := httptest.NewRecorder()
		newReferralRouter(svc, stats, http.MethodGet, "/referral/stats").
			ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/referral/stats", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NotContains(t, w.Body.String(), "boom", "internal errors must not leak to clients")
	})
}

func TestReferralHandler_GetHistory(t *testing.T) {
	hist := func(h *handler.ReferralHandler) gin.HandlerFunc { return h.GetHistory }

	t.Run("nil list is returned as empty array", func(t *testing.T) {
		svc := &referralStub{stubIncentivesService: &stubIncentivesService{}}
		w := httptest.NewRecorder()
		newReferralRouter(svc, hist, http.MethodGet, "/referral/history").
			ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/referral/history", nil))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"referrals":[]`)
	})

	t.Run("service error is internal", func(t *testing.T) {
		svc := &referralStub{stubIncentivesService: &stubIncentivesService{}, referralErr: errors.New("boom")}
		w := httptest.NewRecorder()
		newReferralRouter(svc, hist, http.MethodGet, "/referral/history").
			ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/referral/history", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
