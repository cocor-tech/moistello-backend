package audit

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) Log(ctx context.Context, entry *AuditEntry) error {
	query := `INSERT INTO audit_log (id, actor_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at)
		VALUES (:id, :actor_id, :action, :resource_type, :resource_id, :details, :ip_address, :user_agent, :created_at)`
	_, err := r.db.NamedExecContext(ctx, query, entry)
	if err != nil {
		return fmt.Errorf("logging audit entry: %w", err)
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, page, limit int) ([]AuditEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM audit_log`); err != nil {
		return nil, 0, fmt.Errorf("counting audit entries: %w", err)
	}

	var entries []AuditEntry
	query := `SELECT id, actor_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at
		FROM audit_log ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	if err := r.db.SelectContext(ctx, &entries, query, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("listing audit entries: %w", err)
	}
	return entries, total, nil
}
