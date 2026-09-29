package circle

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresRepository_ListUsesOneAggregatedQuery(t *testing.T) {
	for _, size := range []int{1, 20} {
		t.Run(fmt.Sprintf("rows_%d", size), func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer mockDB.Close()
			repo := NewRepository(sqlx.NewDb(mockDB, "sqlmock"))
			columns := []string{"id", "contract_id", "community_id", "name", "description", "circle_type", "payout_type", "contribution_amount", "currency", "frequency", "max_members", "min_moi_score", "collateral_percent", "late_fee_percent", "grace_period_hours", "max_strikes", "member_count", "requires_invite", "start_date", "end_date", "status", "current_round", "total_contributions", "organizer_id", "created_at", "updated_at"}
			rows := sqlmock.NewRows(columns)
			now := time.Now()
			for n := 0; n < size; n++ {
				rows.AddRow(uuid.New(), nil, nil, fmt.Sprintf("circle-%d", n), nil, CircleTypePublic, PayoutTypeRandom, 10.0, CurrencyUSDC, FrequencyWeekly, 20, 0, 0.0, 0.0, 1, 3, n, false, nil, nil, CircleStatusActive, 1, 10.0, uuid.New(), now, now)
			}
			mock.ExpectQuery("LEFT JOIN \\(").WithArgs(20, 0).WillReturnRows(rows)
			got, err := repo.List(context.Background(), CircleFilter{})
			require.NoError(t, err)
			assert.Len(t, got, size)
			assert.NoError(t, mock.ExpectationsWereMet(), "query count must not grow with result size")
		})
	}
}

func TestPostgresRepository_CreatePenalty(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewRepository(db)

	p := &Penalty{
		ID:             uuid.New(),
		CircleID:       uuid.New(),
		UserID:         uuid.New(),
		RoundNumber:    1,
		PenaltyType:    PenaltyTypeLate,
		Amount:         10.5,
		StrikesApplied: 1,
		Reason:         sql.NullString{String: "missed round", Valid: true},
		CreatedAt:      time.Now(),
	}

	mock.ExpectExec("INSERT INTO penalties").
		WithArgs(p.ID, p.CircleID, p.UserID, p.RoundNumber, p.PenaltyType, p.Amount, p.StrikesApplied, p.Reason, p.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.CreatePenalty(context.Background(), p)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
