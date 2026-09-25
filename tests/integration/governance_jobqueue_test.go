package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/governance"
	"github.com/moistello/backend/pkg/jobqueue"
)

// ── Governance Persistence Integration Tests ──────────────────────────

// TestGovernance_ProposalLifecycle verifies the full lifecycle of a governance
// proposal: creation, voting, execution — using the in-memory service (which
// exercises the same Service interface as the PostgreSQL-backed version).
func TestGovernance_ProposalLifecycle_Persistence(t *testing.T) {
	ctx := context.Background()
	svc := governance.NewService(nil)

	creatorID := uuid.New()

	// Create
	created, err := svc.CreateProposal(ctx, governance.CreateProposalInput{
		Title:        "Increase payout frequency",
		Description:  "Allow weekly payouts for active circles to improve liquidity",
		ProposalType: "parameter",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, governance.ProposalStatusPending, created.Status)
	assert.Equal(t, "Increase payout frequency", created.Title)
	assert.Equal(t, creatorID, created.CreatorID)
	assert.Equal(t, 0, created.ForVotes)
	assert.Equal(t, 0, created.AgainstVotes)

	// Get (verify persistence)
	proposal, err := svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	assert.Equal(t, created.ID, proposal.ID)
	assert.Equal(t, governance.ProposalStatusPending, proposal.Status)

	// List (verify it appears)
	proposals, total, err := svc.ListProposals(ctx, 1, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.GreaterOrEqual(t, len(proposals), 1)
	found := false
	for _, p := range proposals {
		if p.ID == created.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "created proposal should be in list")

	// Vote for
	err = svc.VoteProposal(ctx, created.ID.String(), creatorID.String(), true)
	require.NoError(t, err)

	// Verify vote was recorded
	proposal, err = svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, proposal.ForVotes)
	assert.Equal(t, 0, proposal.AgainstVotes)

	// Second user votes against
	secondVoter := uuid.New()
	err = svc.VoteProposal(ctx, created.ID.String(), secondVoter.String(), false)
	require.NoError(t, err)

	proposal, err = svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, proposal.ForVotes)
	assert.Equal(t, 1, proposal.AgainstVotes)
	assert.Equal(t, governance.ProposalStatusPending, proposal.Status)

	// Execute proposal
	err = svc.ExecuteProposal(ctx, created.ID.String())
	require.NoError(t, err)

	// Verify execution
	executed, err := svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	assert.Equal(t, governance.ProposalStatusExecuted, executed.Status)
	assert.NotNil(t, executed.ExecutedAt)
}

// TestGovernance_DuplicateVotePrevention verifies that the same user cannot
// vote twice on the same proposal.
func TestGovernance_DuplicateVotePrevention(t *testing.T) {
	ctx := context.Background()
	svc := governance.NewService(nil)

	creatorID := uuid.New()

	created, err := svc.CreateProposal(ctx, governance.CreateProposalInput{
		Title:        "Test Proposal",
		Description:  "Testing duplicate votes",
		ProposalType: "parameter",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)

	// First vote
	err = svc.VoteProposal(ctx, created.ID.String(), creatorID.String(), true)
	require.NoError(t, err)

	// Duplicate vote
	err = svc.VoteProposal(ctx, created.ID.String(), creatorID.String(), false)
	assert.Error(t, err)
	assert.Equal(t, governance.ErrAlreadyVoted, err)
}

// TestGovernance_ProposalNotFound verifies error handling for non-existent proposals.
func TestGovernance_ProposalNotFound(t *testing.T) {
	ctx := context.Background()
	svc := governance.NewService(nil)

	_, err := svc.GetProposal(ctx, uuid.New().String())
	assert.Error(t, err)
	assert.Equal(t, governance.ErrProposalNotFound, err)

	err = svc.VoteProposal(ctx, uuid.New().String(), uuid.New().String(), true)
	assert.Error(t, err)
	assert.Equal(t, governance.ErrProposalNotFound, err)
}

// TestGovernance_ExecuteAlreadyProcessed verifies we can't execute a non-pending proposal.
func TestGovernance_ExecuteAlreadyProcessed(t *testing.T) {
	ctx := context.Background()
	svc := governance.NewService(nil)

	creatorID := uuid.New()
	created, err := svc.CreateProposal(ctx, governance.CreateProposalInput{
		Title:        "Test",
		Description:  "Test",
		ProposalType: "parameter",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)

	// Execute once
	err = svc.ExecuteProposal(ctx, created.ID.String())
	require.NoError(t, err)

	// Execute again
	err = svc.ExecuteProposal(ctx, created.ID.String())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been processed")
}

// ── Job Queue Enqueue → Process → Dead-Letter Integration Tests ────────

// TestJobQueue_EnqueueProcessDeadLetter verifies the full lifecycle of a job:
// enqueue → dequeue → fail (retries) → dead letter → retry from dead letter.
func TestJobQueue_EnqueueProcessDeadLetter(t *testing.T) {
	ctx := context.Background()
	jq := jobqueue.NewJobQueue(nil)

	// Step 1: Enqueue a job
	payload := map[string]string{
		"task":    "send_reminder",
		"user_id": uuid.New().String(),
	}
	job, err := jq.Enqueue(ctx, "notifications", payload, 3)
	require.NoError(t, err)
	assert.NotEmpty(t, job.ID)
	assert.Equal(t, "notifications", job.QueueName)
	assert.Equal(t, jobqueue.StatusPending, job.Status)
	assert.Equal(t, 3, job.MaxRetries)

	// Step 2: Dequeue — should transition to processing
	dequeued, err := jq.Dequeue(ctx, "notifications")
	require.NoError(t, err)
	require.NotNil(t, dequeued)
	assert.Equal(t, job.ID, dequeued.ID)
	assert.Equal(t, jobqueue.StatusProcessing, dequeued.Status)

	// Step 3: Fail job — first failure (retry count 1 < max 3 → back to pending)
	err = jq.Fail(ctx, job.ID, assert.AnError)
	require.NoError(t, err)
	assert.Equal(t, jobqueue.StatusPending, jq.GetJobStatus(job.ID))
	assert.Equal(t, 1, jq.GetJobRetries(job.ID))

	// Step 4: Fail again — second failure (retry count 2 < max 3 → pending)
	err = jq.Fail(ctx, job.ID, assert.AnError)
	require.NoError(t, err)
	assert.Equal(t, jobqueue.StatusPending, jq.GetJobStatus(job.ID))
	assert.Equal(t, 2, jq.GetJobRetries(job.ID))

	// Step 5: Fail again — third failure (retry count 3 >= max 3 → dead_letter)
	err = jq.Fail(ctx, job.ID, assert.AnError)
	require.NoError(t, err)
	assert.Equal(t, jobqueue.StatusDeadLetter, jq.GetJobStatus(job.ID))
	assert.Equal(t, 3, jq.GetJobRetries(job.ID))

	// Step 6: Verify dead letter job is retrievable
	deadJobs, err := jq.GetDeadLetterJobs(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(deadJobs), 1)
	found := false
	for _, dj := range deadJobs {
		if dj.ID == job.ID {
			found = true
			assert.Equal(t, jobqueue.StatusDeadLetter, dj.Status)
		}
	}
	assert.True(t, found, "job should appear in dead letter queue")

	// Step 7: Retry dead letter job — should reset to pending
	err = jq.RetryDeadLetterJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, jobqueue.StatusPending, jq.GetJobStatus(job.ID))
	assert.Equal(t, 0, jq.GetJobRetries(job.ID))

	// Step 8: Dequeue and complete
	dequeued, err = jq.Dequeue(ctx, "notifications")
	require.NoError(t, err)
	require.NotNil(t, dequeued)
	assert.Equal(t, job.ID, dequeued.ID)

	err = jq.Complete(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, jobqueue.StatusCompleted, jq.GetJobStatus(job.ID))
}

// TestJobQueue_DifferentQueuesIsolation verifies that jobs from different
// queues are isolated and don't interfere with each other.
func TestJobQueue_DifferentQueuesIsolation(t *testing.T) {
	ctx := context.Background()
	jq := jobqueue.NewJobQueue(nil)

	// Enqueue jobs in different queues
	emailJob, err := jq.Enqueue(ctx, "emails", map[string]string{"to": "a@b.com"}, 2)
	require.NoError(t, err)

	smsJob, err := jq.Enqueue(ctx, "sms", map[string]string{"to": "555-1234"}, 2)
	require.NoError(t, err)

	// Dequeue from emails should return only the email job
	dequeued, err := jq.Dequeue(ctx, "emails")
	require.NoError(t, err)
	require.NotNil(t, dequeued)
	assert.Equal(t, emailJob.ID, dequeued.ID)

	// Dequeue from sms should return only the SMS job
	dequeued, err = jq.Dequeue(ctx, "sms")
	require.NoError(t, err)
	require.NotNil(t, dequeued)
	assert.Equal(t, smsJob.ID, dequeued.ID)
}

// TestJobQueue_EmptyQueueReturnsNil verifies that dequeuing from an empty
// queue returns nil (no error).
func TestJobQueue_EmptyQueueReturnsNil(t *testing.T) {
	ctx := context.Background()
	jq := jobqueue.NewJobQueue(nil)

	dequeued, err := jq.Dequeue(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, dequeued)
}
