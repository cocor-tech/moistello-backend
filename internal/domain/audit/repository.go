package audit

import "context"

type Repository interface {
	Log(ctx context.Context, entry *AuditEntry) error
	List(ctx context.Context, page, limit int) ([]AuditEntry, int, error)
}
