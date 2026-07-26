CREATE TABLE IF NOT EXISTS job_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    queue_name VARCHAR(100) NOT NULL DEFAULT 'default',
    payload JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending, processing, failed, dead_letter, completed
    max_retries INT NOT NULL DEFAULT 3,
    retries_count INT NOT NULL DEFAULT 0,
    scheduled_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_job_queue_status_sched ON job_queue(status, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_job_queue_dead_letter ON job_queue(status) WHERE status = 'dead_letter';
