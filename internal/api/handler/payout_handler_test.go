package handler_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/payout"
)

type mockPayoutService struct {
	mock.Mock
}

func (m *mockPayoutService) Record(ctx context.Context, input payout.RecordInput) (*payout.Payout, error) {
	args := m.Called(ctx, input)
	if p, ok := args.Get(0).(*payout.Payout); ok {
		return p, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockPayoutService) GetUserHistory(ctx context.Context, userID string, page, limit int) ([]payout.Payout, int, error) {
	args := m.Called(ctx, userID, page, limit)
	if p, ok := args.Get(0).([]payout.Payout); ok {
		return p, args.Int(1), args.Error(2)
	}
	return nil, args.Int(1), args.Error(2)
}

func (m *mockPayoutService) GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]payout.Payout, int, error) {
	args := m.Called(ctx, circleID, page, limit)
	if p, ok := args.Get(0).([]payout.Payout); ok {
		return p, args.Int(1), args.Error(2)
	}
	return nil, args.Int(1), args.Error(2)
}

func (m *mockPayoutService) UpdateVerification(ctx context.Context, id string, verifiedOnchain bool, status payout.VerificationStatus) error {
	args := m.Called(ctx, id, verifiedOnchain, status)
	return args.Error(0)
}

type mockPayoutRepository struct {
	mock.Mock
}

func (m *mockPayoutRepository) FindByID(ctx context.Context, id uuid.UUID) (*payout.Payout, error) {
	args := m.Called(ctx, id)
	if p, ok := args.Get(0).(*payout.Payout); ok {
		return p, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockPayoutRepository) FindByTxnHash(ctx context.Context, txnHash string) (*payout.Payout, error) {
	args := m.Called(ctx, txnHash)
	if p, ok := args.Get(0).(*payout.Payout); ok {
		return p, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockPayoutRepository) Create(ctx context.Context, p *payout.Payout) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockPayoutRepository) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status payout.VerificationStatus) error {
	args := m.Called(ctx, id, verifiedOnchain, status)
	return args.Error(0)
}

func (m *mockPayoutRepository) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]payout.Payout, int, error) {
	args := m.Called(ctx, userID, page, limit)
	if p, ok := args.Get(0).([]payout.Payout); ok {
		return p, args.Int(1), args.Error(2)
	}
	return nil, args.Int(1), args.Error(2)
}

func (m *mockPayoutRepository) ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]payout.Payout, int, error) {
	args := m.Called(ctx, circleID, page, limit)
	if p, ok := args.Get(0).([]payout.Payout); ok {
		return p, args.Int(1), args.Error(2)
	}
	return nil, args.Int(1), args.Error(2)
}

func TestNewPayoutHandler(t *testing.T) {
	tests := []struct {
		name    string
		svc     payout.Service
		repo    payout.Repository
		wantNil bool
	}{
		{
			name:    "with service and repository",
			svc:     new(mockPayoutService),
			repo:    new(mockPayoutRepository),
			wantNil: false,
		},
		{
			name:    "with nil service and repository",
			svc:     nil,
			repo:    nil,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := handler.NewPayoutHandler(tt.svc, tt.repo)
			if tt.wantNil {
				assert.Nil(t, h)
			} else {
				assert.NotNil(t, h)
			}
		})
	}
}

func TestPayoutHandler_ListPayouts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	samplePayouts := []payout.Payout{
		{
			ID:                 uuid.New(),
			CircleID:           uuid.New(),
			RecipientID:        uuid.New(),
			RoundNumber:        1,
			Amount:             500.0,
			FeeAmount:          5.0,
			PayoutType:         payout.PayoutTypeFixed,
			VerifiedOnchain:    true,
			VerificationStatus: payout.VerificationStatusVerified,
			CreatedAt:          time.Now().UTC(),
			UpdatedAt:          time.Now().UTC(),
			OnTime:             true,
		},
	}

	tests := []struct {
		name           string
		queryURL       string
		authUserID     string
		setupMocks     func(svc *mockPayoutService)
		expectedStatus int
		expectContains string
	}{
		{
			name:       "circleId filter success",
			queryURL:   "/payouts?circleId=circle-uuid-1&page=1&limit=20",
			authUserID: "user-default-1",
			setupMocks: func(svc *mockPayoutService) {
				svc.On("GetCircleHistory", mock.Anything, "circle-uuid-1", 1, 20).
					Return(samplePayouts, 1, nil).Once()
			},
			expectedStatus: http.StatusOK,
			expectContains: `"success":true`,
		},
		{
			name:       "circleId filter error",
			queryURL:   "/payouts?circleId=circle-uuid-1",
			authUserID: "user-default-1",
			setupMocks: func(svc *mockPayoutService) {
				svc.On("GetCircleHistory", mock.Anything, "circle-uuid-1", mock.Anything, mock.Anything).
					Return(nil, 0, errors.New("db error")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
			expectContains: "failed to list payouts",
		},
		{
			name:       "userId filter success",
			queryURL:   "/payouts?userId=user-filter-1&page=1&limit=10",
			authUserID: "user-default-1",
			setupMocks: func(svc *mockPayoutService) {
				svc.On("GetUserHistory", mock.Anything, "user-filter-1", 1, 10).
					Return(samplePayouts, 1, nil).Once()
			},
			expectedStatus: http.StatusOK,
			expectContains: `"success":true`,
		},
		{
			name:       "userId filter error",
			queryURL:   "/payouts?userId=user-filter-1",
			authUserID: "user-default-1",
			setupMocks: func(svc *mockPayoutService) {
				svc.On("GetUserHistory", mock.Anything, "user-filter-1", mock.Anything, mock.Anything).
					Return(nil, 0, errors.New("query error")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
			expectContains: "failed to list payouts",
		},
		{
			name:       "default authenticated user success",
			queryURL:   "/payouts?page=2&limit=5",
			authUserID: "user-auth-123",
			setupMocks: func(svc *mockPayoutService) {
				svc.On("GetUserHistory", mock.Anything, "user-auth-123", 2, 5).
					Return(samplePayouts, 1, nil).Once()
			},
			expectedStatus: http.StatusOK,
			expectContains: `"success":true`,
		},
		{
			name:       "default authenticated user error",
			queryURL:   "/payouts",
			authUserID: "user-auth-123",
			setupMocks: func(svc *mockPayoutService) {
				svc.On("GetUserHistory", mock.Anything, "user-auth-123", mock.Anything, mock.Anything).
					Return(nil, 0, errors.New("history error")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
			expectContains: "failed to list payouts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := new(mockPayoutService)
			repo := new(mockPayoutRepository)
			tt.setupMocks(svc)

			h := handler.NewPayoutHandler(svc, repo)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tt.authUserID != "" {
					c.Set("userID", tt.authUserID)
				}
				c.Next()
			})
			r.GET("/payouts", h.ListPayouts)

			w := httptest.NewRecorder()
			req, err := http.NewRequest(http.MethodGet, tt.queryURL, nil)
			require.NoError(t, err)

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectContains != "" {
				assert.Contains(t, w.Body.String(), tt.expectContains)
			}
			svc.AssertExpectations(t)
		})
	}
}

func TestPayoutHandler_GetPayout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validUID := uuid.New()
	samplePayout := &payout.Payout{
		ID:                 validUID,
		CircleID:           uuid.New(),
		RecipientID:        uuid.New(),
		RoundNumber:        1,
		Amount:             250.0,
		FeeAmount:          2.5,
		PayoutType:         payout.PayoutTypeFixed,
		VerifiedOnchain:    true,
		VerificationStatus: payout.VerificationStatusVerified,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		OnTime:             true,
	}

	tests := []struct {
		name           string
		pathID         string
		setupMocks     func(repo *mockPayoutRepository)
		expectedStatus int
		expectContains string
	}{
		{
			name:   "valid ID found",
			pathID: validUID.String(),
			setupMocks: func(repo *mockPayoutRepository) {
				repo.On("FindByID", mock.Anything, validUID).
					Return(samplePayout, nil).Once()
			},
			expectedStatus: http.StatusOK,
			expectContains: validUID.String(),
		},
		{
			name:   "invalid UUID format",
			pathID: "not-a-valid-uuid",
			setupMocks: func(repo *mockPayoutRepository) {
				// No repo calls expected for invalid UUID
			},
			expectedStatus: http.StatusBadRequest,
			expectContains: "invalid payout ID",
		},
		{
			name:   "valid ID not found",
			pathID: validUID.String(),
			setupMocks: func(repo *mockPayoutRepository) {
				repo.On("FindByID", mock.Anything, validUID).
					Return(nil, sql.ErrNoRows).Once()
			},
			expectedStatus: http.StatusNotFound,
			expectContains: "payout not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := new(mockPayoutService)
			repo := new(mockPayoutRepository)
			tt.setupMocks(repo)

			h := handler.NewPayoutHandler(svc, repo)
			r := gin.New()
			r.GET("/payouts/:id", h.GetPayout)

			w := httptest.NewRecorder()
			req, err := http.NewRequest(http.MethodGet, "/payouts/"+tt.pathID, nil)
			require.NoError(t, err)

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectContains != "" {
				assert.Contains(t, w.Body.String(), tt.expectContains)
			}
			repo.AssertExpectations(t)
		})
	}
}
