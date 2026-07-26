package jobqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusProcessing JobStatus = "processing"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
	StatusDeadLetter JobStatus = "dead_letter"
)

type Job struct {
	ID           string          `db:"id" json:"id"`
	QueueName    string          `db:"queue_name" json:"queue_name"`
	Payload      json.RawMessage `db:"payload" json:"payload"`
	Status       JobStatus       `db:"status" json:"status"`
	MaxRetries   int             `db:"max_retries" json:"max_retries"`
	RetriesCount int             `db:"retries_count" json:"retries_count"`
	ScheduledAt  time.Time       `db:"scheduled_at" json:"scheduled_at"`
	LastError    sql.NullString  `db:"last_error" json:"last_error,omitempty"`
	CreatedAt    time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time       `db:"updated_at" json:"updated_at"`
}

type JobQueue struct {
	db     *sqlx.DB
	memory map[string]*Job
	mu     sync.Mutex
}

func NewJobQueue(db *sqlx.DB) *JobQueue {
	return &JobQueue{
		db:     db,
		memory: make(map[string]*Job),
	}
}

// Enqueue inserts a new background job.
func (jq *JobQueue) Enqueue(ctx context.Context, queueName string, payload any, maxRetries int) (*Job, error) {
	if maxRetries <= 0 {
		maxRetries = 3
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	jobID := uuid.New().String()
	now := time.Now()

	if jq.db != nil {
		query := `
			INSERT INTO job_queue (id, queue_name, payload, status, max_retries, retries_count, scheduled_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 0, $6, $6, $6)
			RETURNING id, queue_name, payload, status, max_retries, retries_count, scheduled_at, created_at, updated_at
		`
		var job Job
		err := jq.db.GetContext(ctx, &job, query, jobID, queueName, payloadBytes, StatusPending, maxRetries, now)
		if err != nil {
			return nil, fmt.Errorf("enqueue query: %w", err)
		}
		return &job, nil
	}

	// In-memory fallback
	jq.mu.Lock()
	defer jq.mu.Unlock()

	job := &Job{
		ID:           jobID,
		QueueName:    queueName,
		Payload:      payloadBytes,
		Status:       StatusPending,
		MaxRetries:   maxRetries,
		RetriesCount: 0,
		ScheduledAt:  now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	jq.memory[jobID] = job
	return job, nil
}

// Dequeue acquires an available pending job using FOR UPDATE SKIP LOCKED.
func (jq *JobQueue) Dequeue(ctx context.Context, queueName string) (*Job, error) {
	if jq.db != nil {
		tx, err := jq.db.BeginTxx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()

		query := `
			SELECT id, queue_name, payload, status, max_retries, retries_count, scheduled_at, created_at, updated_at
			FROM job_queue
			WHERE queue_name = $1 AND status = 'pending' AND scheduled_at <= NOW()
			ORDER BY scheduled_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		`
		var job Job
		err = tx.GetContext(ctx, &job, query, queueName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}

		updateQuery := `UPDATE job_queue SET status = 'processing', updated_at = NOW() WHERE id = $1`
		if _, err := tx.ExecContext(ctx, updateQuery, job.ID); err != nil {
			return nil, err
		}

		if err := tx.Commit(); err != nil {
			return nil, err
		}
		job.Status = StatusProcessing
		return &job, nil
	}

	// In-memory fallback
	jq.mu.Lock()
	defer jq.mu.Unlock()

	now := time.Now()
	for _, job := range jq.memory {
		if job.QueueName == queueName && job.Status == StatusPending && !job.ScheduledAt.After(now) {
			job.Status = StatusProcessing
			job.UpdatedAt = now
			return job, nil
		}
	}
	return nil, nil
}

// Complete marks a job as successfully completed.
func (jq *JobQueue) Complete(ctx context.Context, jobID string) error {
	if jq.db != nil {
		_, err := jq.db.ExecContext(ctx, `UPDATE job_queue SET status = 'completed', updated_at = NOW() WHERE id = $1`, jobID)
		return err
	}

	jq.mu.Lock()
	defer jq.mu.Unlock()
	if job, ok := jq.memory[jobID]; ok {
		job.Status = StatusCompleted
		job.UpdatedAt = time.Now()
	}
	return nil
}

// Fail handles job execution failure with exponential backoff retries (max 3) or moves to dead_letter.
func (jq *JobQueue) Fail(ctx context.Context, jobID string, execErr error) error {
	errMessage := execErr.Error()

	if jq.db != nil {
		var job Job
		if err := jq.db.GetContext(ctx, &job, `SELECT id, max_retries, retries_count FROM job_queue WHERE id = $1`, jobID); err != nil {
			return err
		}

		newRetries := job.RetriesCount + 1
		if newRetries >= job.MaxRetries {
			// Move to dead letter queue
			_, err := jq.db.ExecContext(ctx, `
				UPDATE job_queue
				SET status = 'dead_letter', retries_count = $1, last_error = $2, updated_at = NOW()
				WHERE id = $3
			`, newRetries, errMessage, jobID)
			return err
		}

		// Exponential backoff: 2^newRetries seconds
		backoffDuration := time.Duration(1<<uint(newRetries)) * time.Second
		nextSchedule := time.Now().Add(backoffDuration)

		_, err := jq.db.ExecContext(ctx, `
			UPDATE job_queue
			SET status = 'pending', retries_count = $1, scheduled_at = $2, last_error = $3, updated_at = NOW()
			WHERE id = $4
		`, newRetries, nextSchedule, errMessage, jobID)
		return err
	}

	// In-memory fallback
	jq.mu.Lock()
	defer jq.mu.Unlock()
	if job, ok := jq.memory[jobID]; ok {
		job.RetriesCount++
		job.LastError = sql.NullString{String: errMessage, Valid: true}
		job.UpdatedAt = time.Now()

		if job.RetriesCount >= job.MaxRetries {
			job.Status = StatusDeadLetter
		} else {
			job.Status = StatusPending
			job.ScheduledAt = time.Now().Add(time.Duration(1<<uint(job.RetriesCount)) * time.Second)
		}
	}
	return nil
}

// GetDeadLetterJobs retrieves all jobs in dead_letter status.
func (jq *JobQueue) GetDeadLetterJobs(ctx context.Context) ([]*Job, error) {
	if jq.db != nil {
		var jobs []*Job
		err := jq.db.SelectContext(ctx, &jobs, `SELECT id, queue_name, payload, status, max_retries, retries_count, scheduled_at, last_error, created_at, updated_at FROM job_queue WHERE status = 'dead_letter' ORDER BY updated_at DESC`)
		return jobs, err
	}

	jq.mu.Lock()
	defer jq.mu.Unlock()
	var result []*Job
	for _, job := range jq.memory {
		if job.Status == StatusDeadLetter {
			result = append(result, job)
		}
	}
	return result, nil
}

// RetryDeadLetterJob resets a dead-letter job back to pending.
func (jq *JobQueue) RetryDeadLetterJob(ctx context.Context, jobID string) error {
	if jq.db != nil {
		res, err := jq.db.ExecContext(ctx, `
			UPDATE job_queue
			SET status = 'pending', retries_count = 0, scheduled_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND status = 'dead_letter'
		`, jobID)
		if err != nil {
			return err
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			return errors.New("dead letter job not found or not in dead_letter status")
		}
		return nil
	}

	jq.mu.Lock()
	defer jq.mu.Unlock()
	job, ok := jq.memory[jobID]
	if !ok || job.Status != StatusDeadLetter {
		return errors.New("dead letter job not found")
	}
	job.Status = StatusPending
	job.RetriesCount = 0
	job.ScheduledAt = time.Now()
	job.UpdatedAt = time.Now()
	return nil
}
