package circle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/moistello/backend/internal/domain/audit"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/pkg/apperrors"
)

type Service interface {
	Get(ctx context.Context, id string) (*Circle, error)
	List(ctx context.Context, filter CircleFilter) ([]Circle, int, error)
	Create(ctx context.Context, organizerID string, input CreateCircleInput) (*Circle, error)
	Update(ctx context.Context, id, userID string, input UpdateCircleInput) (*Circle, error)
	Start(ctx context.Context, id, userID string) error
	Close(ctx context.Context, id, userID string) error
	Cancel(ctx context.Context, id, userID string) error
	Join(ctx context.Context, circleID, userID string, inviteCode string) error
	Exit(ctx context.Context, circleID, userID string) error
	GetMembers(ctx context.Context, circleID string) ([]CircleMember, error)
	IsMember(ctx context.Context, circleID, userID string) (bool, error)
	RemoveMember(ctx context.Context, circleID, callerID, memberAddress string, reason string) error
	ProcessMissedContributions(ctx context.Context, circleID string, roundNumber int) error
	RaiseDispute(ctx context.Context, circleID, userID string, input DisputeInput) (*CircleDispute, error)
	CastVote(ctx context.Context, circleID, userID string, input VoteInput) (*CircleVote, bool, string, error)
	SubmitAuctionBid(ctx context.Context, circleID, userID string, input AuctionBidInput) (*CircleAuctionBid, error)
}

type UserMOIFetcher interface {
	FindByID(ctx context.Context, id uuid.UUID) (*UserMOIData, error)
}

type UserMOIData struct {
	MoiScore int
}

type Broadcaster interface {
	CircleCreated(ctx context.Context, circleID, organizerID string)
	CircleStatusChanged(ctx context.Context, circleID, status string)
	MemberJoined(ctx context.Context, circleID, userID string)
	MemberLeft(ctx context.Context, circleID, userID string)
	ContributionRecorded(ctx context.Context, circleID, userID string, roundNumber int, amount float64)
	MemberPenalized(ctx context.Context, circleID, userID string, roundNumber int, penaltyAmount float64)
}

type CommunityMembershipChecker interface {
	IsMember(ctx context.Context, communityID, userID uuid.UUID) (bool, error)
}

type Transactor interface {
	WithTransaction(ctx context.Context, fn func(repo Repository) error) error
}

type CreateCircleInput struct {
	Name               string          `json:"name" validate:"required,min=3,max=100"`
	Description        string          `json:"description"`
	CommunityID        string          `json:"communityId"`
	CircleType         CircleType      `json:"circleType" validate:"required,oneof=public private community premium"`
	PayoutType         PayoutType      `json:"payoutType" validate:"required,oneof=random fixed auction vote"`
	ContributionAmount float64         `json:"contributionAmount" validate:"required,gt=0"`
	Currency           CircleCurrency  `json:"currency" validate:"required,oneof=USDC XLM"`
	Frequency          CircleFrequency `json:"frequency" validate:"required,oneof=daily weekly biweekly monthly"`
	MaxMembers         int             `json:"maxMembers" validate:"required,gte=2,lte=100"`
	MinMoiScore        int             `json:"minMoiScore" validate:"gte=0,lte=1000"`
	CollateralPercent  float64         `json:"collateralPercent" validate:"gte=0,lte=100"`
	LateFeePercent     float64         `json:"lateFeePercent" validate:"gte=0,lte=100"`
	GracePeriodHours   int             `json:"gracePeriodHours" validate:"gte=0"`
	MaxStrikes         int             `json:"maxStrikes" validate:"gte=1,lte=10"`
	RequiresInvite     bool            `json:"requiresInvite"`
}

type UpdateCircleInput struct {
	Name               *string          `json:"name"`
	Description        *string          `json:"description"`
	ContributionAmount *float64         `json:"contributionAmount"`
	Currency           *CircleCurrency  `json:"currency"`
	Frequency          *CircleFrequency `json:"frequency"`
	MaxMembers         *int             `json:"maxMembers"`
	MinMoiScore        *int             `json:"minMoiScore"`
	CollateralPercent  *float64         `json:"collateralPercent"`
	LateFeePercent     *float64         `json:"lateFeePercent"`
	GracePeriodHours   *int             `json:"gracePeriodHours"`
	MaxStrikes         *int             `json:"maxStrikes"`
}

type AuditLogger interface {
	Create(ctx context.Context, entry *audit.AuditEntry) error
}

type NotificationSender interface {
	Create(ctx context.Context, input notification.CreateInput) (*notification.Notification, error)
}

// Dependencies holds the optional collaborators injected into the circle
// service. All fields are optional; nil values are safely handled at call sites.
type Dependencies struct {
	CommunityChecker CommunityMembershipChecker
	Broadcaster      Broadcaster
	Transactor       Transactor
	AuditLogger      AuditLogger
	NotificationSvc  NotificationSender
}

type circleService struct {
	repo             Repository
	userRepo         UserMOIFetcher
	communityChecker CommunityMembershipChecker
	broadcaster      Broadcaster
	tx               Transactor
	auditRepo        AuditLogger
	notificationSvc  NotificationSender
}

func NewService(repo Repository, userRepo UserMOIFetcher, deps Dependencies) Service {
	return &circleService{
		repo:             repo,
		userRepo:         userRepo,
		communityChecker: deps.CommunityChecker,
		broadcaster:      deps.Broadcaster,
		tx:               deps.Transactor,
		auditRepo:        deps.AuditLogger,
		notificationSvc:  deps.NotificationSvc,
	}
}

type circleTransactor struct {
	db *sqlx.DB
}

func NewTransactor(db *sqlx.DB) Transactor {
	return &circleTransactor{db: db}
}

func (t *circleTransactor) WithTransaction(ctx context.Context, fn func(repo Repository) error) error {
	tx, err := t.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(NewRepositoryFromTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *circleService) withTransaction(ctx context.Context, fn func(repo Repository) error) error {
	if s.tx == nil {
		return fn(s.repo)
	}
	return s.tx.WithTransaction(ctx, fn)
}

func parseCommunityID(id string) *uuid.UUID {
	if id == "" {
		return nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseUUID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, ErrInvalidUUID
	}
	return parsed, nil
}

func (s *circleService) Get(ctx context.Context, id string) (*Circle, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	c, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		if err == ErrCircleNotFound {
			return nil, ErrCircleNotFound
		}
		return nil, fmt.Errorf("getting circle: %w", err)
	}
	return c, nil
}

func (s *circleService) List(ctx context.Context, filter CircleFilter) ([]Circle, int, error) {
	circles, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("listing circles: %w", err)
	}
	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("counting circles: %w", err)
	}
	return circles, total, nil
}

func (s *circleService) Update(ctx context.Context, id, userID string, input UpdateCircleInput) (*Circle, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	usrID, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}

	c, err := s.repo.FindByID(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("finding circle for update: %w", err)
	}

	if c.OrganizerID != usrID {
		return nil, ErrNotOrganizer
	}
	if c.Status != CircleStatusPending && c.Status != CircleStatusActive {
		return nil, ErrCircleNotActive
	}

	if input.Name != nil {
		c.Name = *input.Name
	}
	if input.Description != nil {
		if *input.Description == "" {
			c.Description = sql.NullString{}
		} else {
			c.Description = sql.NullString{String: *input.Description, Valid: true}
		}
	}
	if input.ContributionAmount != nil {
		c.ContributionAmount = *input.ContributionAmount
	}
	if input.Currency != nil {
		c.Currency = *input.Currency
	}
	if input.Frequency != nil {
		c.Frequency = *input.Frequency
	}
	if input.MaxMembers != nil {
		c.MaxMembers = *input.MaxMembers
	}
	if input.MinMoiScore != nil {
		c.MinMoiScore = *input.MinMoiScore
	}
	if input.CollateralPercent != nil {
		c.CollateralPercent = *input.CollateralPercent
	}
	if input.LateFeePercent != nil {
		c.LateFeePercent = *input.LateFeePercent
	}
	if input.GracePeriodHours != nil {
		c.GracePeriodHours = *input.GracePeriodHours
	}
	if input.MaxStrikes != nil {
		c.MaxStrikes = *input.MaxStrikes
	}
	c.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, c); err != nil {
		return nil, fmt.Errorf("updating circle: %w", err)
	}
	return c, nil
}

func (s *circleService) Create(ctx context.Context, organizerID string, input CreateCircleInput) (*Circle, error) {
	orgID, err := parseUUID(organizerID)
	if err != nil {
		return nil, err
	}

	if input.MaxMembers < 2 {
		return nil, ErrParticipantLimit
	}

	if input.CircleType == CircleTypePremium {
		org, err := s.userRepo.FindByID(ctx, orgID)
		if err != nil {
			return nil, fmt.Errorf("finding organizer for premium check: %w", err)
		}
		if org.MoiScore < 50 {
			return nil, fmt.Errorf("premium circles require at least 50 MoiScore")
		}
		if input.Currency == CurrencyUSDC && input.ContributionAmount < 50 {
			return nil, fmt.Errorf("premium circles require minimum 50 USDC contribution")
		}
		if input.Currency == CurrencyXLM && input.ContributionAmount < 100 {
			return nil, fmt.Errorf("premium circles require minimum 100 XLM contribution")
		}
	}

	now := time.Now().UTC()

	buildCircle := func() *Circle {
		c := &Circle{
			ID:                 uuid.New(),
			CommunityID:        parseCommunityID(input.CommunityID),
			Name:               input.Name,
			CircleType:         input.CircleType,
			PayoutType:         input.PayoutType,
			ContributionAmount: input.ContributionAmount,
			Currency:           input.Currency,
			Frequency:          input.Frequency,
			MaxMembers:         input.MaxMembers,
			MinMoiScore:        input.MinMoiScore,
			CollateralPercent:  input.CollateralPercent,
			LateFeePercent:     input.LateFeePercent,
			GracePeriodHours:   input.GracePeriodHours,
			MaxStrikes:         input.MaxStrikes,
			RequiresInvite:     input.RequiresInvite,
			Status:             CircleStatusPending,
			CurrentRound:       0,
			TotalContributions: 0,
			OrganizerID:        orgID,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if input.Description != "" {
			c.Description = sql.NullString{String: input.Description, Valid: true}
		}
		return c
	}

	var circle *Circle
	err = s.withTransaction(ctx, func(repo Repository) error {
		c := buildCircle()
		if err := repo.Create(ctx, c); err != nil {
			if err == apperrors.ErrConflict {
				return fmt.Errorf("circle name conflict: %w", err)
			}
			return fmt.Errorf("creating circle: %w", err)
		}
		member := &CircleMember{
			CircleID: c.ID,
			UserID:   orgID,
			Position: 1,
			Status:   MemberStatusActive,
			JoinedAt: now,
		}
		if err := repo.CreateMember(ctx, member); err != nil {
			return fmt.Errorf("adding organizer as member: %w", err)
		}
		circle = c
		return nil
	})
	if err == nil && s.broadcaster != nil && circle != nil {
		s.broadcaster.CircleCreated(ctx, circle.ID.String(), organizerID)
	}
	return circle, err
}

func (s *circleService) Start(ctx context.Context, id, userID string) error {
	cid, err := parseUUID(id)
	if err != nil {
		return err
	}
	usrID, err := parseUUID(userID)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return fmt.Errorf("finding circle for start: %w", err)
	}

	if c.OrganizerID != usrID {
		return ErrNotOrganizer
	}
	if c.Status != CircleStatusPending {
		return ErrCircleNotActive
	}

	count, err := s.repo.GetMemberCount(ctx, cid)
	if err != nil {
		return fmt.Errorf("checking member count: %w", err)
	}
	if count < 2 {
		return fmt.Errorf("need at least 2 members to start")
	}

	c.Status = CircleStatusActive
	c.CurrentRound = 1
	c.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, c); err != nil {
		return fmt.Errorf("activating circle: %w", err)
	}
	if s.broadcaster != nil {
		s.broadcaster.CircleStatusChanged(ctx, id, "active")
	}
	return nil
}

func (s *circleService) Cancel(ctx context.Context, id, userID string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	usrID, err := parseUUID(userID)
	if err != nil {
		return err
	}

	err = s.withTransaction(ctx, func(repo Repository) error {
		c, err := repo.FindByID(ctx, uid)
		if err != nil {
			return fmt.Errorf("finding circle for cancel: %w", err)
		}
		if c.OrganizerID != usrID {
			return ErrNotOrganizer
		}
		if c.Status != CircleStatusPending {
			return ErrCircleNotActive
		}
		c.Status = CircleStatusCancelled
		c.UpdatedAt = time.Now().UTC()
		if err := repo.Update(ctx, c); err != nil {
			return fmt.Errorf("cancelling circle: %w", err)
		}
		return nil
	})
	if err == nil && s.broadcaster != nil {
		s.broadcaster.CircleStatusChanged(ctx, id, "cancelled")
	}
	return err
}

// Close completes an active circle after its final payout. Only the organizer
// may close it; cancellation remains the separate pending-circle operation.
func (s *circleService) Close(ctx context.Context, id, userID string) error {
	cid, err := parseUUID(id)
	if err != nil {
		return err
	}
	organizerID, err := parseUUID(userID)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return fmt.Errorf("finding circle for close: %w", err)
	}
	if c.OrganizerID != organizerID {
		return ErrNotOrganizer
	}
	if c.Status != CircleStatusActive {
		return ErrCircleNotActive
	}
	c.Status = CircleStatusCompleted
	c.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, c); err != nil {
		return fmt.Errorf("closing circle: %w", err)
	}
	if s.broadcaster != nil {
		s.broadcaster.CircleStatusChanged(ctx, id, string(CircleStatusCompleted))
	}
	return nil
}

func (s *circleService) Join(ctx context.Context, circleID, userID string, inviteCode string) error {
	cid, err := parseUUID(circleID)
	if err != nil {
		return err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return fmt.Errorf("finding circle for join: %w", err)
	}

	if c.Status != CircleStatusPending && c.Status != CircleStatusActive {
		return ErrCircleNotActive
	}

	if c.CircleType == CircleTypePremium {
		joiner, err := s.userRepo.FindByID(ctx, uid)
		if err != nil {
			return fmt.Errorf("finding user for premium join check: %w", err)
		}
		if joiner.MoiScore < 50 {
			return fmt.Errorf("joining premium circles requires at least 50 MoiScore")
		}
	}

	if c.CircleType == "community" && c.CommunityID != nil {
		ok, err := s.communityChecker.IsMember(ctx, *c.CommunityID, uid)
		if err != nil {
			return fmt.Errorf("checking community membership: %w", err)
		}
		if !ok {
			return ErrNotCommunityMember
		}
	}

	if (c.CircleType == CircleTypePrivate || c.RequiresInvite) && inviteCode == "" {
		return ErrInvalidInvite
	}

	err = s.withTransaction(ctx, func(repo Repository) error {
		count, err := repo.GetMemberCount(ctx, cid)
		if err != nil {
			return fmt.Errorf("checking member count: %w", err)
		}
		if count >= c.MaxMembers {
			return ErrCircleFull
		}
		existing, err := repo.FindMemberByCircleAndUser(ctx, cid, uid)
		if err == nil && existing != nil {
			return ErrAlreadyMember
		}
		if err := repo.CreateMember(ctx, &CircleMember{
			CircleID: cid,
			UserID:   uid,
			Position: count + 1,
			Status:   MemberStatusActive,
			JoinedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("joining circle: %w", err)
		}
		return nil
	})
	if err == nil && s.broadcaster != nil {
		s.broadcaster.MemberJoined(ctx, circleID, userID)
	}
	return err
}

func (s *circleService) Exit(ctx context.Context, circleID, userID string) error {
	cid, err := parseUUID(circleID)
	if err != nil {
		return err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return fmt.Errorf("finding circle for exit: %w", err)
	}

	if c.OrganizerID == uid {
		return ErrNotOrganizer
	}

	member, err := s.repo.FindMemberByCircleAndUser(ctx, cid, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return ErrNotMember
		}
		return fmt.Errorf("finding member for exit: %w", err)
	}

	if member.Status != MemberStatusActive {
		return ErrNotMember
	}

	if c.Status == CircleStatusActive {
		penalty := CalculateEarlyExitPenalty(c.TotalContributions, c.ContributionAmount*c.CollateralPercent/100.0, 0)
		_ = penalty
	}

	if err := s.repo.UpdateMemberStatus(ctx, cid, uid, MemberStatusExited); err != nil {
		return fmt.Errorf("exiting circle: %w", err)
	}

	if s.broadcaster != nil {
		s.broadcaster.MemberLeft(ctx, circleID, userID)
	}

	return nil
}

func (s *circleService) GetMembers(ctx context.Context, circleID string) ([]CircleMember, error) {
	cid, err := parseUUID(circleID)
	if err != nil {
		return nil, err
	}

	_, err = s.repo.FindByID(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("finding circle for members: %w", err)
	}

	members, err := s.repo.GetMembers(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("getting members: %w", err)
	}
	return members, nil
}

// IsMember reports whether the user is an active member of the circle.
func (s *circleService) IsMember(ctx context.Context, circleID, userID string) (bool, error) {
	cid, err := parseUUID(circleID)
	if err != nil {
		return false, err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return false, err
	}

	member, err := s.repo.FindMemberByCircleAndUser(ctx, cid, uid)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return false, nil
		}
		return false, fmt.Errorf("checking circle membership: %w", err)
	}
	return member != nil && member.Status == MemberStatusActive, nil
}

// RemoveMember lets the circle organizer forcibly remove a member by their user ID.
// The member's stake redistribution is noted in the audit trail; actual on-chain
// redistribution is handled by the treasury contract and triggered via the emitted event.
func (s *circleService) RemoveMember(ctx context.Context, circleID, callerID, memberAddress string, reason string) error {
	cid, err := parseUUID(circleID)
	if err != nil {
		return err
	}
	callerUID, err := parseUUID(callerID)
	if err != nil {
		return err
	}
	memberUID, err := parseUUID(memberAddress)
	if err != nil {
		return err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return fmt.Errorf("finding circle: %w", err)
	}

	if c.OrganizerID != callerUID {
		return ErrNotOrganizer
	}

	if callerUID == memberUID {
		return fmt.Errorf("organizer cannot remove themselves; use cancel or close")
	}

	member, err := s.repo.FindMemberByCircleAndUser(ctx, cid, memberUID)
	if err != nil {
		if err == apperrors.ErrNotFound {
			return ErrNotMember
		}
		return fmt.Errorf("finding member: %w", err)
	}
	if member.Status != MemberStatusActive {
		return ErrNotMember
	}

	if err := s.repo.UpdateMemberStatus(ctx, cid, memberUID, MemberStatusRemoved); err != nil {
		return fmt.Errorf("removing member: %w", err)
	}

	if s.broadcaster != nil {
		s.broadcaster.MemberLeft(ctx, circleID, memberAddress)
	}
	return nil
}

func (s *circleService) RaiseDispute(ctx context.Context, circleID, userID string, input DisputeInput) (*CircleDispute, error) {
	cid, err := parseUUID(circleID)
	if err != nil {
		return nil, err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("finding circle for dispute: %w", err)
	}

	if c.Status != CircleStatusActive && c.Status != CircleStatusPending {
		return nil, ErrCircleNotActive
	}

	member, err := s.repo.FindMemberByCircleAndUser(ctx, cid, uid)
	if err != nil || member.Status != MemberStatusActive {
		return nil, ErrNotMember
	}

	now := time.Now().UTC()
	var details sql.NullString
	if input.Details != "" {
		details = sql.NullString{String: input.Details, Valid: true}
	}

	dispute := &CircleDispute{
		ID:        uuid.New(),
		CircleID:  cid,
		RaiserID:  uid,
		Reason:    input.Reason,
		Details:   details,
		Status:    "open",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.CreateDispute(ctx, dispute); err != nil {
		return nil, fmt.Errorf("persisting dispute: %w", err)
	}

	if s.notificationSvc != nil {
		notifInput := notification.CreateInput{
			UserID:  c.OrganizerID.String(),
			Type:    notification.TypeDisputeRaised,
			Title:   "Dispute Raised",
			Body:    fmt.Sprintf("A dispute was raised in circle %s: %s", c.Name, input.Reason),
			Channel: notification.ChannelInApp,
		}
		_, _ = s.notificationSvc.Create(ctx, notifInput)
	}

	if s.auditRepo != nil {
		detailsBytes, _ := json.Marshal(map[string]string{"reason": input.Reason})
		auditEntry := &audit.AuditEntry{
			ID:           uuid.New(),
			ActorID:      uid,
			Action:       "circle.dispute_raised",
			ResourceType: "circle",
			ResourceID:   sql.NullString{String: cid.String(), Valid: true},
			Details:      detailsBytes,
			CreatedAt:    now,
		}
		_ = s.auditRepo.Create(ctx, auditEntry)
	}

	return dispute, nil
}

func (s *circleService) CastVote(ctx context.Context, circleID, userID string, input VoteInput) (*CircleVote, bool, string, error) {
	cid, err := parseUUID(circleID)
	if err != nil {
		return nil, false, "", err
	}
	voterID, err := parseUUID(userID)
	if err != nil {
		return nil, false, "", err
	}
	recipientID, err := parseUUID(input.RecipientID)
	if err != nil {
		return nil, false, "", err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return nil, false, "", fmt.Errorf("finding circle for vote: %w", err)
	}
	if c.Status != CircleStatusActive {
		return nil, false, "", ErrCircleNotActive
	}
	if c.PayoutType != PayoutTypeVote {
		return nil, false, "", fmt.Errorf("circle payout type is not vote")
	}

	voter, err := s.repo.FindMemberByCircleAndUser(ctx, cid, voterID)
	if err != nil || voter.Status != MemberStatusActive {
		return nil, false, "", ErrNotMember
	}

	recipient, err := s.repo.FindMemberByCircleAndUser(ctx, cid, recipientID)
	if err != nil || recipient.Status != MemberStatusActive {
		return nil, false, "", fmt.Errorf("recipient is not an active circle member")
	}

	roundNum := c.CurrentRound
	if roundNum < 1 {
		roundNum = 1
	}

	vote := &CircleVote{
		ID:          uuid.New(),
		CircleID:    cid,
		VoterID:     voterID,
		RecipientID: recipientID,
		RoundNumber: roundNum,
		CreatedAt:   time.Now().UTC(),
	}

	if err := s.repo.CreateVote(ctx, vote); err != nil {
		if err == apperrors.ErrConflict {
			return nil, false, "", fmt.Errorf("user has already voted in this round")
		}
		return nil, false, "", fmt.Errorf("recording vote: %w", err)
	}

	votes, err := s.repo.GetVotesByRound(ctx, cid, roundNum)
	if err != nil {
		return vote, false, "", nil
	}

	memberCount, err := s.repo.GetMemberCount(ctx, cid)
	if err != nil || memberCount == 0 {
		memberCount = c.MaxMembers
	}

	if len(votes) >= memberCount {
		tallyMap := make(map[string]int)
		for _, v := range votes {
			tallyMap[v.RecipientID.String()]++
		}
		winner := VoteTally(tallyMap)
		return vote, true, winner, nil
	}

	return vote, false, "", nil
}

func (s *circleService) SubmitAuctionBid(ctx context.Context, circleID, userID string, input AuctionBidInput) (*CircleAuctionBid, error) {
	cid, err := parseUUID(circleID)
	if err != nil {
		return nil, err
	}
	bidderID, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}

	c, err := s.repo.FindByID(ctx, cid)
	if err != nil {
		return nil, err
	}
	if c.Status != CircleStatusActive {
		return nil, ErrCircleNotActive
	}
	if c.PayoutType != PayoutTypeAuction {
		return nil, fmt.Errorf("circle payout type is not auction")
	}

	bidder, err := s.repo.FindMemberByCircleAndUser(ctx, cid, bidderID)
	if err != nil || bidder.Status != MemberStatusActive {
		return nil, ErrNotMember
	}

	if input.BidAmount <= 0 {
		return nil, fmt.Errorf("bid amount must be greater than zero")
	}

	roundNum := c.CurrentRound
	if roundNum < 1 {
		roundNum = 1
	}

	bid := &CircleAuctionBid{
		ID:          uuid.New(),
		CircleID:    cid,
		BidderID:    bidderID,
		RoundNumber: roundNum,
		BidAmount:   input.BidAmount,
		CreatedAt:   time.Now().UTC(),
	}

	if err := s.repo.CreateAuctionBid(ctx, bid); err != nil {
		return nil, fmt.Errorf("recording auction bid: %w", err)
	}

	return bid, nil
}

func ceilFloat(f float64) float64 {
	return math.Ceil(f*100) / 100
}

func (s *circleService) ProcessMissedContributions(ctx context.Context, circleID string, roundNumber int) error {
	cID, err := uuid.Parse(circleID)
	if err != nil {
		return apperrors.ErrInvalidInput
	}

	c, err := s.repo.FindByID(ctx, cID)
	if err != nil {
		return err
	}
	if c == nil {
		return apperrors.ErrNotFound
	}

	members, err := s.repo.GetMembers(ctx, cID)
	if err != nil {
		return err
	}

	contributedUserIDs, err := s.repo.GetContributionsByCircleAndRound(ctx, cID, roundNumber)
	if err != nil {
		return err
	}

	contributedMap := make(map[uuid.UUID]bool)
	for _, uid := range contributedUserIDs {
		contributedMap[uid] = true
	}

	for _, member := range members {
		if member.Status != MemberStatusActive {
			continue
		}
		if !contributedMap[member.UserID] {
			// Member missed contribution, apply penalty
			penaltyAmt := CalculateLateFee(c.ContributionAmount, c.LateFeePercent)
			strikes := ApplyStrikes(&member, "late") // default late penalty

			p := &Penalty{
				ID:             uuid.New(),
				CircleID:       cID,
				UserID:         member.UserID,
				RoundNumber:    roundNumber,
				PenaltyType:    PenaltyTypeLate,
				Amount:         penaltyAmt,
				StrikesApplied: strikes,
				Reason:         sql.NullString{String: fmt.Sprintf("Missed contribution for round %d", roundNumber), Valid: true},
				CreatedAt:      time.Now(),
			}

			err = s.repo.CreatePenalty(ctx, p)
			if err != nil {
				return err
			}

			// Broadcast event for notification/indexer
			// We can use a new method like MemberPenalized or just use existing ones.
			// The Acceptance Criteria mentions: "Notify affected members"
			// This will likely be handled by an event listener on the indexer or notification service.
			// But for now, we process it here.
			if s.broadcaster != nil {
				s.broadcaster.MemberPenalized(ctx, circleID, member.UserID.String(), roundNumber, penaltyAmt)
			}
		}
	}

	return nil
}
