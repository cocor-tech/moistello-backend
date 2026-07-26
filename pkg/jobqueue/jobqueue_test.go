package jobqueue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobQueue_EnqueueAndDequeue(t *testing.T) {
	ctx := context.Background()
	jq := NewJobQueue(nil)

	payload := map[string]string{"task": "send_email", "to": "user@example.com"}
	job, err := jq.Enqueue(ctx, "emails", payload, 3)
	require.NoError(t, err)
	assert.Equal(t, "emails", job.QueueName)
	assert.Equal(t, StatusPending, job.Status)
	assert.Equal(t, 3, job.MaxRetries)

	dequeued, err := jq.Dequeue(ctx, "emails")
	require.NoError(t, err)
	require.NotNil(t, dequeued)
	assert.Equal(t, job.ID, dequeued.ID)
	assert.Equal(t, StatusProcessing, dequeued.Status)
}

func TestJobQueue_RetriesAndDeadLetter(t *testing.T) {
	ctx := context.Background()
	jq := NewJobQueue(nil)

	job, err := jq.Enqueue(ctx, "tasks", "data", 2)
	require.NoError(t, err)

	// First failure (retry count 1 < max 2 -> pending)
	err = jq.Fail(ctx, job.ID, assert.AnError)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, jq.memory[job.ID].Status)
	assert.Equal(t, 1, jq.memory[job.ID].RetriesCount)

	// Second failure (retry count 2 >= max 2 -> dead_letter)
	err = jq.Fail(ctx, job.ID, assert.AnError)
	require.NoError(t, err)
	assert.Equal(t, StatusDeadLetter, jq.memory[job.ID].Status)

	// View dead letter jobs
	deadJobs, err := jq.GetDeadLetterJobs(ctx)
	require.NoError(t, err)
	require.Len(t, deadJobs, 1)
	assert.Equal(t, job.ID, deadJobs[0].ID)

	// Admin retry dead letter job
	err = jq.RetryDeadLetterJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, jq.memory[job.ID].Status)
	assert.Equal(t, 0, jq.memory[job.ID].RetriesCount)
}
