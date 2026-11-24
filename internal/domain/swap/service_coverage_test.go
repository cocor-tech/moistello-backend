package swap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/apperrors"
)

var errBoom = errors.New("boom")

func validSwapRequest() SwapOfferRequest {
	return SwapOfferRequest{
		CircleID:        "circle-1",
		OfferorAsset:    "XLM",
		OfferorAmount:   10,
		RequestedAsset:  "USDC",
		RequestedAmount: 20,
		ExpiresIn:       24,
	}
}

func okUsers() *fakeUserService {
	return &fakeUserService{getByIDFn: func(_ context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
}

func okEscrow() *fakeEscrow {
	return &fakeEscrow{
		createSwapFn: func(context.Context, string, string, string, string, int64, string, int64, uint64) (string, error) {
			return "tx", nil
		},
		cancelSwapFn: func(context.Context, string, string) (string, error) { return "tx", nil },
		acceptSwapFn: func(context.Context, string, string) (string, error) { return "tx", nil },
	}
}

// ── CreateSwapOffer ───────────────────────────────────────────────────────

func TestCreateSwapOffer_OfferorNotFound(t *testing.T) {
	users := &fakeUserService{getByIDFn: func(context.Context, string) (*user.User, error) { return nil, errBoom }}
	svc := NewService(&fakeRepo{}, nil, users, okEscrow())

	_, err := svc.CreateSwapOffer(context.Background(), "u1", validSwapRequest())
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestCreateSwapOffer_OffereeNotFound(t *testing.T) {
	users := &fakeUserService{getByIDFn: func(_ context.Context, id string) (*user.User, error) {
		if id == "u2" {
			return nil, errBoom
		}
		return walletUser(id), nil
	}}
	svc := NewService(&fakeRepo{}, nil, users, okEscrow())
	input := validSwapRequest()
	offeree := "u2"
	input.OffereeUserID = &offeree

	_, err := svc.CreateSwapOffer(context.Background(), "u1", input)
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestCreateSwapOffer_EscrowFailure(t *testing.T) {
	escrow := okEscrow()
	escrow.createSwapFn = func(context.Context, string, string, string, string, int64, string, int64, uint64) (string, error) {
		return "", errBoom
	}
	repo := &fakeRepo{createFn: func(context.Context, *SwapOffer) error {
		t.Fatal("repo must not be written when escrow fails")
		return nil
	}}
	svc := NewService(repo, nil, okUsers(), escrow)

	_, err := svc.CreateSwapOffer(context.Background(), "u1", validSwapRequest())
	require.ErrorIs(t, err, errBoom)
}

func TestCreateSwapOffer_RepoFailure(t *testing.T) {
	repo := &fakeRepo{createFn: func(context.Context, *SwapOffer) error { return errBoom }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.CreateSwapOffer(context.Background(), "u1", validSwapRequest())
	assert.ErrorIs(t, err, errBoom)
}

func TestCreateSwapOffer_Success(t *testing.T) {
	var gotOfferee string
	escrow := okEscrow()
	escrow.createSwapFn = func(_ context.Context, _, _, offeree string, _ string, _ int64, _ string, _ int64, _ uint64) (string, error) {
		gotOfferee = offeree
		return "tx", nil
	}
	var saved *SwapOffer
	repo := &fakeRepo{createFn: func(_ context.Context, o *SwapOffer) error {
		saved = o
		return nil
	}}
	svc := NewService(repo, nil, okUsers(), escrow)
	input := validSwapRequest()
	offeree := "u2"
	input.OffereeUserID = &offeree

	offer, err := svc.CreateSwapOffer(context.Background(), "u1", input)
	require.NoError(t, err)
	assert.Equal(t, saved, offer)
	assert.Equal(t, SwapOfferStatusCreated, offer.Status)
	assert.Equal(t, "u1", offer.OfferorUserID)
	assert.Equal(t, "u2", *offer.OffereeUserID)
	assert.Equal(t, "Gu2WALLET", gotOfferee)
	assert.True(t, offer.ExpiresAt.After(time.Now()))
}

// ── AcceptSwapOffer ───────────────────────────────────────────────────────

func TestAcceptSwapOffer_OfferNotFound(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return nil, apperrors.ErrNotFound }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestAcceptSwapOffer_NotInCreatedState(t *testing.T) {
	offer := createdOffer("offer-1", "u1")
	offer.Status = SwapOfferStatusAccepted
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return offer, nil }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrConflict)
}

func TestAcceptSwapOffer_Expired(t *testing.T) {
	offer := createdOffer("offer-1", "u1")
	offer.ExpiresAt = time.Now().Add(-time.Minute)
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return offer, nil }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrInvalidInput)
}

func TestAcceptSwapOffer_WrongOfferee(t *testing.T) {
	offer := createdOffer("offer-1", "u1")
	other := "u3"
	offer.OffereeUserID = &other
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return offer, nil }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrForbidden)
}

func TestAcceptSwapOffer_AcceptorNotFound(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil }}
	users := &fakeUserService{getByIDFn: func(context.Context, string) (*user.User, error) { return nil, errBoom }}
	svc := NewService(repo, nil, users, okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestAcceptSwapOffer_CASError(t *testing.T) {
	repo := &fakeRepo{
		getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil },
		casFn: func(context.Context, string, SwapOfferStatus, SwapOfferStatus, *string) (bool, error) {
			return false, errBoom
		},
	}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, errBoom)
}

func TestAcceptSwapOffer_CASLostRace(t *testing.T) {
	repo := &fakeRepo{
		getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil },
		casFn: func(context.Context, string, SwapOfferStatus, SwapOfferStatus, *string) (bool, error) {
			return false, nil
		},
	}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrConflict)
}

func TestAcceptSwapOffer_EscrowFailureRollsBackToCreated(t *testing.T) {
	var casCalls []SwapOfferStatus
	repo := &fakeRepo{
		getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil },
		casFn: func(_ context.Context, _ string, _, newStatus SwapOfferStatus, _ *string) (bool, error) {
			casCalls = append(casCalls, newStatus)
			return true, nil
		},
	}
	escrow := okEscrow()
	escrow.acceptSwapFn = func(context.Context, string, string) (string, error) { return "", errBoom }
	svc := NewService(repo, nil, okUsers(), escrow)

	_, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, []SwapOfferStatus{SwapOfferStatusAccepted, SwapOfferStatusCreated}, casCalls)
}

func TestAcceptSwapOffer_Success(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	offer, err := svc.AcceptSwapOffer(context.Background(), "u2", "offer-1")
	require.NoError(t, err)
	assert.Equal(t, SwapOfferStatusAccepted, offer.Status)
	require.NotNil(t, offer.TransactionHash)
	assert.Equal(t, "tx", *offer.TransactionHash)
	assert.Equal(t, []SwapOfferStatus{SwapOfferStatusAccepted}, repo.updatedStatuses)
}

// ── CancelSwapOffer ───────────────────────────────────────────────────────

func TestCancelSwapOffer_OfferNotFound(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return nil, apperrors.ErrNotFound }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.CancelSwapOffer(context.Background(), "u1", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestCancelSwapOffer_NotInCreatedState(t *testing.T) {
	offer := createdOffer("offer-1", "u1")
	offer.Status = SwapOfferStatusCancelled
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return offer, nil }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.CancelSwapOffer(context.Background(), "u1", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrConflict)
}

func TestCancelSwapOffer_NotOfferor(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.CancelSwapOffer(context.Background(), "u2", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrForbidden)
}

func TestCancelSwapOffer_CancellerNotFound(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil }}
	users := &fakeUserService{getByIDFn: func(context.Context, string) (*user.User, error) { return nil, errBoom }}
	svc := NewService(repo, nil, users, okEscrow())

	_, err := svc.CancelSwapOffer(context.Background(), "u1", "offer-1")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestCancelSwapOffer_EscrowFailure(t *testing.T) {
	repo := &fakeRepo{getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil }}
	escrow := okEscrow()
	escrow.cancelSwapFn = func(context.Context, string, string) (string, error) { return "", errBoom }
	svc := NewService(repo, nil, okUsers(), escrow)

	_, err := svc.CancelSwapOffer(context.Background(), "u1", "offer-1")
	assert.ErrorIs(t, err, errBoom)
	assert.Empty(t, repo.updatedStatuses)
}

func TestCancelSwapOffer_StatusUpdateFailure(t *testing.T) {
	repo := &fakeRepo{
		getByIDFn: func(context.Context, string) (*SwapOffer, error) { return createdOffer("offer-1", "u1"), nil },
		updateFn:  func(context.Context, string, SwapOfferStatus, *string) error { return errBoom },
	}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, err := svc.CancelSwapOffer(context.Background(), "u1", "offer-1")
	assert.ErrorIs(t, err, errBoom)
}

// ── History delegation ────────────────────────────────────────────────────

func TestGetSwapHistory_DelegatesToRepo(t *testing.T) {
	repo := &fakeRepo{listUserFn: func(_ context.Context, userID string, _ SwapHistoryFilter) ([]SwapOffer, int, error) {
		assert.Equal(t, "u1", userID)
		return []SwapOffer{{ID: "a"}}, 1, nil
	}}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	offers, total, err := svc.GetSwapHistory(context.Background(), "u1", SwapHistoryFilter{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, offers, 1)
}

func TestGetSwapHistory_PropagatesRepoError(t *testing.T) {
	repo := &fakeRepo{listUserFn: func(context.Context, string, SwapHistoryFilter) ([]SwapOffer, int, error) {
		return nil, 0, errBoom
	}}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, _, err := svc.GetSwapHistory(context.Background(), "u1", SwapHistoryFilter{Limit: 10})
	assert.ErrorIs(t, err, errBoom)
}

func TestGetCircleSwapHistory_DelegatesToRepo(t *testing.T) {
	repo := &fakeRepo{listCircleFn: func(_ context.Context, circleID string, _ SwapHistoryFilter) ([]SwapOffer, int, error) {
		assert.Equal(t, "c1", circleID)
		return []SwapOffer{{ID: "a"}, {ID: "b"}}, 2, nil
	}}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	offers, total, err := svc.GetCircleSwapHistory(context.Background(), "c1", SwapHistoryFilter{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, offers, 2)
}

func TestGetCircleSwapHistory_PropagatesRepoError(t *testing.T) {
	repo := &fakeRepo{listCircleFn: func(context.Context, string, SwapHistoryFilter) ([]SwapOffer, int, error) {
		return nil, 0, errBoom
	}}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	_, _, err := svc.GetCircleSwapHistory(context.Background(), "c1", SwapHistoryFilter{Limit: 10})
	assert.ErrorIs(t, err, errBoom)
}

// ── SweepExpiredOffers error paths ────────────────────────────────────────

func TestSweepExpiredOffers_ListError(t *testing.T) {
	repo := &fakeRepo{listExpiredFn: func(context.Context, time.Time) ([]SwapOffer, error) { return nil, errBoom }}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	swept, err := svc.SweepExpiredOffers(context.Background())
	assert.ErrorIs(t, err, errBoom)
	assert.Equal(t, 0, swept)
}

func TestSweepExpiredOffers_FinalizeFailureReturnsClaim(t *testing.T) {
	offers := []SwapOffer{*createdOffer("offer-1", "u1")}
	repo := &fakeRepo{
		listExpiredFn:   func(context.Context, time.Time) ([]SwapOffer, error) { return offers, nil },
		finalizeSweepFn: func(context.Context, string, *string) error { return errBoom },
	}
	svc := NewService(repo, nil, okUsers(), okEscrow())

	swept, err := svc.SweepExpiredOffers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, swept)
	assert.Equal(t, []string{"offer-1"}, repo.releasedClaimIDs)
}

func TestSweepExpiredOffers_ReleaseClaimFailureIsLoggedNotFatal(t *testing.T) {
	offers := []SwapOffer{*createdOffer("offer-1", "u1")}
	repo := &fakeRepo{
		listExpiredFn:  func(context.Context, time.Time) ([]SwapOffer, error) { return offers, nil },
		releaseClaimFn: func(context.Context, string) error { return errBoom },
	}
	users := &fakeUserService{getByIDFn: func(context.Context, string) (*user.User, error) { return nil, errBoom }}
	svc := NewService(repo, nil, users, okEscrow())

	swept, err := svc.SweepExpiredOffers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, swept)
	assert.Equal(t, []string{"offer-1"}, repo.releasedClaimIDs)
}
