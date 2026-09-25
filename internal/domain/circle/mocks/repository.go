package mocks

import (
	"context"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/domain/circle"
)

type Repository struct {
	mock.Mock
}

func (m *Repository) FindByID(ctx context.Context, id uuid.UUID) (*circle.Circle, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*circle.Circle), args.Error(1)
}

func (m *Repository) FindByContractID(ctx context.Context, contractID string) (*circle.Circle, error) {
	args := m.Called(ctx, contractID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*circle.Circle), args.Error(1)
}

func (m *Repository) List(ctx context.Context, filter circle.CircleFilter) ([]circle.Circle, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.Circle), args.Error(1)
}

func (m *Repository) Count(ctx context.Context, filter circle.CircleFilter) (int, error) {
	args := m.Called(ctx, filter)
	return args.Int(0), args.Error(1)
}

func (m *Repository) Create(ctx context.Context, c *circle.Circle) error {
	return m.Called(ctx, c).Error(0)
}

func (m *Repository) Update(ctx context.Context, c *circle.Circle) error {
	return m.Called(ctx, c).Error(0)
}

func (m *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}

func (m *Repository) CreateMember(ctx context.Context, cm *circle.CircleMember) error {
	return m.Called(ctx, cm).Error(0)
}

func (m *Repository) GetMembers(ctx context.Context, circleID uuid.UUID) ([]circle.CircleMember, error) {
	args := m.Called(ctx, circleID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.CircleMember), args.Error(1)
}

func (m *Repository) GetMemberCount(ctx context.Context, circleID uuid.UUID) (int, error) {
	args := m.Called(ctx, circleID)
	return args.Int(0), args.Error(1)
}

func (m *Repository) UpdateMemberStatus(ctx context.Context, circleID, userID uuid.UUID, status circle.MemberStatus) error {
	return m.Called(ctx, circleID, userID, status).Error(0)
}

func (m *Repository) FindMemberByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*circle.CircleMember, error) {
	args := m.Called(ctx, circleID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*circle.CircleMember), args.Error(1)
}

func (m *Repository) IsMember(ctx context.Context, circleID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, circleID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *Repository) FindCirclesByUserID(ctx context.Context, userID uuid.UUID) ([]circle.Circle, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.Circle), args.Error(1)
}

func (m *Repository) CreatePenalty(ctx context.Context, p *circle.Penalty) error {
	return m.Called(ctx, p).Error(0)
}

func (m *Repository) GetPenaltiesByCircle(ctx context.Context, circleID uuid.UUID) ([]circle.Penalty, error) {
	args := m.Called(ctx, circleID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.Penalty), args.Error(1)
}

func (m *Repository) GetPenaltiesByUser(ctx context.Context, userID uuid.UUID) ([]circle.Penalty, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.Penalty), args.Error(1)
}

func (m *Repository) GetContributionsByCircleAndRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]uuid.UUID, error) {
	args := m.Called(ctx, circleID, roundNumber)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]uuid.UUID), args.Error(1)
}

func (m *Repository) CreateDispute(ctx context.Context, dispute *circle.CircleDispute) error {
	return m.Called(ctx, dispute).Error(0)
}

func (m *Repository) CreateVote(ctx context.Context, vote *circle.CircleVote) error {
	return m.Called(ctx, vote).Error(0)
}

func (m *Repository) GetVotesByRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]circle.CircleVote, error) {
	args := m.Called(ctx, circleID, roundNumber)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.CircleVote), args.Error(1)
}

func (m *Repository) CreateAuctionBid(ctx context.Context, bid *circle.CircleAuctionBid) error {
	return m.Called(ctx, bid).Error(0)
}

func (m *Repository) GetAuctionBidsByRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]circle.CircleAuctionBid, error) {
	args := m.Called(ctx, circleID, roundNumber)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]circle.CircleAuctionBid), args.Error(1)
}

func (m *Repository) SaveRoundConfigSnapshot(ctx context.Context, snapshot *circle.RoundConfigSnapshot) error {
	return m.Called(ctx, snapshot).Error(0)
}

func (m *Repository) GetRoundConfigSnapshot(ctx context.Context, circleID uuid.UUID, roundNumber int) (*circle.RoundConfigSnapshot, error) {
	args := m.Called(ctx, circleID, roundNumber)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*circle.RoundConfigSnapshot), args.Error(1)
}

