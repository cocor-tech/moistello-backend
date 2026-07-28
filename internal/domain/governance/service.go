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

type Service interface {
	CreateProposal(ctx context.Context, input CreateProposalInput) (*Proposal, error)
	ListProposals(ctx context.Context) ([]Proposal, error)
	GetProposal(ctx context.Context, id string) (*Proposal, error)
	VoteProposal(ctx context.Context, proposalID, userID string, vote bool) error
	ExecuteProposal(ctx context.Context, id string) error
}

type service struct {
	mu         sync.RWMutex
	proposals  map[uuid.UUID]*Proposal
	votesByID  map[uuid.UUID]map[uuid.UUID]bool
}

func NewService() Service {
	return &service{
		proposals: make(map[uuid.UUID]*Proposal),
		votesByID: make(map[uuid.UUID]map[uuid.UUID]bool),
	}
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

	s.mu.Lock()
	defer s.mu.Unlock()
	s.proposals[proposal.ID] = proposal
	s.votesByID[proposal.ID] = make(map[uuid.UUID]bool)
	return proposal, nil
}

func (s *service) ListProposals(ctx context.Context) ([]Proposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	proposals := make([]Proposal, 0, len(s.proposals))
	for _, proposal := range s.proposals {
		proposals = append(proposals, *proposal)
	}
	sort.Slice(proposals, func(i, j int) bool {
		return proposals[i].CreatedAt.After(proposals[j].CreatedAt)
	})
	return proposals, nil
}

func (s *service) GetProposal(ctx context.Context, id string) (*Proposal, error) {
	proposalID, err := parseUUID(id)
	if err != nil {
		return nil, err
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

	s.mu.Lock()
	defer s.mu.Unlock()

	proposal, ok := s.proposals[proposalID]
	if !ok {
		return ErrProposalNotFound
	}
	if proposal.Status != ProposalStatusPending {
		return fmt.Errorf("proposal has already been processed")
	}
	proposal.Status = ProposalStatusExecuted
	now := time.Now().UTC()
	proposal.ExecutedAt = &now
	proposal.UpdatedAt = now
	return nil
}
