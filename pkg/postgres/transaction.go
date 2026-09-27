package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"

	"github.com/moistello/backend/pkg/tracing"
)

type TxFunc func(tx *sqlx.Tx) error

func WithTransaction(ctx context.Context, db *sqlx.DB, fn TxFunc) error {
	return withTransaction(ctx, db, sql.LevelReadCommitted, fn)
}

func WithTransactionLevel(ctx context.Context, db *sqlx.DB, level sql.IsolationLevel, fn TxFunc) error {
	return withTransaction(ctx, db, level, fn)
}

// withTransaction is the single implementation behind every transaction helper.
//
// It is the one place every database transaction passes through, so instrumenting
// it here gives the request pipeline a DB span for transactional work without each
// repository having to be traced individually.
func withTransaction(ctx context.Context, db *sqlx.DB, level sql.IsolationLevel, fn TxFunc) (err error) {
	_, span := tracing.StartDBSpan(ctx, "transaction", "tx")
	start := time.Now()

	// Deferred first, so it runs last. A panic must roll back and propagate before
	// the span closes, and the span must record the panic as the error rather than
	// reporting a clean transaction that never actually completed.
	defer func() {
		if p := recover(); p != nil {
			tracing.EndSpan(span, fmt.Errorf("panic in transaction: %v", p), start)
			panic(p)
		}
		tracing.EndSpan(span, err, start)
	}()

	span.SetAttributes(attribute.String("db.isolation_level", level.String()))

	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: level})
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

type Transactor struct {
	DB *sqlx.DB
}

func NewTransactor(db *sqlx.DB) *Transactor {
	return &Transactor{DB: db}
}

func (t *Transactor) WithTransaction(ctx context.Context, fn func() error) error {
	return withTransaction(ctx, t.DB, sql.LevelReadCommitted, func(tx *sqlx.Tx) error {
		return fn()
	})
}
