package indexer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/circle"
	circleMocks "github.com/moistello/backend/internal/domain/circle/mocks"
	contribMocks "github.com/moistello/backend/internal/domain/contribution/mocks"
	payoutMocks "github.com/moistello/backend/internal/domain/payout/mocks"
	"github.com/moistello/backend/internal/domain/reputation"
	reputationMocks "github.com/moistello/backend/internal/domain/reputation/mocks"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/pkg/apperrors"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var errTestNotFound = errors.New("not found")

func newTestProcessor(
	circleRepo *circleMocks.Repository,
	contribRepo *contribMocks.Repository,
	payoutRepo *payoutMocks.Repository,
	repRepo *reputationMocks.Repository,
	userRepo *userMocks.Repository,
) *EventProcessor {
	return &EventProcessor{
		db:             nil, // not needed for dispatch-level tests
		rmqClient:      nil,
		circleRepo:     circleRepo,
		contribRepo:    contribRepo,
		payoutRepo:     payoutRepo,
		reputationRepo: repRepo,
		userRepo:       userRepo,
	}
}

func testCircle(contractID string) *circle.Circle {
	return &circle.Circle{
		ID:         uuid.New(),
		ContractID: sql.NullString{String: contractID, Valid: contractID != ""},
		Status:     circle.CircleStatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func testUser(wallet string) *user.User {
	return &user.User{
		ID:            uuid.New(),
		WalletAddress: wallet,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func contractEvent(eventType, contractID string, payload map[string]any) *ContractEvent {
	return &ContractEvent{
		ContractID: contractID,
		EventType:  eventType,
		Ledger:     100,
		TxHash:     "txhash_" + eventType,
		Payload:    payload,
	}
}

// ---------------------------------------------------------------------------
// CircleCreated
// ---------------------------------------------------------------------------

func TestOnCircleCreated_AlreadyLinked(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, nil)

	ev := contractEvent(EventCircleCreated, "contract1", map[string]any{
		"circle_id": "contract1",
		"creator":   "GABC123",
	})

	c := testCircle("contract1")
	cRepo.On("FindByContractID", mock.Anything, "contract1").Return(c, nil)

	err := p.onCircleCreated(context.Background(), ev)
	assert.NoError(t, err)
	cRepo.AssertExpectations(t)
}

func TestOnCircleCreated_NotFound_LogsOnly(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, nil)

	ev := contractEvent(EventCircleCreated, "contract_new", map[string]any{
		"circle_id": "contract_new",
		"creator":   "GABC",
	})

	cRepo.On("FindByContractID", mock.Anything, "contract_new").Return(nil, errTestNotFound)

	err := p.onCircleCreated(context.Background(), ev)
	assert.NoError(t, err)
	cRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// MemberJoined
// ---------------------------------------------------------------------------

func TestOnMemberJoined_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET1")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	cRepo.On("CreateMember", mock.Anything, mock.MatchedBy(func(m *circle.CircleMember) bool {
		return m.CircleID == c.ID && m.UserID == u.ID && m.Status == circle.MemberStatusActive
	})).Return(nil)

	ev := contractEvent(EventMemberJoined, "cid1", map[string]any{
		"circle_id":           "cid1",
		"member":              "GWALLET1",
		"contribution_amount": float64(100),
	})

	err := p.onMemberJoined(context.Background(), ev)
	assert.NoError(t, err)
	cRepo.AssertExpectations(t)
	uRepo.AssertExpectations(t)
}

func TestOnMemberJoined_CircleNotFound(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, uRepo)

	cRepo.On("FindByContractID", mock.Anything, "cid_missing").Return(nil, errTestNotFound)

	ev := contractEvent(EventMemberJoined, "cid_missing", map[string]any{
		"circle_id": "cid_missing",
		"member":    "GWALLET1",
	})

	err := p.onMemberJoined(context.Background(), ev)
	assert.NoError(t, err) // graceful skip
	cRepo.AssertExpectations(t)
	uRepo.AssertNotCalled(t, "FindByWalletAddress")
}

func TestOnMemberJoined_UserNotFound(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, uRepo)

	c := testCircle("cid1")
	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET_MISSING").Return(nil, errTestNotFound)

	ev := contractEvent(EventMemberJoined, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET_MISSING",
	})

	err := p.onMemberJoined(context.Background(), ev)
	assert.NoError(t, err) // graceful skip
}

// ---------------------------------------------------------------------------
// ContributionReceived
// ---------------------------------------------------------------------------

func TestOnContributionReceived_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	ctRepo := &contribMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, ctRepo, nil, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET1")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	ctRepo.On("Create", mock.Anything, mock.AnythingOfType("*contribution.Contribution")).Return(nil)
	cRepo.On("Update", mock.Anything, mock.AnythingOfType("*circle.Circle")).Return(nil)

	ev := contractEvent(EventContributionReceived, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET1",
		"amount":    float64(50),
		"round":     int(1),
	})

	err := p.onContributionReceived(context.Background(), ev)
	assert.NoError(t, err)
	ctRepo.AssertCalled(t, "Create", mock.Anything, mock.AnythingOfType("*contribution.Contribution"))
}

// ---------------------------------------------------------------------------
// PayoutExecuted
// ---------------------------------------------------------------------------

func TestOnPayoutExecuted_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	pRepo := &payoutMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, pRepo, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET_RECIPIENT")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET_RECIPIENT").Return(u, nil)
	pRepo.On("Create", mock.Anything, mock.AnythingOfType("*payout.Payout")).Return(nil)
	cRepo.On("Update", mock.Anything, mock.AnythingOfType("*circle.Circle")).Return(nil)

	ev := contractEvent(EventPayoutExecuted, "cid1", map[string]any{
		"circle_id":   "cid1",
		"recipient":   "GWALLET_RECIPIENT",
		"amount":      float64(500),
		"round":       int(2),
		"payout_type": "random",
	})

	err := p.onPayoutExecuted(context.Background(), ev)
	assert.NoError(t, err)
	pRepo.AssertCalled(t, "Create", mock.Anything, mock.AnythingOfType("*payout.Payout"))
}

// ---------------------------------------------------------------------------
// LateReported
// ---------------------------------------------------------------------------

func TestOnLateReported_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET1")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)

	ev := contractEvent(EventLateReported, "cid1", map[string]any{
		"circle_id":      "cid1",
		"member":         "GWALLET1",
		"penalty_amount": float64(5),
		"strikes":        int(1),
	})

	err := p.onLateReported(context.Background(), ev)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// MemberExited
// ---------------------------------------------------------------------------

func TestOnMemberExited_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET1")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	cRepo.On("UpdateMemberStatus", mock.Anything, c.ID, u.ID, circle.MemberStatusExited).Return(nil)

	ev := contractEvent(EventMemberExited, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET1",
		"penalty":   float64(10),
	})

	err := p.onMemberExited(context.Background(), ev)
	assert.NoError(t, err)
	cRepo.AssertCalled(t, "UpdateMemberStatus", mock.Anything, c.ID, u.ID, circle.MemberStatusExited)
}

// ---------------------------------------------------------------------------
// DefaultRecorded
// ---------------------------------------------------------------------------

func TestOnDefaultRecorded_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	rRepo := &reputationMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, rRepo, uRepo)

	u := testUser("GWALLET1")
	existing := &reputation.ReputationSnapshot{
		UserID: u.ID,
		Score:  600,
		Level:  "Platinum",
	}

	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	rRepo.On("GetByUser", mock.Anything, u.ID).Return(existing, nil)
	rRepo.On("SaveSnapshot", mock.Anything, mock.MatchedBy(func(s *reputation.ReputationSnapshot) bool {
		return s.Score == 550 && s.UserID == u.ID
	})).Return(nil)
	uRepo.On("UpdateMoiScore", mock.Anything, u.ID, 550).Return(nil)

	ev := contractEvent(EventDefaultRecorded, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET1",
	})

	err := p.onDefaultRecorded(context.Background(), ev)
	assert.NoError(t, err)
	rRepo.AssertExpectations(t)
}

func TestOnDefaultRecorded_ScoreFloorsAtZero(t *testing.T) {
	rRepo := &reputationMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(nil, nil, nil, rRepo, uRepo)

	u := testUser("GWALLET1")
	existing := &reputation.ReputationSnapshot{
		UserID: u.ID,
		Score:  30, // < 50, so penalty should floor at 0
		Level:  "Bronze",
	}

	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	rRepo.On("GetByUser", mock.Anything, u.ID).Return(existing, nil)
	rRepo.On("SaveSnapshot", mock.Anything, mock.MatchedBy(func(s *reputation.ReputationSnapshot) bool {
		return s.Score == 0
	})).Return(nil)
	uRepo.On("UpdateMoiScore", mock.Anything, u.ID, 0).Return(nil)

	ev := contractEvent(EventDefaultRecorded, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET1",
	})

	err := p.onDefaultRecorded(context.Background(), ev)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// CircleCompleted
// ---------------------------------------------------------------------------

func TestOnCircleCompleted_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, nil)

	c := testCircle("cid1")
	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	cRepo.On("Update", mock.Anything, mock.MatchedBy(func(upd *circle.Circle) bool {
		return upd.Status == circle.CircleStatusCompleted && upd.TotalContributions == 1000
	})).Return(nil)

	ev := contractEvent(EventCircleCompleted, "cid1", map[string]any{
		"circle_id":           "cid1",
		"total_contributions": float64(1000),
	})

	err := p.onCircleCompleted(context.Background(), ev)
	assert.NoError(t, err)
	cRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// AuctionBid — log + broadcast only, no repo calls
// ---------------------------------------------------------------------------

func TestOnAuctionBid_NoError(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)

	ev := contractEvent(EventAuctionBid, "cid1", map[string]any{
		"circle_id":     "cid1",
		"bidder":        "GBIDDER",
		"discount_bips": int(200),
		"round":         int(3),
	})

	err := p.onAuctionBid(context.Background(), ev)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// VoteCast — log + broadcast only
// ---------------------------------------------------------------------------

func TestOnVoteCast_NoError(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)

	ev := contractEvent(EventVoteCast, "cid1", map[string]any{
		"circle_id": "cid1",
		"voter":     "GVOTER",
		"vote_for":  "GCANDIDATE",
		"round":     int(1),
	})

	err := p.onVoteCast(context.Background(), ev)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// DisputeRaised
// ---------------------------------------------------------------------------

func TestOnDisputeRaised_Success(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, nil)

	c := testCircle("cid1")
	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	cRepo.On("Update", mock.Anything, mock.MatchedBy(func(upd *circle.Circle) bool {
		return string(upd.Status) == "disputed"
	})).Return(nil)

	ev := contractEvent(EventDisputeRaised, "cid1", map[string]any{
		"circle_id":     "cid1",
		"member":        "GDISPUTER",
		"evidence_hash": "sha256hash",
	})

	err := p.onDisputeRaised(context.Background(), ev)
	assert.NoError(t, err)
	cRepo.AssertExpectations(t)
}

func TestOnDisputeRaised_CircleNotFound(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, nil)

	cRepo.On("FindByContractID", mock.Anything, "cid_missing").Return(nil, errTestNotFound)

	ev := contractEvent(EventDisputeRaised, "cid_missing", map[string]any{
		"circle_id": "cid_missing",
		"member":    "GMEMBER",
	})

	err := p.onDisputeRaised(context.Background(), ev)
	assert.NoError(t, err) // graceful skip
}

// ---------------------------------------------------------------------------
// FeeDeposited — log + broadcast only
// ---------------------------------------------------------------------------

func TestOnFeeDeposited_NoError(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)

	ev := contractEvent(EventFeeDeposited, "treasury_contract", map[string]any{
		"circle_id": "cid1",
		"amount":    float64(2.5),
	})

	err := p.onFeeDeposited(context.Background(), ev)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// dispatchEvent routing
// ---------------------------------------------------------------------------

func TestDispatchEvent_UnknownType_NoError(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)

	ev := contractEvent("UnknownEventXYZ", "cid1", nil)
	err := p.dispatchEvent(context.Background(), ev)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ProcessTransaction — integration path
// ---------------------------------------------------------------------------

func TestProcessTransaction_EmptyOperations(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)
	txn := &Transaction{Hash: "abc", Ledger: 1}
	err := p.ProcessTransaction(context.Background(), txn)
	assert.NoError(t, err)
}

func TestProcessTransaction_NonSorobanOp(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)
	txn := &Transaction{
		Hash:   "abc",
		Ledger: 1,
		Operations: []Operation{
			{Type: "payment", SourceAccount: "GACCOUNT"},
		},
	}
	err := p.ProcessTransaction(context.Background(), txn)
	assert.NoError(t, err)
}

func TestProcessTransaction_ExtendTTL(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)
	txn := &Transaction{
		Hash:   "abc",
		Ledger: 1,
		Operations: []Operation{
			{Type: "extend_footprint_ttl", SourceAccount: "GCONTRACT"},
		},
	}
	err := p.ProcessTransaction(context.Background(), txn)
	assert.NoError(t, err)
}

func TestProcessTransaction_SorobanEmptyXDR(t *testing.T) {
	// invoke_host_function with empty ResultMetaXDR → no events, no error.
	p := newTestProcessor(nil, nil, nil, nil, nil)
	txn := &Transaction{
		Hash:   "abc",
		Ledger: 1,
		Operations: []Operation{
			{Type: "invoke_host_function", SourceAccount: "GCONTRACT", ResultMetaXDR: ""},
		},
	}
	err := p.ProcessTransaction(context.Background(), txn)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// WebSocket broadcast
// ---------------------------------------------------------------------------

func TestBroadcast_CallsWsBroadcast(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)

	called := false
	var capturedCircleID string
	p.SetWebSocketBroadcast(func(circleID string, data any) {
		called = true
		capturedCircleID = circleID
	})

	p.Broadcast(context.Background(), "circle-123", "circle.created", map[string]any{})

	assert.True(t, called)
	assert.Equal(t, "circle-123", capturedCircleID)
}

// ---------------------------------------------------------------------------
// Idempotent reprocessing
// ---------------------------------------------------------------------------

func TestOnContributionReceived_Reprocessed_IsNoOp(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	ctRepo := &contribMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, ctRepo, nil, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET1")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	ctRepo.On("Create", mock.Anything, mock.AnythingOfType("*contribution.Contribution")).Return(apperrors.ErrConflict)

	ev := contractEvent(EventContributionReceived, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET1",
		"amount":    float64(50),
		"round":     int(1),
	})

	assert.NoError(t, p.onContributionReceived(context.Background(), ev))
	assert.Zero(t, c.TotalContributions, "aggregate must not change on replay")
	cRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestOnPayoutExecuted_Reprocessed_IsNoOp(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	pRepo := &payoutMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, pRepo, nil, uRepo)

	c := testCircle("cid1")
	c.CurrentRound = 2
	u := testUser("GWALLET_RECIPIENT")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET_RECIPIENT").Return(u, nil)
	pRepo.On("Create", mock.Anything, mock.AnythingOfType("*payout.Payout")).Return(apperrors.ErrConflict)

	ev := contractEvent(EventPayoutExecuted, "cid1", map[string]any{
		"circle_id":   "cid1",
		"recipient":   "GWALLET_RECIPIENT",
		"amount":      float64(500),
		"round":       int(1),
		"payout_type": "random",
	})

	assert.NoError(t, p.onPayoutExecuted(context.Background(), ev))
	assert.Equal(t, 2, c.CurrentRound, "round must not move on replay")
	cRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestOnMemberJoined_Reprocessed_IsNoOp(t *testing.T) {
	cRepo := &circleMocks.Repository{}
	uRepo := &userMocks.Repository{}
	p := newTestProcessor(cRepo, nil, nil, nil, uRepo)

	c := testCircle("cid1")
	u := testUser("GWALLET1")

	cRepo.On("FindByContractID", mock.Anything, "cid1").Return(c, nil)
	uRepo.On("FindByWalletAddress", mock.Anything, "GWALLET1").Return(u, nil)
	cRepo.On("CreateMember", mock.Anything, mock.AnythingOfType("*circle.CircleMember")).Return(circle.ErrAlreadyMember)

	ev := contractEvent(EventMemberJoined, "cid1", map[string]any{
		"circle_id": "cid1",
		"member":    "GWALLET1",
	})

	assert.NoError(t, p.onMemberJoined(context.Background(), ev))
}

func TestEventRecorded(t *testing.T) {
	mockDB, sqlMock, err := sqlmock.New()
	assert.NoError(t, err)
	defer mockDB.Close()

	p := &EventProcessor{db: sqlx.NewDb(mockDB, "sqlmock")}
	ev := contractEvent(EventDefaultRecorded, "cid1", nil)

	sqlMock.ExpectQuery("SELECT EXISTS").
		WithArgs(ev.TxHash, ev.ContractID, ev.EventType).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	assert.True(t, p.eventRecorded(context.Background(), ev), "event already in the log must be skipped")

	sqlMock.ExpectQuery("SELECT EXISTS").
		WithArgs(ev.TxHash, ev.ContractID, ev.EventType).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	assert.False(t, p.eventRecorded(context.Background(), ev))

	assert.False(t, (&EventProcessor{}).eventRecorded(context.Background(), ev), "no database means nothing is recorded")
	assert.NoError(t, sqlMock.ExpectationsWereMet())
}
