package circle

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

var (
	circleColumns       = []string{"id", "contract_id", "community_id", "name", "description", "circle_type", "payout_type", "contribution_amount", "currency", "frequency", "max_members", "min_moi_score", "collateral_percent", "late_fee_percent", "grace_period_hours", "max_strikes", "member_count", "requires_invite", "start_date", "end_date", "status", "current_round", "total_contributions", "organizer_id", "created_at", "updated_at"}
	memberColumns       = []string{"circle_id", "user_id", "position", "status", "joined_at"}
	contributionColumns = []string{"id", "circle_id", "user_id", "round_number", "amount", "txn_hash", "status", "on_time", "verified_onchain", "verification_status", "created_at", "updated_at"}
	payoutColumns       = []string{"id", "circle_id", "recipient_id", "round_number", "amount", "fee_amount", "txn_hash", "payout_type", "verified_onchain", "verification_status", "created_at", "updated_at"}
	configColumns       = []string{"id", "circle_id", "round_number", "config_hash", "config_json", "created_at"}
	penaltyColumns      = []string{"id", "circle_id", "user_id", "round_number", "penalty_type", "amount", "strikes_applied", "reason", "created_at"}
	voteColumns         = []string{"id", "circle_id", "voter_id", "recipient_id", "round_number", "created_at"}
	bidColumns          = []string{"id", "circle_id", "bidder_id", "round_number", "bid_amount", "created_at"}
)

// newPgRepoMock returns the concrete repository wired to a sqlmock. Tests use the
// concrete type so they can reach every method, not only the Repository interface.
func newPgRepoMock(t *testing.T) (*pgRepo, *sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = mockDB.Close() })
	db := sqlx.NewDb(mockDB, "sqlmock")
	return NewRepository(db).(*pgRepo), db, mock
}

func circleRowFor(circleID, organizerID uuid.UUID, status string, currentRound int, contribution float64) *sqlmock.Rows {
	return addCircleRow(sqlmock.NewRows(circleColumns), circleID, organizerID, status, currentRound, contribution)
}

func addCircleRow(rows *sqlmock.Rows, circleID, organizerID uuid.UUID, status string, currentRound int, contribution float64) *sqlmock.Rows {
	now := time.Now()
	return rows.AddRow(
		circleID.String(), nil, nil, "Test Circle", nil,
		string(CircleTypePublic), string(PayoutTypeRandom), contribution, string(CurrencyUSDC), string(FrequencyWeekly),
		10, 0, 0.0, 0.0, 1, 3,
		0, false, nil, nil, status, currentRound, 0.0,
		organizerID.String(), now, now,
	)
}

func memberRows(members ...CircleMember) *sqlmock.Rows {
	rows := sqlmock.NewRows(memberColumns)
	for _, m := range members {
		rows.AddRow(m.CircleID.String(), m.UserID.String(), m.Position, string(m.Status), m.JoinedAt)
	}
	return rows
}

// expectSnapshotReads queues the five reads GetCircleSnapshot issues inside its
// transaction, in order. The caller queues the Begin and the Commit or Rollback.
func expectSnapshotReads(mock sqlmock.Sqlmock, circle, members, contribs, payouts, configs *sqlmock.Rows) {
	mock.ExpectQuery("FROM circles WHERE id").WillReturnRows(circle)
	mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(members)
	mock.ExpectQuery("FROM contributions WHERE circle_id").WillReturnRows(contribs)
	mock.ExpectQuery("FROM payouts WHERE circle_id").WillReturnRows(payouts)
	mock.ExpectQuery("FROM round_config_snapshots WHERE circle_id").WillReturnRows(configs)
}

func emptySnapshotRows() (contribs, payouts, configs *sqlmock.Rows) {
	return sqlmock.NewRows(contributionColumns), sqlmock.NewRows(payoutColumns), sqlmock.NewRows(configColumns)
}

func TestNewRepositoryFromTx_ReadsThroughTransaction(t *testing.T) {
	_, db, mock := newPgRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("FROM circles WHERE id").WillReturnRows(sqlmock.NewRows(circleColumns))
	mock.ExpectCommit()

	tx, err := db.Beginx()
	require.NoError(t, err)
	repo := NewRepositoryFromTx(tx)

	_, err = repo.FindByID(context.Background(), uuid.New())
	assert.ErrorIs(t, err, ErrCircleNotFound)
	require.NoError(t, tx.Commit())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresRepository_FindByID(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the circle", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		id := uuid.New()
		mock.ExpectQuery("FROM circles WHERE id").WillReturnRows(circleRowFor(id, uuid.New(), string(CircleStatusActive), 1, 10))

		c, err := repo.FindByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, id, c.ID)
		assert.Equal(t, "Test Circle", c.Name)
		assert.Equal(t, CircleStatusActive, c.Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps a missing row to ErrCircleNotFound", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circles WHERE id").WillReturnRows(sqlmock.NewRows(circleColumns))

		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, ErrCircleNotFound)
	})

	t.Run("wraps query failures without masking them as not found", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("connection reset")
		mock.ExpectQuery("FROM circles WHERE id").WillReturnError(boom)

		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, boom)
		assert.NotErrorIs(t, err, ErrCircleNotFound)
	})
}

func TestPostgresRepository_FindByContractID(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the circle", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		id := uuid.New()
		mock.ExpectQuery("FROM circles WHERE contract_id").WillReturnRows(circleRowFor(id, uuid.New(), string(CircleStatusPending), 1, 5))

		c, err := repo.FindByContractID(ctx, "CONTRACT-1")
		require.NoError(t, err)
		assert.Equal(t, id, c.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps a missing row to ErrCircleNotFound", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circles WHERE contract_id").WillReturnRows(sqlmock.NewRows(circleColumns))

		_, err := repo.FindByContractID(ctx, "missing")
		assert.ErrorIs(t, err, ErrCircleNotFound)
	})
}

func TestPostgresRepository_Count(t *testing.T) {
	ctx := context.Background()

	t.Run("counts without filters", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circles WHERE deleted_at IS NULL").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

		n, err := repo.Count(ctx, CircleFilter{})
		require.NoError(t, err)
		assert.Equal(t, 7, n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("binds search and status filters in order", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("name ILIKE").
			WithArgs("%alpha%", "%alpha%", "active").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		n, err := repo.Count(ctx, CircleFilter{Search: "alpha", Status: CircleStatusActive})
		require.NoError(t, err)
		assert.Equal(t, 2, n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM circles WHERE deleted_at IS NULL").WillReturnError(boom)

		n, err := repo.Count(ctx, CircleFilter{})
		assert.Equal(t, 0, n)
		assert.ErrorContains(t, err, "counting circles")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_Create(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	c := &Circle{
		ID:                 uuid.New(),
		Name:               "New Circle",
		CircleType:         CircleTypePublic,
		PayoutType:         PayoutTypeRandom,
		ContributionAmount: 10,
		Currency:           CurrencyUSDC,
		Frequency:          FrequencyWeekly,
		MaxMembers:         10,
		Status:             CircleStatusPending,
		CurrentRound:       1,
		OrganizerID:        uuid.New(),
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	t.Run("inserts the circle", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circles").WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, repo.Create(ctx, c))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps unique violations to ErrConflict", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circles").WillReturnError(&pq.Error{Code: "23505"})

		assert.ErrorIs(t, repo.Create(ctx, c), apperrors.ErrConflict)
	})

	t.Run("wraps other insert failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("disk full")
		mock.ExpectExec("INSERT INTO circles").WillReturnError(boom)

		err := repo.Create(ctx, c)
		assert.ErrorContains(t, err, "creating circle")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_Update(t *testing.T) {
	ctx := context.Background()
	c := &Circle{ID: uuid.New(), Name: "Renamed", CircleType: CircleTypePublic, PayoutType: PayoutTypeFixed, Currency: CurrencyUSDC, Frequency: FrequencyMonthly, Status: CircleStatusActive, UpdatedAt: time.Now()}

	t.Run("updates an existing circle", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("UPDATE circles SET name").WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Update(ctx, c))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns ErrCircleNotFound when no row matches", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("UPDATE circles SET name").WillReturnResult(sqlmock.NewResult(0, 0))

		assert.ErrorIs(t, repo.Update(ctx, c), ErrCircleNotFound)
	})

	t.Run("wraps update failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("UPDATE circles SET name").WillReturnError(boom)

		err := repo.Update(ctx, c)
		assert.ErrorContains(t, err, "updating circle")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_Delete(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("soft-deletes the circle", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("UPDATE circles SET deleted_at").WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Delete(ctx, id))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns ErrCircleNotFound when no row matches", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("UPDATE circles SET deleted_at").WillReturnResult(sqlmock.NewResult(0, 0))

		assert.ErrorIs(t, repo.Delete(ctx, id), ErrCircleNotFound)
	})

	t.Run("wraps delete failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("UPDATE circles SET deleted_at").WillReturnError(boom)

		err := repo.Delete(ctx, id)
		assert.ErrorContains(t, err, "deleting circle")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_CreateMember(t *testing.T) {
	ctx := context.Background()
	m := &CircleMember{CircleID: uuid.New(), UserID: uuid.New(), Position: 1, Status: MemberStatusActive, JoinedAt: time.Now()}

	t.Run("inserts the member", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circle_members").WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, repo.CreateMember(ctx, m))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps duplicate membership to ErrAlreadyMember", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circle_members").WillReturnError(&pq.Error{Code: "23505"})

		assert.ErrorIs(t, repo.CreateMember(ctx, m), ErrAlreadyMember)
	})

	t.Run("wraps other insert failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("INSERT INTO circle_members").WillReturnError(boom)

		err := repo.CreateMember(ctx, m)
		assert.ErrorContains(t, err, "creating circle member")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetMembers(t *testing.T) {
	ctx := context.Background()
	circleID, userID := uuid.New(), uuid.New()
	now := time.Now()

	t.Run("returns members in position order", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(memberRows(
			CircleMember{CircleID: circleID, UserID: userID, Position: 1, Status: MemberStatusActive, JoinedAt: now},
			CircleMember{CircleID: circleID, UserID: uuid.New(), Position: 2, Status: MemberStatusExited, JoinedAt: now},
		))

		members, err := repo.GetMembers(ctx, circleID)
		require.NoError(t, err)
		require.Len(t, members, 2)
		assert.Equal(t, userID, members[0].UserID)
		assert.Equal(t, MemberStatusExited, members[1].Status)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns no members for an empty circle", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(memberRows())

		members, err := repo.GetMembers(ctx, circleID)
		require.NoError(t, err)
		assert.Empty(t, members)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnError(boom)

		_, err := repo.GetMembers(ctx, circleID)
		assert.ErrorContains(t, err, "getting circle members")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("reports rows that fail to scan", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(
			sqlmock.NewRows(memberColumns).AddRow(circleID.String(), userID.String(), "not-a-number", "active", now),
		)

		_, err := repo.GetMembers(ctx, circleID)
		assert.ErrorContains(t, err, "scanning circle member row")
	})

	t.Run("reports iteration failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("conn reset")
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(
			memberRows(CircleMember{CircleID: circleID, UserID: userID, Position: 1, Status: MemberStatusActive, JoinedAt: now}).RowError(0, boom),
		)

		_, err := repo.GetMembers(ctx, circleID)
		assert.ErrorContains(t, err, "iterating circle members")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetMemberCount(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()

	t.Run("counts active members", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

		n, err := repo.GetMemberCount(ctx, circleID)
		require.NoError(t, err)
		assert.Equal(t, 3, n)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FOR UPDATE").WillReturnError(boom)

		n, err := repo.GetMemberCount(ctx, circleID)
		assert.Equal(t, 0, n)
		assert.ErrorContains(t, err, "getting member count")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_UpdateMemberStatus(t *testing.T) {
	ctx := context.Background()
	circleID, userID := uuid.New(), uuid.New()

	t.Run("updates the member", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("UPDATE circle_members SET status").WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.UpdateMemberStatus(ctx, circleID, userID, MemberStatusRemoved))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns ErrNotMember when no row matches", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("UPDATE circle_members SET status").WillReturnResult(sqlmock.NewResult(0, 0))

		assert.ErrorIs(t, repo.UpdateMemberStatus(ctx, circleID, userID, MemberStatusRemoved), ErrNotMember)
	})

	t.Run("wraps update failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("UPDATE circle_members SET status").WillReturnError(boom)

		err := repo.UpdateMemberStatus(ctx, circleID, userID, MemberStatusRemoved)
		assert.ErrorContains(t, err, "updating member status")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_FindMemberByCircleAndUser(t *testing.T) {
	ctx := context.Background()
	circleID, userID := uuid.New(), uuid.New()
	now := time.Now()

	t.Run("returns the member", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(memberRows(
			CircleMember{CircleID: circleID, UserID: userID, Position: 4, Status: MemberStatusActive, JoinedAt: now},
		))

		m, err := repo.FindMemberByCircleAndUser(ctx, circleID, userID)
		require.NoError(t, err)
		assert.Equal(t, userID, m.UserID)
		assert.Equal(t, 4, m.Position)
	})

	t.Run("maps a missing row to apperrors.ErrNotFound", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(memberRows())

		_, err := repo.FindMemberByCircleAndUser(ctx, circleID, userID)
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnError(boom)

		_, err := repo.FindMemberByCircleAndUser(ctx, circleID, userID)
		assert.ErrorContains(t, err, "scanning circle member row")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_FindCirclesByUserID(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("returns the circles the user belongs to", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		organizer := uuid.New()
		rows := sqlmock.NewRows(circleColumns)
		for i := 0; i < 2; i++ {
			rows = addCircleRow(rows, uuid.New(), organizer, string(CircleStatusActive), 1, 10)
		}
		mock.ExpectQuery("INNER JOIN circle_members").WithArgs(userID).WillReturnRows(rows)

		circles, err := repo.FindCirclesByUserID(ctx, userID)
		require.NoError(t, err)
		assert.Len(t, circles, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns no circles when the user has none", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("INNER JOIN circle_members").WithArgs(userID).WillReturnRows(sqlmock.NewRows(circleColumns))

		circles, err := repo.FindCirclesByUserID(ctx, userID)
		require.NoError(t, err)
		assert.Empty(t, circles)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("INNER JOIN circle_members").WillReturnError(boom)

		_, err := repo.FindCirclesByUserID(ctx, userID)
		assert.ErrorContains(t, err, "finding circles by user ID")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("reports iteration failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("conn reset")
		rows := circleRowFor(uuid.New(), uuid.New(), string(CircleStatusActive), 1, 10).RowError(0, boom)
		mock.ExpectQuery("INNER JOIN circle_members").WillReturnRows(rows)

		_, err := repo.FindCirclesByUserID(ctx, userID)
		assert.ErrorContains(t, err, "iterating circles")
		assert.ErrorIs(t, err, boom)
	})
}

func TestIsUniqueViolationPg(t *testing.T) {
	assert.False(t, isUniqueViolationPg(nil))
	assert.False(t, isUniqueViolationPg(errors.New("plain error")))
	assert.False(t, isUniqueViolationPg(&pq.Error{Code: "23503"}), "foreign key violations are not duplicates")
	assert.True(t, isUniqueViolationPg(&pq.Error{Code: "23505"}))
}

func TestPostgresRepository_GetPenalties(t *testing.T) {
	ctx := context.Background()
	circleID, userID := uuid.New(), uuid.New()
	now := time.Now()

	penaltyRows := func() *sqlmock.Rows {
		return sqlmock.NewRows(penaltyColumns).AddRow(uuid.New().String(), circleID.String(), userID.String(), 2, string(PenaltyTypeLate), 5.5, 1, "missed round", now)
	}

	t.Run("GetPenaltiesByCircle returns penalties", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM penalties WHERE circle_id").WillReturnRows(penaltyRows())

		penalties, err := repo.GetPenaltiesByCircle(ctx, circleID)
		require.NoError(t, err)
		require.Len(t, penalties, 1)
		assert.Equal(t, PenaltyTypeLate, penalties[0].PenaltyType)
		assert.Equal(t, "missed round", penalties[0].Reason.String)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetPenaltiesByCircle surfaces query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM penalties WHERE circle_id").WillReturnError(boom)

		_, err := repo.GetPenaltiesByCircle(ctx, circleID)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("GetPenaltiesByUser returns penalties", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM penalties WHERE user_id").WillReturnRows(penaltyRows())

		penalties, err := repo.GetPenaltiesByUser(ctx, userID)
		require.NoError(t, err)
		require.Len(t, penalties, 1)
		assert.Equal(t, userID, penalties[0].UserID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetPenaltiesByUser surfaces query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM penalties WHERE user_id").WillReturnError(boom)

		_, err := repo.GetPenaltiesByUser(ctx, userID)
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetContributionsByCircleAndRound(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()
	first, second := uuid.New(), uuid.New()

	t.Run("returns contributor IDs", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM contributions WHERE circle_id").WillReturnRows(
			sqlmock.NewRows([]string{"user_id"}).AddRow(first.String()).AddRow(second.String()),
		)

		ids, err := repo.GetContributionsByCircleAndRound(ctx, circleID, 1)
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{first, second}, ids)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("surfaces query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM contributions WHERE circle_id").WillReturnError(boom)

		_, err := repo.GetContributionsByCircleAndRound(ctx, circleID, 1)
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_CreateDispute(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	d := &CircleDispute{
		ID:        uuid.New(),
		CircleID:  uuid.New(),
		RaiserID:  uuid.New(),
		Reason:    "late payout",
		Details:   sql.NullString{String: "details", Valid: true},
		Status:    "open",
		CreatedAt: now,
		UpdatedAt: now,
	}

	t.Run("inserts the dispute", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circle_disputes").WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, repo.CreateDispute(ctx, d))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("wraps insert failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("INSERT INTO circle_disputes").WillReturnError(boom)

		err := repo.CreateDispute(ctx, d)
		assert.ErrorContains(t, err, "creating dispute")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_CreateVote(t *testing.T) {
	ctx := context.Background()
	v := &CircleVote{ID: uuid.New(), CircleID: uuid.New(), VoterID: uuid.New(), RecipientID: uuid.New(), RoundNumber: 1, CreatedAt: time.Now()}

	t.Run("inserts the vote", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circle_votes").WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, repo.CreateVote(ctx, v))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps a repeat vote to ErrConflict", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circle_votes").WillReturnError(&pq.Error{Code: "23505"})

		assert.ErrorIs(t, repo.CreateVote(ctx, v), apperrors.ErrConflict)
	})

	t.Run("wraps other insert failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("INSERT INTO circle_votes").WillReturnError(boom)

		err := repo.CreateVote(ctx, v)
		assert.ErrorContains(t, err, "creating vote")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetVotesByRound(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()
	now := time.Now()

	t.Run("returns votes", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		voteID, recipient := uuid.New(), uuid.New()
		mock.ExpectQuery("FROM circle_votes").WillReturnRows(
			sqlmock.NewRows(voteColumns).AddRow(voteID.String(), circleID.String(), uuid.New().String(), recipient.String(), 1, now),
		)

		votes, err := repo.GetVotesByRound(ctx, circleID, 1)
		require.NoError(t, err)
		require.Len(t, votes, 1)
		assert.Equal(t, voteID, votes[0].ID)
		assert.Equal(t, recipient, votes[0].RecipientID)
	})

	t.Run("returns a non-nil empty slice when there are no votes", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_votes").WillReturnRows(sqlmock.NewRows(voteColumns))

		votes, err := repo.GetVotesByRound(ctx, circleID, 1)
		require.NoError(t, err)
		assert.NotNil(t, votes)
		assert.Empty(t, votes)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM circle_votes").WillReturnError(boom)

		_, err := repo.GetVotesByRound(ctx, circleID, 1)
		assert.ErrorContains(t, err, "getting votes by round")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("reports rows that fail to scan", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_votes").WillReturnRows(
			sqlmock.NewRows(voteColumns).AddRow(uuid.New().String(), circleID.String(), uuid.New().String(), uuid.New().String(), "not-a-round", now),
		)

		_, err := repo.GetVotesByRound(ctx, circleID, 1)
		assert.ErrorContains(t, err, "scanning vote")
	})
}

func TestPostgresRepository_CreateAuctionBid(t *testing.T) {
	ctx := context.Background()
	b := &CircleAuctionBid{ID: uuid.New(), CircleID: uuid.New(), BidderID: uuid.New(), RoundNumber: 1, BidAmount: 12.5, CreatedAt: time.Now()}

	t.Run("upserts the bid", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO circle_auction_bids").WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, repo.CreateAuctionBid(ctx, b))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("wraps insert failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("INSERT INTO circle_auction_bids").WillReturnError(boom)

		err := repo.CreateAuctionBid(ctx, b)
		assert.ErrorContains(t, err, "creating auction bid")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetAuctionBidsByRound(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()
	now := time.Now()

	t.Run("returns bids", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_auction_bids").WillReturnRows(
			sqlmock.NewRows(bidColumns).AddRow(uuid.New().String(), circleID.String(), uuid.New().String(), 1, 42.0, now),
		)

		bids, err := repo.GetAuctionBidsByRound(ctx, circleID, 1)
		require.NoError(t, err)
		require.Len(t, bids, 1)
		assert.Equal(t, 42.0, bids[0].BidAmount)
	})

	t.Run("returns a non-nil empty slice when there are no bids", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_auction_bids").WillReturnRows(sqlmock.NewRows(bidColumns))

		bids, err := repo.GetAuctionBidsByRound(ctx, circleID, 1)
		require.NoError(t, err)
		assert.NotNil(t, bids)
		assert.Empty(t, bids)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM circle_auction_bids").WillReturnError(boom)

		_, err := repo.GetAuctionBidsByRound(ctx, circleID, 1)
		assert.ErrorContains(t, err, "getting auction bids by round")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("reports rows that fail to scan", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM circle_auction_bids").WillReturnRows(
			sqlmock.NewRows(bidColumns).AddRow(uuid.New().String(), circleID.String(), uuid.New().String(), 1, "not-an-amount", now),
		)

		_, err := repo.GetAuctionBidsByRound(ctx, circleID, 1)
		assert.ErrorContains(t, err, "scanning auction bid")
	})
}

func TestPostgresRepository_SaveRoundConfigSnapshot(t *testing.T) {
	ctx := context.Background()
	s := &RoundConfigSnapshot{ID: uuid.New(), CircleID: uuid.New(), RoundNumber: 1, ConfigHash: "abc", ConfigJSON: "{}", CreatedAt: time.Now()}

	t.Run("saves the snapshot", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectExec("INSERT INTO round_config_snapshots").WillReturnResult(sqlmock.NewResult(1, 1))

		require.NoError(t, repo.SaveRoundConfigSnapshot(ctx, s))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("wraps insert failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectExec("INSERT INTO round_config_snapshots").WillReturnError(boom)

		err := repo.SaveRoundConfigSnapshot(ctx, s)
		assert.ErrorContains(t, err, "saving round config snapshot")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetRoundConfigSnapshot(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()
	now := time.Now()

	t.Run("returns the snapshot", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM round_config_snapshots WHERE circle_id").WillReturnRows(
			sqlmock.NewRows(configColumns).AddRow(uuid.New().String(), circleID.String(), 3, "hash-3", "{\"n\":3}", now),
		)

		s, err := repo.GetRoundConfigSnapshot(ctx, circleID, 3)
		require.NoError(t, err)
		assert.Equal(t, "hash-3", s.ConfigHash)
		assert.Equal(t, 3, s.RoundNumber)
	})

	t.Run("maps a missing row to apperrors.ErrNotFound", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectQuery("FROM round_config_snapshots WHERE circle_id").WillReturnRows(sqlmock.NewRows(configColumns))

		_, err := repo.GetRoundConfigSnapshot(ctx, circleID, 3)
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("FROM round_config_snapshots WHERE circle_id").WillReturnError(boom)

		_, err := repo.GetRoundConfigSnapshot(ctx, circleID, 3)
		assert.ErrorContains(t, err, "getting round config snapshot")
		assert.ErrorIs(t, err, boom)
	})
}

func TestPostgresRepository_GetCircleSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("member sees balances and rounds derived from the ledger", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID, memberID, otherID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		now := time.Now()

		contribs := sqlmock.NewRows(contributionColumns).
			AddRow(uuid.New().String(), circleID.String(), memberID.String(), 1, 10.0, nil, "confirmed", true, false, "pending", now, now).
			AddRow(uuid.New().String(), circleID.String(), otherID.String(), 2, 10.0, nil, "pending", false, false, "pending", now, now).
			AddRow(uuid.New().String(), circleID.String(), memberID.String(), 2, 10.0, nil, "failed", false, false, "pending", now, now)
		payouts := sqlmock.NewRows(payoutColumns).
			AddRow(uuid.New().String(), circleID.String(), memberID.String(), 1, 20.0, 1.0, nil, "random", false, "pending", now, now)
		configs := sqlmock.NewRows(configColumns).
			AddRow(uuid.New().String(), circleID.String(), 1, "hash-1", "{}", now)

		mock.ExpectBegin()
		expectSnapshotReads(mock,
			circleRowFor(circleID, organizerID, string(CircleStatusActive), 2, 10.0),
			memberRows(CircleMember{CircleID: circleID, UserID: memberID, Position: 1, Status: MemberStatusActive, JoinedAt: now}),
			contribs, payouts, configs,
		)
		mock.ExpectCommit()

		snap, err := repo.GetCircleSnapshot(ctx, circleID, memberID)
		require.NoError(t, err)
		assert.Equal(t, "member", snap.UserRole)
		assert.Equal(t, circleID, snap.Circle.ID)
		require.Len(t, snap.Rounds, 2)
		assert.Equal(t, "completed", snap.Rounds[0].Status)
		assert.NotNil(t, snap.Rounds[0].Config)
		assert.NotNil(t, snap.Rounds[0].Payout)
		assert.Equal(t, "active", snap.Rounds[1].Status)
		assert.Nil(t, snap.Rounds[1].Payout)

		assert.Equal(t, 10.0, snap.UserBalance.TotalContributed, "failed contributions are excluded")
		assert.Equal(t, 20.0, snap.UserBalance.TotalPaidOut)
		assert.Equal(t, 10.0, snap.UserBalance.NetBalance)
		assert.Equal(t, 10.0, snap.UserBalance.PendingContribution)
		assert.False(t, snap.UserBalance.IsCurrentRoundPaid)

		assert.Equal(t, 20.0, snap.CircleBalance.TotalContributed)
		assert.Equal(t, 20.0, snap.CircleBalance.TotalPaidOut)
		assert.Equal(t, 0.0, snap.CircleBalance.VaultBalance)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("organizer is classified as organizer even without membership", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID := uuid.New(), uuid.New()
		contribs, payouts, configs := emptySnapshotRows()

		mock.ExpectBegin()
		expectSnapshotReads(mock, circleRowFor(circleID, organizerID, string(CircleStatusActive), 1, 10.0), memberRows(), contribs, payouts, configs)
		mock.ExpectCommit()

		snap, err := repo.GetCircleSnapshot(ctx, circleID, organizerID)
		require.NoError(t, err)
		assert.Equal(t, "organizer", snap.UserRole)
		assert.Equal(t, 10.0, snap.UserBalance.PendingContribution)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects users who are neither organizer nor active member", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID := uuid.New(), uuid.New()
		contribs, payouts, configs := emptySnapshotRows()

		mock.ExpectBegin()
		expectSnapshotReads(mock, circleRowFor(circleID, organizerID, string(CircleStatusActive), 1, 10.0), memberRows(), contribs, payouts, configs)
		mock.ExpectRollback()

		snap, err := repo.GetCircleSnapshot(ctx, circleID, uuid.New())
		assert.Nil(t, snap)
		assert.ErrorIs(t, err, apperrors.ErrForbidden)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects a former member whose status is no longer active", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID, exitedID := uuid.New(), uuid.New(), uuid.New()
		contribs, payouts, configs := emptySnapshotRows()

		mock.ExpectBegin()
		expectSnapshotReads(mock, circleRowFor(circleID, organizerID, string(CircleStatusActive), 1, 10.0),
			memberRows(CircleMember{CircleID: circleID, UserID: exitedID, Position: 1, Status: MemberStatusExited, JoinedAt: time.Now()}),
			contribs, payouts, configs)
		mock.ExpectRollback()

		_, err := repo.GetCircleSnapshot(ctx, circleID, exitedID)
		assert.ErrorIs(t, err, apperrors.ErrForbidden)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maps a missing circle to ErrCircleNotFound and rolls back", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("FROM circles WHERE id").WillReturnRows(sqlmock.NewRows(circleColumns))
		mock.ExpectRollback()

		_, err := repo.GetCircleSnapshot(ctx, uuid.New(), uuid.New())
		assert.ErrorIs(t, err, ErrCircleNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("falls back to read committed when repeatable read is refused", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID := uuid.New(), uuid.New()
		contribs, payouts, configs := emptySnapshotRows()

		mock.ExpectBegin().WillReturnError(errors.New("isolation level not supported"))
		mock.ExpectBegin()
		expectSnapshotReads(mock, circleRowFor(circleID, organizerID, string(CircleStatusActive), 1, 10.0), memberRows(), contribs, payouts, configs)
		mock.ExpectCommit()

		snap, err := repo.GetCircleSnapshot(ctx, circleID, organizerID)
		require.NoError(t, err)
		assert.Equal(t, "organizer", snap.UserRole)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fails when no transaction can be started", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("pool exhausted")
		mock.ExpectBegin().WillReturnError(boom)
		mock.ExpectBegin().WillReturnError(boom)

		_, err := repo.GetCircleSnapshot(ctx, uuid.New(), uuid.New())
		assert.ErrorContains(t, err, "beginning snapshot transaction")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("fails when the commit is refused", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID := uuid.New(), uuid.New()
		contribs, payouts, configs := emptySnapshotRows()

		mock.ExpectBegin()
		expectSnapshotReads(mock, circleRowFor(circleID, organizerID, string(CircleStatusActive), 1, 10.0), memberRows(), contribs, payouts, configs)
		mock.ExpectCommit().WillReturnError(errors.New("commit refused"))

		_, err := repo.GetCircleSnapshot(ctx, circleID, organizerID)
		assert.ErrorContains(t, err, "committing snapshot transaction")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("treats a failed contributions read as an empty ledger", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		circleID, organizerID, memberID := uuid.New(), uuid.New(), uuid.New()
		now := time.Now()
		_, payouts, configs := emptySnapshotRows()

		mock.ExpectBegin()
		mock.ExpectQuery("FROM circles WHERE id").WillReturnRows(circleRowFor(circleID, organizerID, string(CircleStatusActive), 1, 10.0))
		mock.ExpectQuery("FROM circle_members WHERE circle_id").WillReturnRows(
			memberRows(CircleMember{CircleID: circleID, UserID: memberID, Position: 1, Status: MemberStatusActive, JoinedAt: now}),
		)
		mock.ExpectQuery("FROM contributions WHERE circle_id").WillReturnError(errors.New("boom"))
		mock.ExpectQuery("FROM payouts WHERE circle_id").WillReturnRows(payouts)
		mock.ExpectQuery("FROM round_config_snapshots WHERE circle_id").WillReturnRows(configs)
		mock.ExpectCommit()

		snap, err := repo.GetCircleSnapshot(ctx, circleID, memberID)
		require.NoError(t, err)
		assert.Equal(t, 0.0, snap.UserBalance.TotalContributed)
		assert.Equal(t, "member", snap.UserRole)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPostgresRepository_GetBulkCircleSnapshots(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	now := time.Now()

	// memberSnapshotReads queues a full, successful snapshot read in which userID is an active member.
	memberSnapshotReads := func(mock sqlmock.Sqlmock, circleID uuid.UUID) {
		contribs, payouts, configs := emptySnapshotRows()
		mock.ExpectBegin()
		expectSnapshotReads(mock,
			circleRowFor(circleID, uuid.New(), string(CircleStatusActive), 1, 10.0),
			memberRows(CircleMember{CircleID: circleID, UserID: userID, Position: 1, Status: MemberStatusActive, JoinedAt: now}),
			contribs, payouts, configs,
		)
		mock.ExpectCommit()
	}

	t.Run("without IDs, snapshots every circle the user belongs to", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		first, second := uuid.New(), uuid.New()
		mock.ExpectQuery("SELECT DISTINCT circle_id FROM circle_members").WithArgs(userID).WillReturnRows(
			sqlmock.NewRows([]string{"circle_id"}).AddRow(first.String()).AddRow(second.String()),
		)
		memberSnapshotReads(mock, first)
		memberSnapshotReads(mock, second)

		snaps, err := repo.GetBulkCircleSnapshots(ctx, userID, nil)
		require.NoError(t, err)
		require.Len(t, snaps, 2)
		assert.Equal(t, first, snaps[0].Circle.ID)
		assert.Equal(t, second, snaps[1].Circle.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("without IDs, surfaces failures to list the user's circles", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		boom := errors.New("boom")
		mock.ExpectQuery("SELECT DISTINCT circle_id FROM circle_members").WillReturnError(boom)

		_, err := repo.GetBulkCircleSnapshots(ctx, userID, nil)
		assert.ErrorContains(t, err, "fetching user circle IDs")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("with IDs, stops and returns the error when one circle is forbidden", func(t *testing.T) {
		repo, _, mock := newPgRepoMock(t)
		allowed, denied := uuid.New(), uuid.New()
		memberSnapshotReads(mock, allowed)

		mock.ExpectBegin()
		contribs, payouts, configs := emptySnapshotRows()
		expectSnapshotReads(mock, circleRowFor(denied, uuid.New(), string(CircleStatusActive), 1, 10.0), memberRows(), contribs, payouts, configs)
		mock.ExpectRollback()

		snaps, err := repo.GetBulkCircleSnapshots(ctx, userID, []uuid.UUID{allowed, denied})
		assert.Nil(t, snaps)
		assert.ErrorIs(t, err, apperrors.ErrForbidden)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
