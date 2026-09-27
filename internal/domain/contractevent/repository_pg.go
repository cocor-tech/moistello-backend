package contractevent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type pgRepo struct {
	db *sqlx.DB
}

// NewRepository returns a PostgreSQL-backed Repository over the contract_events
// audit log.
func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

const eventColumns = `id, tx_hash, ledger, contract_id, event_type, contract_version, payload, processed_at`

// List returns one page of contract events matching filter, newest ledger first,
// along with the total number of events matching that same filter so callers can
// paginate without losing the filtered count.
func (r *pgRepo) List(ctx context.Context, filter ListFilter, page, limit int) ([]ContractEvent, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	page, limit = normalizePage(page, limit)
	offset := (page - 1) * limit

	where, args := filter.buildWhere()

	var total int
	countQuery := `SELECT COUNT(*) FROM contract_events` + where
	if err := r.db.GetContext(ctx, &total, countQuery, args...); err != nil {
		return nil, 0, fmt.Errorf("counting contract events: %w", err)
	}

	// LIMIT/OFFSET are appended after the filter args so their placeholders keep
	// matching the order the conditions were built in. The ordering is by ledger
	// rather than time because the ledger sequence is what callers page over, and
	// id breaks ties so paging is stable within a ledger.
	pageArgs := append(append([]any{}, args...), limit, offset)
	query := `SELECT ` + eventColumns + ` FROM contract_events` + where +
		` ORDER BY ledger DESC, id DESC LIMIT $` + fmt.Sprint(len(args)+1) +
		` OFFSET $` + fmt.Sprint(len(args)+2)

	var events []ContractEvent
	if err := r.db.SelectContext(ctx, &events, query, pageArgs...); err != nil {
		return nil, 0, fmt.Errorf("listing contract events: %w", err)
	}
	return events, total, nil
}

func normalizePage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	return page, limit
}

// buildWhere turns the filter into a WHERE clause and its bound arguments.
//
// Each clause is appended with an explicit placeholder rather than interpolated,
// so filter values can never be read as SQL. The order is fixed, which keeps the
// placeholder numbering and the argument slice in step.
func (f ListFilter) buildWhere() (string, []any) {
	var clauses []string
	var args []any

	add := func(format string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(format, len(args)))
	}

	if f.TxHash != "" {
		add("tx_hash = $%d", f.TxHash)
	}
	if f.ContractID != "" {
		add("contract_id = $%d", f.ContractID)
	}
	if f.EventType != "" {
		add("event_type = $%d", f.EventType)
	}
	if f.ContractVersion != "" {
		add("contract_version = $%d", f.ContractVersion)
	}
	if f.FromLedger != nil {
		add("ledger >= $%d", *f.FromLedger)
	}
	if f.ToLedger != nil {
		add("ledger <= $%d", *f.ToLedger)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}
