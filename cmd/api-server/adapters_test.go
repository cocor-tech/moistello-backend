package main

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/community"
	communityMocks "github.com/moistello/backend/internal/domain/community/mocks"
	"github.com/moistello/backend/internal/domain/notification"
	"github.com/moistello/backend/internal/domain/reputation"
	reputationMocks "github.com/moistello/backend/internal/domain/reputation/mocks"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
)

// fakeTokenService implements token.Service for the governance weight tests.
// Only GetBalance is exercised; the other methods are unused by the adapter.
type fakeTokenService struct {
	balance uint64
	err     error
}

func (f *fakeTokenService) GetBalance(_ context.Context, _ string) (uint64, error) {
	return f.balance, f.err
}

func (f *fakeTokenService) Stake(_ context.Context, _ string, _ uint64) (string, error) {
	return "", nil
}

func (f *fakeTokenService) Unstake(_ context.Context, _ string, _ uint64) (string, error) {
	return "", nil
}

func (f *fakeTokenService) GetStakedAmount(_ context.Context, _ string) (uint64, error) {
	return 0, nil
}

func TestMoiAdapter_FindByID(t *testing.T) {
	t.Run("maps the user's MOI score", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, MoiScore: 420}, nil).Once()

		data, err := (&moiAdapter{repo: repo}).FindByID(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, 420, data.MoiScore)
		repo.AssertExpectations(t)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(nil, errors.New("db down")).Once()

		data, err := (&moiAdapter{repo: repo}).FindByID(context.Background(), id)
		assert.EqualError(t, err, "db down")
		assert.Nil(t, data)
		repo.AssertExpectations(t)
	})
}

func TestPayoutWalletAdapter_WalletAddressForUser(t *testing.T) {
	t.Run("returns the wallet address", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, WalletAddress: "GWALLET"}, nil).Once()

		addr, err := (&payoutWalletAdapter{repo: repo}).WalletAddressForUser(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, "GWALLET", addr)
		repo.AssertExpectations(t)
	})

	t.Run("returns empty address and error when the user is missing", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(nil, errors.New("not found")).Once()

		addr, err := (&payoutWalletAdapter{repo: repo}).WalletAddressForUser(context.Background(), id)
		assert.EqualError(t, err, "not found")
		assert.Empty(t, addr)
		repo.AssertExpectations(t)
	})
}

func TestCommunityAdapter_IsMember(t *testing.T) {
	communityID, userID := uuid.New(), uuid.New()

	t.Run("delegates membership to the repository", func(t *testing.T) {
		repo := new(communityMocks.Repository)
		repo.On("IsMember", mock.Anything, communityID, userID).Return(true, nil).Once()

		ok, err := (&communityAdapter{repo: community.Repository(repo)}).IsMember(context.Background(), communityID, userID)
		require.NoError(t, err)
		assert.True(t, ok)
		repo.AssertExpectations(t)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		repo := new(communityMocks.Repository)
		repo.On("IsMember", mock.Anything, communityID, userID).Return(false, errors.New("boom")).Once()

		ok, err := (&communityAdapter{repo: community.Repository(repo)}).IsMember(context.Background(), communityID, userID)
		assert.EqualError(t, err, "boom")
		assert.False(t, ok)
		repo.AssertExpectations(t)
	})
}

func TestUserLookupAdapter_FindRecipient(t *testing.T) {
	t.Run("rejects a malformed user id without touching the repository", func(t *testing.T) {
		repo := new(userMocks.Repository)

		_, err := (&userLookupAdapter{repo: repo}).FindRecipient(context.Background(), "not-a-uuid")
		assert.Error(t, err)
		repo.AssertNotCalled(t, "FindByID", mock.Anything, mock.Anything)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(nil, errors.New("missing")).Once()

		_, err := (&userLookupAdapter{repo: repo}).FindRecipient(context.Background(), id.String())
		assert.EqualError(t, err, "missing")
		repo.AssertExpectations(t)
	})

	t.Run("maps contact details, channels and mute flag", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		email, phone, push := "a@example.com", "+2348000000000", "push-token"
		repo.On("FindByID", mock.Anything, id).Return(&user.User{
			ID:                   id,
			Email:                &email,
			Phone:                &phone,
			PushToken:            &push,
			NotificationChannels: []string{"email", "push"},
			NotificationsMuted:   true,
		}, nil).Once()

		r, err := (&userLookupAdapter{repo: repo}).FindRecipient(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, &email, r.Email)
		assert.Equal(t, &phone, r.Phone)
		assert.Equal(t, &push, r.PushToken)
		assert.Equal(t, []string{"email", "push"}, r.PreferredChannels)
		assert.True(t, r.Muted)
		repo.AssertExpectations(t)
	})
}

func TestDigestPreferenceAdapter_DigestPreferences(t *testing.T) {
	t.Run("converts the stored interval from minutes to a duration", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(&user.User{
			ID:                    id,
			DigestEnabled:         true,
			DigestIntervalMinutes: 90,
		}, nil).Once()

		prefs, err := (&digestPreferenceAdapter{repo: repo}).DigestPreferences(context.Background(), id.String())
		require.NoError(t, err)
		assert.True(t, prefs.Enabled)
		assert.Equal(t, 90*time.Minute, prefs.Interval)
		repo.AssertExpectations(t)
	})

	t.Run("rejects a malformed user id", func(t *testing.T) {
		repo := new(userMocks.Repository)

		prefs, err := (&digestPreferenceAdapter{repo: repo}).DigestPreferences(context.Background(), "bad")
		assert.Error(t, err)
		assert.Equal(t, notification.DigestPreferences{}, prefs)
		repo.AssertNotCalled(t, "FindByID", mock.Anything, mock.Anything)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		repo := new(userMocks.Repository)
		id := uuid.New()
		repo.On("FindByID", mock.Anything, id).Return(nil, errors.New("gone")).Once()

		_, err := (&digestPreferenceAdapter{repo: repo}).DigestPreferences(context.Background(), id.String())
		assert.EqualError(t, err, "gone")
		repo.AssertExpectations(t)
	})
}

func TestGovernanceWeightResolver_VotingWeight(t *testing.T) {
	id := uuid.New()
	wallet := "GVOTER"

	t.Run("rejects a malformed user id", func(t *testing.T) {
		r := &governanceWeightResolver{users: new(userMocks.Repository)}

		w, err := r.VotingWeight(context.Background(), "bad")
		assert.Error(t, err)
		assert.Zero(t, w)
	})

	t.Run("propagates user lookup errors", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(nil, errors.New("no user")).Once()
		r := &governanceWeightResolver{users: users}

		w, err := r.VotingWeight(context.Background(), id.String())
		assert.EqualError(t, err, "no user")
		assert.Zero(t, w)
		users.AssertExpectations(t)
	})

	t.Run("without a token service counts the baseline scaled by tier", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, WalletAddress: wallet}, nil).Once()
		reps := new(reputationMocks.Repository)
		reps.On("GetByUser", mock.Anything, id).Return(&reputation.ReputationSnapshot{Level: "Gold"}, nil).Once()
		r := &governanceWeightResolver{users: users, reputation: reps, tokens: nil}

		w, err := r.VotingWeight(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, int64(1), w, "baseline of 1 scaled by Gold (150%) and truncated to 1")
		users.AssertExpectations(t)
		reps.AssertExpectations(t)
	})

	t.Run("a missing user wallet skips the token balance lookup", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(&user.User{ID: id}, nil).Once()
		reps := new(reputationMocks.Repository)
		reps.On("GetByUser", mock.Anything, id).Return(nil, errors.New("no snapshot")).Once()
		tokens := &fakeTokenService{balance: 1000}
		r := &governanceWeightResolver{users: users, reputation: reps, tokens: tokens}

		w, err := r.VotingWeight(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, int64(1), w)
	})

	t.Run("weights the token balance by the reputation tier", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, WalletAddress: wallet}, nil).Once()
		reps := new(reputationMocks.Repository)
		reps.On("GetByUser", mock.Anything, id).Return(&reputation.ReputationSnapshot{Level: "Silver"}, nil).Once()
		tokens := &fakeTokenService{balance: 1000}
		r := &governanceWeightResolver{users: users, reputation: reps, tokens: tokens}

		w, err := r.VotingWeight(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, int64(1250), w)
		users.AssertExpectations(t)
		reps.AssertExpectations(t)
	})

	t.Run("an unknown tier falls back to the default factor", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, WalletAddress: wallet}, nil).Once()
		reps := new(reputationMocks.Repository)
		reps.On("GetByUser", mock.Anything, id).Return(&reputation.ReputationSnapshot{Level: "Mythic"}, nil).Once()
		tokens := &fakeTokenService{balance: 1000}
		r := &governanceWeightResolver{users: users, reputation: reps, tokens: tokens}

		w, err := r.VotingWeight(context.Background(), id.String())
		require.NoError(t, err)
		assert.Equal(t, int64(1000), w)
	})

	t.Run("propagates token balance errors", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, WalletAddress: wallet}, nil).Once()
		reps := new(reputationMocks.Repository)
		reps.On("GetByUser", mock.Anything, id).Return(nil, nil).Once()
		tokens := &fakeTokenService{err: errors.New("rpc unavailable")}
		r := &governanceWeightResolver{users: users, reputation: reps, tokens: tokens}

		w, err := r.VotingWeight(context.Background(), id.String())
		assert.EqualError(t, err, "rpc unavailable")
		assert.Zero(t, w)
	})

	t.Run("rejects balances that overflow the voting weight", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("FindByID", mock.Anything, id).Return(&user.User{ID: id, WalletAddress: wallet}, nil).Once()
		reps := new(reputationMocks.Repository)
		reps.On("GetByUser", mock.Anything, id).Return(nil, nil).Once()
		tokens := &fakeTokenService{balance: uint64(math.MaxInt64) + 1}
		r := &governanceWeightResolver{users: users, reputation: reps, tokens: tokens}

		w, err := r.VotingWeight(context.Background(), id.String())
		assert.ErrorContains(t, err, "exceeds supported voting weight")
		assert.Zero(t, w)
	})
}

func TestGovernanceWeightResolver_EligibleVoters(t *testing.T) {
	t.Run("returns every user from a single short page", func(t *testing.T) {
		a, b := uuid.New(), uuid.New()
		users := new(userMocks.Repository)
		users.On("List", mock.Anything, user.UserFilter{Page: 1, Limit: 200}).
			Return([]user.User{{ID: a}, {ID: b}}, nil).Once()
		r := &governanceWeightResolver{users: users}

		ids, err := r.EligibleVoters(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []string{a.String(), b.String()}, ids)
		users.AssertExpectations(t)
	})

	t.Run("continues to the next page when a page is full", func(t *testing.T) {
		full := make([]user.User, 200)
		for i := range full {
			full[i] = user.User{ID: uuid.New()}
		}
		last := user.User{ID: uuid.New()}

		users := new(userMocks.Repository)
		users.On("List", mock.Anything, user.UserFilter{Page: 1, Limit: 200}).Return(full, nil).Once()
		users.On("List", mock.Anything, user.UserFilter{Page: 2, Limit: 200}).Return([]user.User{last}, nil).Once()
		r := &governanceWeightResolver{users: users}

		ids, err := r.EligibleVoters(context.Background())
		require.NoError(t, err)
		assert.Len(t, ids, 201)
		assert.Equal(t, last.ID.String(), ids[200])
		users.AssertExpectations(t)
	})

	t.Run("propagates list errors", func(t *testing.T) {
		users := new(userMocks.Repository)
		users.On("List", mock.Anything, user.UserFilter{Page: 1, Limit: 200}).Return(nil, errors.New("list failed")).Once()
		r := &governanceWeightResolver{users: users}

		ids, err := r.EligibleVoters(context.Background())
		assert.EqualError(t, err, "list failed")
		assert.Nil(t, ids)
		users.AssertExpectations(t)
	})
}
