package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/payout"
)

// lifecycleStore is a dedicated, per-test state store. It exercises the HTTP
// orchestration without sharing data or requiring a developer database.
type lifecycleStore struct {
	mu            sync.Mutex
	circle        *circle.Circle
	members       map[uuid.UUID]struct{}
	contributions []contribution.Contribution
	payouts       []payout.Payout
}

func newLifecycleStore() *lifecycleStore {
	return &lifecycleStore{members: make(map[uuid.UUID]struct{})}
}

func (s *lifecycleStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.circle = nil
	s.members = make(map[uuid.UUID]struct{})
	s.contributions = nil
	s.payouts = nil
}

type lifecycleCircleService struct{ store *lifecycleStore }

func (s *lifecycleCircleService) QueryRoundConfig(_ context.Context, _ string, _ int) (*circle.RoundConfigSnapshot, error) {
	return nil, circle.ErrCircleNotFound
}

func (s *lifecycleCircleService) Get(_ context.Context, id string) (*circle.Circle, error) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.circle == nil || s.store.circle.ID.String() != id {
		return nil, circle.ErrCircleNotFound
	}
	copy := *s.store.circle
	return &copy, nil
}

func (s *lifecycleCircleService) List(context.Context, circle.CircleFilter) ([]circle.Circle, int, error) {
	if s.store.circle == nil {
		return []circle.Circle{}, 0, nil
	}
	return []circle.Circle{*s.store.circle}, 1, nil
}

func (s *lifecycleCircleService) Create(
	_ context.Context, organizerID string, input circle.CreateCircleInput,
) (*circle.Circle, error) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	organizer := uuid.MustParse(organizerID)
	s.store.circle = &circle.Circle{
		ID: uuid.New(), Name: input.Name, CircleType: input.CircleType,
		PayoutType: input.PayoutType, ContributionAmount: input.ContributionAmount,
		Currency: input.Currency, Frequency: input.Frequency, MaxMembers: input.MaxMembers,
		Status: circle.CircleStatusPending, OrganizerID: organizer,
	}
	s.store.members[organizer] = struct{}{}
	copy := *s.store.circle
	return &copy, nil
}

func (s *lifecycleCircleService) Update(
	context.Context, string, string, circle.UpdateCircleInput,
) (*circle.Circle, error) {
	return nil, errors.New("not used")
}

func (s *lifecycleCircleService) Start(_ context.Context, id, userID string) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.circle.ID.String() != id || s.store.circle.OrganizerID.String() != userID {
		return errors.New("not organizer")
	}
	if len(s.store.members) < 2 {
		return errors.New("need at least 2 members")
	}
	s.store.circle.Status = circle.CircleStatusActive
	s.store.circle.CurrentRound = 1
	return nil
}

func (s *lifecycleCircleService) Close(_ context.Context, id, userID string) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.circle.ID.String() != id || s.store.circle.OrganizerID.String() != userID {
		return errors.New("not organizer")
	}
	if s.store.circle.Status != circle.CircleStatusActive || len(s.store.payouts) == 0 {
		return errors.New("circle cannot close before payout")
	}
	s.store.circle.Status = circle.CircleStatusCompleted
	return nil
}

func (s *lifecycleCircleService) Cancel(context.Context, string, string) error { return nil }

func (s *lifecycleCircleService) Join(_ context.Context, id, userID, _ string) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.circle.ID.String() != id {
		return circle.ErrCircleNotFound
	}
	s.store.members[uuid.MustParse(userID)] = struct{}{}
	return nil
}

func (s *lifecycleCircleService) Exit(context.Context, string, string) error { return nil }

func (s *lifecycleCircleService) GetMembers(
	context.Context, string,
) ([]circle.CircleMember, error) {
	return []circle.CircleMember{}, nil
}

func (s *lifecycleCircleService) IsMember(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (s *lifecycleCircleService) RemoveMember(_ context.Context, _, _, _ string, _ string) error {
	return nil
}

func (s *lifecycleCircleService) ProcessMissedContributions(_ context.Context, _ string, _ int) error {
	return nil
}

func (s *lifecycleCircleService) RaiseDispute(_ context.Context, _, _ string, _ circle.DisputeInput) (*circle.CircleDispute, error) {
	return nil, nil
}

func (s *lifecycleCircleService) CastVote(_ context.Context, _, _ string, _ circle.VoteInput) (*circle.CircleVote, bool, string, error) {
	return nil, false, "", nil
}

func (s *lifecycleCircleService) SubmitAuctionBid(_ context.Context, _, _ string, _ circle.AuctionBidInput) (*circle.CircleAuctionBid, error) {
	return nil, nil
}

type lifecycleContributionService struct{ store *lifecycleStore }

func (s *lifecycleContributionService) Record(
	_ context.Context, input contribution.RecordInput,
) (*contribution.Contribution, error) {
	record := contribution.Contribution{
		ID: uuid.New(), CircleID: uuid.MustParse(input.CircleID),
		UserID: uuid.MustParse(input.UserID), RoundNumber: input.RoundNumber,
		Amount: input.Amount, Status: contribution.StatusPending,
	}
	s.store.mu.Lock()
	s.store.contributions = append(s.store.contributions, record)
	s.store.mu.Unlock()
	return &record, nil
}

func (s *lifecycleContributionService) GetUserHistory(
	context.Context, string, int, int,
) ([]contribution.Contribution, int, error) {
	return s.store.contributions, len(s.store.contributions), nil
}

func (s *lifecycleContributionService) UpdateVerification(
	_ context.Context, _ string, _ bool, _ contribution.VerificationStatus,
) error {
	return nil
}

func (s *lifecycleContributionService) GetCircleHistory(
	context.Context, string, int, int,
) ([]contribution.Contribution, int, error) {
	return s.store.contributions, len(s.store.contributions), nil
}

type lifecyclePayoutService struct{ store *lifecycleStore }

func (s *lifecyclePayoutService) Record(
	_ context.Context, input payout.RecordInput,
) (*payout.Payout, error) {
	record := payout.Payout{
		ID: uuid.New(), CircleID: uuid.MustParse(input.CircleID),
		RecipientID: uuid.MustParse(input.RecipientID), RoundNumber: input.RoundNumber,
		Amount: input.Amount, FeeAmount: input.FeeAmount, PayoutType: input.PayoutType,
		CreatedAt: time.Now().UTC(),
	}
	s.store.mu.Lock()
	s.store.payouts = append(s.store.payouts, record)
	s.store.mu.Unlock()
	return &record, nil
}

func (s *lifecyclePayoutService) UpdateVerification(
	_ context.Context, _ string, _ bool, _ payout.VerificationStatus,
) error {
	return nil
}

func (s *lifecyclePayoutService) GetUserHistory(
	context.Context, string, int, int,
) ([]payout.Payout, int, error) {
	return s.store.payouts, len(s.store.payouts), nil
}

func (s *lifecyclePayoutService) GetCircleHistory(
	context.Context, string, int, int,
) ([]payout.Payout, int, error) {
	return s.store.payouts, len(s.store.payouts), nil
}

func lifecycleRequest(
	t *testing.T, router http.Handler, method, path, userID string, body any,
) (int, map[string]any) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-User", userID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder.Code, response
}

func TestCircleLifecycleEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	t.Cleanup(store.reset)
	organizer, member := uuid.New(), uuid.New()
	circleService := &lifecycleCircleService{store: store}
	contributionService := &lifecycleContributionService{store: store}
	payoutService := &lifecyclePayoutService{store: store}
	h := handler.NewCircleHandler(circleService, nil, contributionService, payoutService)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/join", h.JoinCircle)
	router.POST("/circles/:id/start", h.StartCircle)
	router.POST("/circles/:id/contribute", h.Contribute)
	router.POST("/circles/:id/payout", h.TriggerPayout)
	router.POST("/circles/:id/close", h.CloseCircle)

	code, response := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Lifecycle Circle", "circleType": "public", "payoutType": "fixed",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 2, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)
	circleData := response["data"].(map[string]any)["circle"].(map[string]any)
	circleID := circleData["id"].(string)
	assert.Equal(t, "pending", circleData["status"])

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/join", member.String(), map[string]any{})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, circle.CircleStatusPending, store.circle.Status)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/start", organizer.String(), nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, circle.CircleStatusActive, store.circle.Status)

	code, response = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/contribute", member.String(), map[string]any{
		"amount": 100, "txnHash": "contribution-hash", "roundNumber": 1,
	})
	require.Equal(t, http.StatusCreated, code)
	contributionData := response["data"].(map[string]any)["contribution"].(map[string]any)
	assert.Equal(t, "pending", contributionData["status"])
	assert.Equal(t, circle.CircleStatusActive, store.circle.Status)

	code, response = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/payout", organizer.String(), map[string]any{
		"recipientId": member.String(), "roundNumber": 1, "amount": 100,
		"feeAmount": 0, "txnHash": "payout-hash", "payoutType": "fixed",
	})
	require.Equal(t, http.StatusCreated, code)
	assert.Equal(t, "active", response["data"].(map[string]any)["status"])
	require.Len(t, store.payouts, 1)

	code, response = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/close", organizer.String(), nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "completed", response["data"].(map[string]any)["status"])
	assert.Equal(t, circle.CircleStatusCompleted, store.circle.Status)
}

func TestCircleLifecycle_DisputeEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer := uuid.New()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/dispute", h.Dispute)

	code, response := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Dispute Circle", "circleType": "public", "payoutType": "random",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)
	circleID := response["data"].(map[string]any)["circle"].(map[string]any)["id"].(string)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/dispute", organizer.String(), map[string]any{
		"reason":  "Suspicious activity",
		"details": "Member X did not contribute",
	})
	assert.Equal(t, http.StatusCreated, code)
}

func TestCircleLifecycle_DisputeEndpoint_MissingReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer := uuid.New()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/dispute", h.Dispute)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Dispute Circle", "circleType": "public", "payoutType": "random",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+store.circle.ID.String()+"/dispute", organizer.String(), map[string]any{
		"details": "Missing reason",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

func TestCircleLifecycle_VoteEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer, member := uuid.New(), uuid.New()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/vote", h.Vote)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Vote Circle", "circleType": "public", "payoutType": "vote",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+store.circle.ID.String()+"/vote", member.String(), map[string]any{
		"recipientId": organizer.String(),
	})
	assert.Equal(t, http.StatusOK, code)
}

func TestCircleLifecycle_VoteEndpoint_MissingRecipient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer := uuid.New()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/vote", h.Vote)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Vote Circle", "circleType": "public", "payoutType": "vote",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+store.circle.ID.String()+"/vote", organizer.String(), map[string]any{})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

func TestCircleLifecycle_AuctionBidEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer, bidder := uuid.New(), uuid.New()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/auction-bid", h.AuctionBid)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Auction Circle", "circleType": "public", "payoutType": "auction",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+store.circle.ID.String()+"/auction-bid", bidder.String(), map[string]any{
		"bidAmount": 150,
	})
	assert.Equal(t, http.StatusCreated, code)
}

func TestCircleLifecycle_AuctionBidEndpoint_MissingAmount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer := uuid.New()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/auction-bid", h.AuctionBid)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Auction Circle", "circleType": "public", "payoutType": "auction",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+store.circle.ID.String()+"/auction-bid", organizer.String(), map[string]any{})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

func TestCircleLifecycle_Contribute_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer := uuid.New()

	circleService := &lifecycleCircleService{store: store}
	contributionService := &lifecycleContributionService{store: store}
	h := handler.NewCircleHandler(circleService, nil, contributionService, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/contribute", h.Contribute)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Contrib Circle", "circleType": "public", "payoutType": "random",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 5, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+store.circle.ID.String()+"/contribute", organizer.String(), map[string]any{})
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

func TestCircleLifecycle_TriggerPayout_NonOrganizer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()
	organizer, member := uuid.New(), uuid.New()

	circleService := &lifecycleCircleService{store: store}
	payoutService := &lifecyclePayoutService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, payoutService)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	router.POST("/circles", h.CreateCircle)
	router.POST("/circles/:id/join", h.JoinCircle)
	router.POST("/circles/:id/start", h.StartCircle)
	router.POST("/circles/:id/payout", h.TriggerPayout)

	code, _ := lifecycleRequest(t, router, http.MethodPost, "/circles", organizer.String(), map[string]any{
		"name": "Payout Circle", "circleType": "public", "payoutType": "fixed",
		"contributionAmount": 100, "currency": "USDC", "frequency": "weekly",
		"maxMembers": 2, "maxStrikes": 3,
	})
	require.Equal(t, http.StatusCreated, code)
	circleID := store.circle.ID.String()

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/join", member.String(), map[string]any{})
	require.Equal(t, http.StatusOK, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/start", organizer.String(), nil)
	require.Equal(t, http.StatusOK, code)

	code, _ = lifecycleRequest(t, router, http.MethodPost, "/circles/"+circleID+"/payout", member.String(), map[string]any{
		"recipientId": member.String(), "roundNumber": 1, "amount": 100,
		"feeAmount": 0, "txnHash": "payout-hash", "payoutType": "fixed",
	})
	assert.Equal(t, http.StatusForbidden, code)
}

func TestCircleLifecycle_GetCircle_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.GET("/circles/:id", h.GetCircle)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCircleLifecycle_GetMembers_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newLifecycleStore()

	circleService := &lifecycleCircleService{store: store}
	h := handler.NewCircleHandler(circleService, nil, nil, nil)

	router := gin.New()
	router.GET("/circles/:id/members", h.GetMembers)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/circles/"+uuid.New().String()+"/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"members":[]`)
}
