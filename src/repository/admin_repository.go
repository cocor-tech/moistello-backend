package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type AuditLogRecord struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id"`
	CreatedAt time.Time `json:"created_at"`
}

type SystemMetrics struct {
	TotalUsers     int64 `json:"total_users"`
	TotalWallets   int64 `json:"total_wallets"`
	ActiveSessions int64 `json:"active_sessions"`
}

type AdminRepository struct {
	db *sql.DB
}

func NewAdminRepository(db *sql.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

func (r *AdminRepository) GetAuditLogs(ctx context.Context, limit, offset int) ([]AuditLogRecord, error) {
	query := `
		SELECT id, actor_id, action, target_id, created_at
		FROM audit_logs
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch audit logs: %w", err)
	}
	defer rows.Close()

	var logs []AuditLogRecord
	for rows.Next() {
		var l AuditLogRecord
		if err := rows.Scan(&l.ID, &l.ActorID, &l.Action, &l.TargetID, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan audit record: %w", err)
		}
		logs = append(logs, l)
	}

	return logs, nil
}

func (r *AdminRepository) GetPlatformMetrics(ctx context.Context) (*SystemMetrics, error) {
	var metrics SystemMetrics

	userQuery := `SELECT COUNT(*) FROM users`
	if err := r.db.QueryRowContext(ctx, userQuery).Scan(&metrics.TotalUsers); err != nil {
		return nil, fmt.Errorf("failed to count users: %w", err)
	}

	walletQuery := `SELECT COUNT(*) FROM wallets`
	if err := r.db.QueryRowContext(ctx, walletQuery).Scan(&metrics.TotalWallets); err != nil {
		return nil, fmt.Errorf("failed to count wallets: %w", err)
	}

	sessionQuery := `SELECT COUNT(*) FROM sessions WHERE expires_at > NOW()`
	if err := r.db.QueryRowContext(ctx, sessionQuery).Scan(&metrics.ActiveSessions); err != nil {
		return nil, fmt.Errorf("failed to count active sessions: %w", err)
	}

	return &metrics, nil
}