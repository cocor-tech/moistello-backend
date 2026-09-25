package circle

import (
	"context"

	"github.com/google/uuid"
)

type CircleFilter struct {
	Search      string
	Status      CircleStatus
	Type        CircleType
	Page        int
	Limit       int
	ExcludeIDs  []uuid.UUID
	CommunityID *uuid.UUID
	OrganizerID *uuid.UUID
}

type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*Circle, error)
	FindByContractID(ctx context.Context, contractID string) (*Circle, error)
	List(ctx context.Context, filter CircleFilter) ([]Circle, error)
	Count(ctx context.Context, filter CircleFilter) (int, error)
	Create(ctx context.Context, c *Circle) error
	Update(ctx context.Context, c *Circle) error
	Delete(ctx context.Context, id uuid.UUID) error
	CreateMember(ctx context.Context, m *CircleMember) error
	GetMembers(ctx context.Context, circleID uuid.UUID) ([]CircleMember, error)
	GetMemberCount(ctx context.Context, circleID uuid.UUID) (int, error)
	UpdateMemberStatus(ctx context.Context, circleID, userID uuid.UUID, status MemberStatus) error
	FindMemberByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*CircleMember, error)
	FindCirclesByUserID(ctx context.Context, userID uuid.UUID) ([]Circle, error)

	CreatePenalty(ctx context.Context, p *Penalty) error
	GetPenaltiesByCircle(ctx context.Context, circleID uuid.UUID) ([]Penalty, error)
	GetPenaltiesByUser(ctx context.Context, userID uuid.UUID) ([]Penalty, error)
	GetContributionsByCircleAndRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]uuid.UUID, error)
	CreateDispute(ctx context.Context, dispute *CircleDispute) error
	CreateVote(ctx context.Context, vote *CircleVote) error
	GetVotesByRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]CircleVote, error)
	CreateAuctionBid(ctx context.Context, bid *CircleAuctionBid) error
	GetAuctionBidsByRound(ctx context.Context, circleID uuid.UUID, roundNumber int) ([]CircleAuctionBid, error)
	SaveRoundConfigSnapshot(ctx context.Context, snapshot *RoundConfigSnapshot) error
	GetRoundConfigSnapshot(ctx context.Context, circleID uuid.UUID, roundNumber int) (*RoundConfigSnapshot, error)
}
