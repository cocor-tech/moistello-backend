package governance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/moistello/backend/pkg/apperrors"
)

var (
	ErrProposalNotFound = errors.New("proposal not found")
	ErrAlreadyVoted     = errors.New("user already voted on this proposal")
	// ErrTimelockActive means a proposal passed but is still inside its
	// execution timelock, so it cannot be executed yet (#414). It is distinct
	// from a failure so the API can answer 409 with executable_at rather than a
	// generic error, letting a client show the countdown.
	ErrTimelockActive = errors.New("proposal is inside its execution timelock and cannot be executed yet")
	// ErrNotCancellable means the proposal is not in the state where
	// cancellation is allowed (#414): not passed, or the timelock has already
	// elapsed so the cancellation window has closed.
	ErrNotCancellable = errors.New("proposal is not in a cancellable state")
	// ErrAlreadyCancelledVote means this member already voted to cancel.
	ErrAlreadyCancelledVote = errors.New("user already voted to cancel this proposal")
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
	// CancelProposal records a member's vote to cancel a proposal that is inside
	// its execution timelock, and cancels it once the threshold is met (#414).
	// It reports whether this call was the one that cancelled the proposal.
	CancelProposal(ctx context.Context, proposalID, userID string) (bool, error)
	SetExecutor(executor ProposalExecutor)
}

type service struct {
	repo      Repository
	executor  ProposalExecutor
	timelock  TimelockConfig
	// now is the clock, injectable so tests can move across a timelock
	// boundary without sleeping.
	now        func() time.Time
	mu         sync.RWMutex
	proposals  map[uuid.UUID]*Proposal
	votesByID  map[uuid.UUID]map[uuid.UUID]bool
	cancelsByID map[uuid.UUID]map[uuid.UUID]bool
}

type Option func(*service)

func WithExecutor(executor ProposalExecutor) Option {
	return func(s *service) {
		s.executor = executor
	}
}

// WithTimelock configures the execution delay and cancellation threshold
// (#414). Without it the delay is zero, so a passed proposal executes
// immediately — the behaviour before #414 — and the service stays usable
// without governance configuration. The deployment config supplies the real
// delay.
func WithTimelock(cfg TimelockConfig) Option {
	return func(s *service) {
		s.timelock = cfg.Normalize()
	}
}

// withClock overrides the service clock. It is unexported and exists for
// tests, which need to cross a timelock boundary deterministically.
func withClock(now func() time.Time) Option {
	return func(s *service) {
		if now != nil {
			s.now = now
		}
	}
}

func NewService(repo Repository, opts ...Option) Service {
	s := &service{
		repo:       repo,
		timelock:   TimelockConfig{}.Normalize(),
		now:        func() time.Time { return time.Now().UTC() },
		proposals:  make(map[uuid.UUID]*Proposal),
		votesByID:  make(map[uuid.UUID]map[uuid.UUID]bool),
		cancelsByID: make(map[uuid.UUID]map[uuid.UUID]bool),
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

// ExecuteProposal advances a proposal toward execution (#414).
//
// It is two-phase, keyed on the proposal's current status:
//
//	pending  -> evaluate the vote. Carried: move to 'passed' and stamp
//	            executableAt = now + the configured delay (or execute at once
//	            when the delay is zero). Not carried: move to 'rejected'.
//	passed   -> if now is before executableAt, return ErrTimelockActive.
//	            Otherwise perform the execution and move to 'executed'.
//
// Splitting evaluation from execution is what creates the review window. The
// first call decides; it does not act. A caller that only wants to know when
// the proposal becomes executable can call it once and read ExecutableAt off
// the proposal.
func (s *service) ExecuteProposal(ctx context.Context, id string) error {
	proposalID, err := parseUUID(id)
	if err != nil {
		return err
	}
	now := s.now()

	if s.repo != nil {
		return s.executeWithRepo(ctx, proposalID, now)
	}
	return s.executeInMemory(proposalID, now)
}

// executeWithRepo is the persisted path. MarkPassed and MarkCancelled are
// conditional updates, so a lost race surfaces as a conflict rather than two
// callers both believing they moved the proposal.
func (s *service) executeWithRepo(ctx context.Context, proposalID uuid.UUID, now time.Time) error {
	p, err := s.repo.GetProposal(ctx, proposalID)
	if err != nil {
		return err
	}

	switch p.Status {
	case ProposalStatusPending:
		if !carried(p) {
			return s.repo.UpdateStatus(ctx, proposalID, ProposalStatusRejected, nil)
		}
		// No timelock configured: preserve the pre-#414 behaviour of executing
		// in the same call that evaluated the vote.
		if s.timelock.Delay <= 0 {
			return s.runAndMarkExecuted(ctx, p, now)
		}
		executableAt := now.Add(s.timelock.Delay)
		return s.repo.MarkPassed(ctx, proposalID, executableAt)

	case ProposalStatusPassed:
		if p.ExecutableAt != nil && now.Before(*p.ExecutableAt) {
			return ErrTimelockActive
		}
		return s.runAndMarkExecuted(ctx, p, now)

	default:
		return fmt.Errorf("proposal has already been processed")
	}
}

// executeInMemory is the in-memory path used by tests and by deployments
// without a repository.
func (s *service) executeInMemory(proposalID uuid.UUID, now time.Time) error {
	s.mu.Lock()
	proposal, ok := s.proposals[proposalID]
	if !ok {
		s.mu.Unlock()
		return ErrProposalNotFound
	}

	switch proposal.Status {
	case ProposalStatusPending:
		if !carried(proposal) {
			proposal.Status = ProposalStatusRejected
			proposal.UpdatedAt = now
			s.mu.Unlock()
			return nil
		}
		if s.timelock.Delay <= 0 {
			exec := s.executor
			proposal.Status = ProposalStatusExecuted
			proposal.ExecutedAt = &now
			proposal.UpdatedAt = now
			s.mu.Unlock()
			if exec != nil {
				return exec.ExecuteProposalAction(ctx, proposal)
			}
			return nil
		}
		executableAt := now.Add(s.timelock.Delay)
		proposal.Status = ProposalStatusPassed
		proposal.ExecutableAt = &executableAt
		proposal.UpdatedAt = now
		s.mu.Unlock()
		return nil

	case ProposalStatusPassed:
		if proposal.ExecutableAt != nil && now.Before(*proposal.ExecutableAt) {
			s.mu.Unlock()
			return ErrTimelockActive
		}
		exec := s.executor
		proposal.Status = ProposalStatusExecuted
		proposal.ExecutedAt = &now
		proposal.UpdatedAt = now
		s.mu.Unlock()
		if exec != nil {
			return exec.ExecuteProposalAction(ctx, proposal)
		}
		return nil

	default:
		s.mu.Unlock()
		return fmt.Errorf("proposal has already been processed")
	}
}

// runAndMarkExecuted performs the proposal's action and records the execution.
func (s *service) runAndMarkExecuted(ctx context.Context, p *Proposal, now time.Time) error {
	if s.executor != nil {
		if err := s.executor.ExecuteProposalAction(ctx, p); err != nil {
			return fmt.Errorf("executing proposal action: %w", err)
		}
	}
	return s.repo.UpdateStatus(ctx, p.ID, ProposalStatusExecuted, &now)
}

// carried reports whether a proposal won its vote. A tie never carries.
func carried(p *Proposal) bool {
	return p.ForVotes > p.AgainstVotes
}

// CancelProposal records a member's vote to cancel a proposal that is inside
// its execution timelock, and cancels the proposal once the threshold is met
// (#414). It reports whether this call is the one that cancelled it.
//
// Cancellation is only allowed while the proposal is 'passed' and the timelock
// has not yet elapsed. Once executableAt passes the window is closed and the
// proposal is on its way to execution — that is the point of the timelock, and
// a cancellation that could race the boundary would defeat it.
//
// The threshold is a share of the votes cast on the original ballot, so a
// lone member cannot cancel a proposal the community backed, while a broad
// wave of regret can stop it.
func (s *service) CancelProposal(ctx context.Context, proposalID, userID string) (bool, error) {
	pid, err := parseUUID(proposalID)
	if err != nil {
		return false, err
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return false, err
	}
	now := s.now()

	if s.repo != nil {
		return s.cancelWithRepo(ctx, pid, uid, now)
	}
	return s.cancelInMemory(pid, uid, now)
}

func (s *service) cancelWithRepo(ctx context.Context, pid, uid uuid.UUID, now time.Time) (bool, error) {
	p, err := s.repo.GetProposal(ctx, pid)
	if err != nil {
		return false, err
	}
	if err := cancellable(p, now); err != nil {
		return false, err
	}

	recorded, err := s.repo.RecordCancellationVote(ctx, pid, uid)
	if err != nil {
		return false, err
	}
	if !recorded {
		return false, ErrAlreadyCancelledVote
	}

	count, err := s.repo.CountCancellationVotes(ctx, pid)
	if err != nil {
		return false, err
	}
	if count < CancellationThreshold(p.ForVotes+p.AgainstVotes, s.timelock.CancelThresholdPct) {
		return false, nil
	}

	// MarkCancelled is conditional on still being passed, so a proposal
	// executed while we were counting surfaces as a conflict rather than being
	// cancelled after the fact.
	if err := s.repo.MarkCancelled(ctx, pid, now); err != nil {
		if errors.Is(err, apperrors.ErrConflict) {
			return false, ErrNotCancellable
		}
		return false, err
	}
	return true, nil
}

func (s *service) cancelInMemory(pid, uid uuid.UUID, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	proposal, ok := s.proposals[pid]
	if !ok {
		return false, ErrProposalNotFound
	}
	if err := cancellable(proposal, now); err != nil {
		return false, err
	}

	cancels := s.cancelsByID[pid]
	if cancels == nil {
		cancels = make(map[uuid.UUID]bool)
		s.cancelsByID[pid] = cancels
	}
	if cancels[uid] {
		return false, ErrAlreadyCancelledVote
	}
	cancels[uid] = true
	proposal.CancellationVotes = len(cancels)

	threshold := CancellationThreshold(proposal.ForVotes+proposal.AgainstVotes, s.timelock.CancelThresholdPct)
	if proposal.CancellationVotes < threshold {
		return false, nil
	}

	proposal.Status = ProposalStatusCancelled
	proposal.CancelledAt = &now
	proposal.UpdatedAt = now
	return true, nil
}

// cancellable reports whether a proposal may be cancelled right now (#414).
func cancellable(p *Proposal, now time.Time) error {
	if p.Status != ProposalStatusPassed {
		// Not passed: still open for voting, or already settled one way or the
		// other. Either way there is no timelock window to cancel inside.
		return ErrNotCancellable
	}
	if p.ExecutableAt != nil && !now.Before(*p.ExecutableAt) {
		// The timelock has elapsed: the cancellation window is closed.
		return ErrNotCancellable
	}
	return nil
}
