package governance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

var (
	ErrProposalNotFound = errors.New("proposal not found")
	ErrAlreadyVoted     = errors.New("user already voted on this proposal")
)

// ProposalExecutor handles execution of approved proposals (e.g., circle actions or parameter updates).
type ProposalExecutor interface {
	ExecuteProposalAction(ctx context.Context, p *Proposal) error
}

type Service interface {
	CreateProposal(ctx context.Context, input CreateProposalInput) (*Proposal, error)
	ListProposals(ctx context.Context, page, limit int) ([]Proposal, int, error)
	GetProposal(ctx context.Context, id string) (*Proposal, error)
	VoteProposal(ctx context.Context, proposalID, userID string, vote bool) error
	ExecuteProposal(ctx context.Context, id string) error
	SetExecutor(executor ProposalExecutor)
}

type service struct {
	repo      Repository
	executor  ProposalExecutor
	weights   WeightResolver
	mu        sync.RWMutex
	proposals map[uuid.UUID]*Proposal
	votesByID map[uuid.UUID]map[uuid.UUID]bool
}

type Option func(*service)

func WithExecutor(executor ProposalExecutor) Option {
	return func(s *service) {
		s.executor = executor
	}
}

// WithWeightResolver enables voting-weight snapshots (#418). Without it the
// service uses WeightSnapshotter, where every voter has weight 1 — the
// behaviour before #418, so governance keeps working when no token/reputation
// wiring is present.
func WithWeightResolver(resolver WeightResolver) Option {
	return func(s *service) {
		if resolver != nil {
			s.weights = resolver
		}
	}
}

func NewService(repo Repository, opts ...Option) Service {
	s := &service{
		repo:      repo,
		weights:   WeightSnapshotter{},
		proposals: make(map[uuid.UUID]*Proposal),
		votesByID: make(map[uuid.UUID]map[uuid.UUID]bool),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *service) SetExecutor(executor ProposalExecutor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executor = executor
}

func parseUUID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID: %w", err)
	}
	return parsed, nil
}

func (s *service) CreateProposal(ctx context.Context, input CreateProposalInput) (*Proposal, error) {
	creatorID, err := parseUUID(input.CreatorID)
	if err != nil {
		return nil, err
	}
	if input.Title == "" || input.Description == "" || input.ProposalType == "" {
		return nil, fmt.Errorf("title, description, and proposal type are required")
	}

	now := time.Now().UTC()
	proposal := &Proposal{
		ID:           uuid.New(),
		Title:        input.Title,
		Description:  input.Description,
		ProposalType: input.ProposalType,
		CreatorID:    creatorID,
		Status:       ProposalStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Freeze voting weight before the proposal is visible (#418). Every later
	// vote is counted against this snapshot, so tokens moved after this point
	// cannot change the outcome of an in-flight vote.
	snapshot, total := s.snapshotWeights(ctx)
	proposal.WeightSnapshot = snapshot
	proposal.SnapshotAt = &now
	proposal.SnapshotTotalWeight = total

	if s.repo != nil {
		if err := s.repo.CreateProposal(ctx, proposal); err != nil {
			return nil, fmt.Errorf("persisting proposal: %w", err)
		}
		return proposal, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.proposals[proposal.ID] = proposal
	s.votesByID[proposal.ID] = make(map[uuid.UUID]bool)
	return proposal, nil
}

// snapshotWeights captures every eligible voter's current weight and returns
// the map plus the total (#418).
//
// Failures are deliberately non-fatal. A snapshot that silently omits a voter
// would let that voter cast a zero-weight vote, and a lookup error must not
// stop the community from creating proposals at all. When a voter's weight
// cannot be resolved they are recorded with weight 1, and a total failure of
// the voter list yields an empty snapshot — resolveVoteWeight then falls back
// to the default weight rather than disenfranchising anyone.
func (s *service) snapshotWeights(ctx context.Context) (map[uuid.UUID]int64, int64) {
	snapshot := make(map[uuid.UUID]int64)

	voters, err := s.weights.EligibleVoters(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("governance: could not list eligible voters, creating proposal with an empty weight snapshot")
		return snapshot, 0
	}

	var total int64
	for _, voterID := range voters {
		parsed, err := parseUUID(voterID)
		if err != nil {
			continue
		}
		weight, err := s.weights.VotingWeight(ctx, voterID)
		if err != nil || weight < 0 {
			log.Warn().Err(err).Str("userID", voterID).Msg("governance: resolving voter weight failed, defaulting to 1")
			weight = 1
		}
		snapshot[parsed] = weight
		total += weight
	}
	return snapshot, total
}

// resolveVoteWeight returns the weight a vote should count for.
//
// If the proposal carries a snapshot that includes this voter, that snapshotted
// value is authoritative and no live lookup happens — this is the whole point of
// #418, and it is why moving tokens after creation cannot change the vote. A
// voter absent from the snapshot falls back to their current weight, which only
// happens when the snapshot could not be taken or did not cover them; an empty
// snapshot (no resolver wiring) falls back to weight 1.
func (s *service) resolveVoteWeight(ctx context.Context, p *Proposal, voterID uuid.UUID) int64 {
	if p.SnapshotAt != nil {
		if weight, ok := p.WeightSnapshot[voterID]; ok {
			return weight
		}
	}
	if len(p.WeightSnapshot) == 0 {
		// No snapshot coverage at all: preserve one-person-one-vote.
		return 1
	}
	weight, err := s.weights.VotingWeight(ctx, voterID.String())
	if err != nil || weight < 0 {
		return 1
	}
	return weight
}

func (s *service) ListProposals(ctx context.Context, page, limit int) ([]Proposal, int, error) {
	if s.repo != nil {
		return s.repo.ListProposals(ctx, page, limit)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	proposals := make([]Proposal, 0, len(s.proposals))
	for _, proposal := range s.proposals {
		proposals = append(proposals, *proposal)
	}
	sort.Slice(proposals, func(i, j int) bool {
		return proposals[i].CreatedAt.After(proposals[j].CreatedAt)
	})

	total := len(proposals)
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}
	offset := (page - 1) * limit
	if offset >= total {
		return []Proposal{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return proposals[offset:end], total, nil
}

func (s *service) GetProposal(ctx context.Context, id string) (*Proposal, error) {
	proposalID, err := parseUUID(id)
	if err != nil {
		return nil, err
	}

	if s.repo != nil {
		return s.repo.GetProposal(ctx, proposalID)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	proposal, ok := s.proposals[proposalID]
	if !ok {
		return nil, ErrProposalNotFound
	}
	copyProposal := *proposal
	return &copyProposal, nil
}

func (s *service) VoteProposal(ctx context.Context, proposalID, userID string, vote bool) error {
	proposalUUID, err := parseUUID(proposalID)
	if err != nil {
		return err
	}
	voterID, err := parseUUID(userID)
	if err != nil {
		return err
	}

	if s.repo != nil {
		p, err := s.repo.GetProposal(ctx, proposalUUID)
		if err != nil {
			return err
		}
		if p.Status != ProposalStatusPending {
			return fmt.Errorf("proposal is no longer active")
		}
		weight := s.resolveVoteWeight(ctx, p, voterID)
		return s.repo.RecordVoteWeighted(ctx, proposalUUID, voterID, vote, weight)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	proposal, ok := s.proposals[proposalUUID]
	if !ok {
		return ErrProposalNotFound
	}
	if proposal.Status != ProposalStatusPending {
		return fmt.Errorf("proposal is no longer active")
	}
	votes := s.votesByID[proposalUUID]
	if votes == nil {
		votes = make(map[uuid.UUID]bool)
		s.votesByID[proposalUUID] = votes
	}
	if _, exists := votes[voterID]; exists {
		return ErrAlreadyVoted
	}
	votes[voterID] = vote

	// Weight comes from the creation-time snapshot, never from a live lookup,
	// so moving tokens after creation cannot change this vote's power (#418).
	weight := s.resolveVoteWeight(ctx, proposal, voterID)
	if vote {
		proposal.ForVotes++
		proposal.ForWeight += weight
	} else {
		proposal.AgainstVotes++
		proposal.AgainstWeight += weight
	}
	proposal.UpdatedAt = time.Now().UTC()
	return nil
}

// passed reports whether a proposal carries, using snapshotted voting weight
// where one exists (#418).
//
// A proposal with a weight snapshot is decided by weighted totals, so one
// holder with more weight outweighs several smaller holders — the point of
// having weight at all. A proposal with no snapshot (weighting not wired, or
// the snapshot could not be taken) falls back to raw head counts, which is the
// pre-#418 behaviour. A tie never passes.
func passed(p *Proposal) bool {
	if p.SnapshotAt != nil {
		return p.ForWeight > p.AgainstWeight
	}
	return p.ForVotes > p.AgainstVotes
}

func (s *service) ExecuteProposal(ctx context.Context, id string) error {
	proposalID, err := parseUUID(id)
	if err != nil {
		return err
	}

	if s.repo != nil {
		p, err := s.repo.GetProposal(ctx, proposalID)
		if err != nil {
			return err
		}
		if p.Status != ProposalStatusPending {
			return fmt.Errorf("proposal has already been processed")
		}

		if passed(p) {
			if s.executor != nil {
				if err := s.executor.ExecuteProposalAction(ctx, p); err != nil {
					return fmt.Errorf("executing proposal action: %w", err)
				}
			}
			now := time.Now().UTC()
			return s.repo.UpdateStatus(ctx, proposalID, ProposalStatusExecuted, &now)
		}

		return s.repo.UpdateStatus(ctx, proposalID, ProposalStatusRejected, nil)
	}

	s.mu.Lock()
	proposal, ok := s.proposals[proposalID]
	if !ok {
		s.mu.Unlock()
		return ErrProposalNotFound
	}
	if proposal.Status != ProposalStatusPending {
		s.mu.Unlock()
		return fmt.Errorf("proposal has already been processed")
	}

	if passed(proposal) {
		exec := s.executor
		s.mu.Unlock()

		if exec != nil {
			if err := exec.ExecuteProposalAction(ctx, proposal); err != nil {
				return fmt.Errorf("executing proposal action: %w", err)
			}
		}

		s.mu.Lock()
		now := time.Now().UTC()
		proposal.Status = ProposalStatusExecuted
		proposal.ExecutedAt = &now
		proposal.UpdatedAt = now
		s.mu.Unlock()
		return nil
	}

	now := time.Now().UTC()
	proposal.Status = ProposalStatusRejected
	proposal.UpdatedAt = now
	s.mu.Unlock()
	return nil
}
