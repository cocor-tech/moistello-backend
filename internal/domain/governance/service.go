package governance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
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

func NewService(repo Repository, opts ...Option) Service {
	s := &service{
		repo:      repo,
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
		return s.repo.RecordVote(ctx, proposalUUID, voterID, vote)
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
	if vote {
		proposal.ForVotes++
	} else {
		proposal.AgainstVotes++
	}
	proposal.UpdatedAt = time.Now().UTC()
	return nil
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

		if p.ForVotes > p.AgainstVotes {
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

	if proposal.ForVotes > proposal.AgainstVotes {
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
