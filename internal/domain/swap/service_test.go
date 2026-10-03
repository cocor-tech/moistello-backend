package swap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/user"
)

// ── Fakes ─────────────────────────────────────────────────────────────────

type fakeUserService struct {
	getByIDFn func(ctx context.Context, id string) (*user.User, error)
}

func (f *fakeUserService) GetByID(ctx context.Context, id string) (*user.User, error) {
	return f.getByIDFn(ctx, id)
}

type fakeEscrow struct {
	createSwapFn  func(ctx context.Context, circleID, offeror, offeree string, offerorAsset string, offerorAmount int64, requestedAsset string, requestedAmount int64, expiresAt uint64) (string, error)
	acceptSwapFn  func(ctx context.Context, swapID string, acceptor string) (string, error)
	cancelSwapFn  func(ctx context.Context, swapID string, canceller string) (string, error)
	executeSwapFn func(ctx context.Context, swapID string) (string, error)
}

func (f *fakeEscrow) CreateSwap(ctx context.Context, circleID, offeror, offeree string, offerorAsset string, offerorAmount int64, requestedAsset string, requestedAmount int64, expiresAt uint64) (string, error) {
	return f.createSwapFn(ctx, circleID, offeror, offeree, offerorAsset, offerorAmount, requestedAsset, requestedAmount, expiresAt)
}

func (f *fakeEscrow) AcceptSwap(ctx context.Context, swapID string, acceptor string) (string, error) {
	return f.acceptSwapFn(ctx, swapID, acceptor)
}

func (f *fakeEscrow) CancelSwap(ctx context.Context, swapID string, canceller string) (string, error) {
	return f.cancelSwapFn(ctx, swapID, canceller)
}

func (f *fakeEscrow) ExecuteSwap(ctx context.Context, swapID string) (string, error) {
	return f.executeSwapFn(ctx, swapID)
}

type fakeRepo struct {
	createFn           func(ctx context.Context, offer *SwapOffer) error
	getByIDFn          func(ctx context.Context, id string) (*SwapOffer, error)
	updateFn           func(ctx context.Context, id string, status SwapOfferStatus, transactionHash *string) error
	casFn              func(ctx context.Context, id string, expectedStatus, newStatus SwapOfferStatus, transactionHash *string) (bool, error)
	listUserFn         func(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, error)
	listCircleFn       func(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error)
	listExpiredFn      func(ctx context.Context, now time.Time) ([]SwapOffer, error)
	claimFn            func(ctx context.Context, id string, now time.Time) (bool, error)
	releaseClaimFn     func(ctx context.Context, id string) error
	finalizeSweepFn    func(ctx context.Context, id string, transactionHash *string) error
	updatedStatuses    []SwapOfferStatus
	updatedIDs         []string
	claimedIDs         []string
	releasedClaimIDs   []string
	finalizedIDs       []string
	mu                 sync.Mutex
}

func (f *fakeRepo) CreateSwapOffer(ctx context.Context, offer *SwapOffer) error {
	return f.createFn(ctx, offer)
}

func (f *fakeRepo) GetSwapOfferByID(ctx context.Context, id string) (*SwapOffer, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeRepo) UpdateSwapOfferStatus(ctx context.Context, id string, status SwapOfferStatus, transactionHash *string) error {
	f.updatedStatuses = append(f.updatedStatuses, status)
	f.updatedIDs = append(f.updatedIDs, id)
	if f.updateFn != nil {
		return f.updateFn(ctx, id, status, transactionHash)
	}
	return nil
}

func (f *fakeRepo) CompareAndSwapStatus(ctx context.Context, id string, expectedStatus, newStatus SwapOfferStatus, transactionHash *string) (bool, error) {
	if f.casFn != nil {
		return f.casFn(ctx, id, expectedStatus, newStatus, transactionHash)
	}
	return true, nil
}

func (f *fakeRepo) ListUserSwapOffers(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	return f.listUserFn(ctx, userID, filter)
}

func (f *fakeRepo) ListCircleSwapOffers(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	return f.listCircleFn(ctx, circleID, filter)
}

func (f *fakeRepo) ListExpiredCreatedOffers(ctx context.Context, now time.Time) ([]SwapOffer, error) {
	return f.listExpiredFn(ctx, now)
}

// ClaimOfferForSweep records the claim and delegates to claimFn. A nil claimFn
// grants the claim, which keeps the pre-#416 sweep tests working unchanged.
func (f *fakeRepo) ClaimOfferForSweep(ctx context.Context, id string, now time.Time) (bool, error) {
	f.mu.Lock()
	f.claimedIDs = append(f.claimedIDs, id)
	f.mu.Unlock()
	if f.claimFn != nil {
		return f.claimFn(ctx, id, now)
	}
	return true, nil
}

func (f *fakeRepo) ReleaseSweepClaim(ctx context.Context, id string) error {
	f.mu.Lock()
	f.releasedClaimIDs = append(f.releasedClaimIDs, id)
	f.mu.Unlock()
	if f.releaseClaimFn != nil {
		return f.releaseClaimFn(ctx, id)
	}
	return nil
}

func (f *fakeRepo) FinalizeSweep(ctx context.Context, id string, transactionHash *string) error {
	f.mu.Lock()
	f.finalizedIDs = append(f.finalizedIDs, id)
	f.mu.Unlock()
	if f.finalizeSweepFn != nil {
		return f.finalizeSweepFn(ctx, id, transactionHash)
	}
	return nil
}

func walletUser(id string) *user.User {
	return &user.User{WalletAddress: "G" + id + "WALLET"}
}

func createdOffer(id, offeror string) *SwapOffer {
	return &SwapOffer{
		ID:            id,
		OfferorUserID: offeror,
		Status:        SwapOfferStatusCreated,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
	}
}

// ── Sweep worker ──────────────────────────────────────────────────────────

func TestSweepExpiredOffers_ReleasesEscrowAndMarksExpired(t *testing.T) {
	ctx := context.Background()
	offers := []SwapOffer{*createdOffer("offer-1", "u1"), *createdOffer("offer-2", "u2")}
	repo := &fakeRepo{listExpiredFn: func(ctx context.Context, now time.Time) ([]SwapOffer, error) {
		return offers, nil
	}}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
	var cancelled []string
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		cancelled = append(cancelled, swapID+":"+canceller)
		return "tx-" + swapID, nil
	}}

	svc := NewService(repo, nil, users, escrow)
	swept, err := svc.SweepExpiredOffers(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, swept)
	assert.Equal(t, []string{"offer-1:Gu1WALLET", "offer-2:Gu2WALLET"}, cancelled)
	// #416: every offer is claimed before escrow is touched, then finalized.
	assert.ElementsMatch(t, []string{"offer-1", "offer-2"}, repo.claimedIDs)
	assert.ElementsMatch(t, []string{"offer-1", "offer-2"}, repo.finalizedIDs)
	assert.Empty(t, repo.releasedClaimIDs, "a successful sweep returns no claims")
}

// The core #416 guarantee: a caller that loses the claim must never touch
// escrow, so a lost race costs wasted work rather than a double release.
func TestSweepExpiredOffers_LosingTheClaimSkipsEscrowEntirely(t *testing.T) {
	ctx := context.Background()
	offers := []SwapOffer{*createdOffer("offer-1", "u1"), *createdOffer("offer-2", "u2")}
	repo := &fakeRepo{
		listExpiredFn: func(ctx context.Context, now time.Time) ([]SwapOffer, error) {
			return offers, nil
		},
		claimFn: func(ctx context.Context, id string, now time.Time) (bool, error) {
			// Simulate another replica having already claimed offer-1.
			return id != "offer-1", nil
		},
	}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
	var cancelled []string
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		cancelled = append(cancelled, swapID)
		return "tx-" + swapID, nil
	}}

	svc := NewService(repo, nil, users, escrow)
	swept, err := svc.SweepExpiredOffers(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, swept)
	assert.Equal(t, []string{"offer-2"}, cancelled, "the unclaimed offer must never reach escrow")
	assert.Equal(t, []string{"offer-2"}, repo.finalizedIDs)
}

// A claim error must be treated as "not claimed" — never as permission to
// proceed, and never as a reason to abort the whole pass.
func TestSweepExpiredOffers_ClaimErrorSkipsOfferButContinuesPass(t *testing.T) {
	ctx := context.Background()
	offers := []SwapOffer{*createdOffer("offer-1", "u1"), *createdOffer("offer-2", "u2")}
	repo := &fakeRepo{
		listExpiredFn: func(ctx context.Context, now time.Time) ([]SwapOffer, error) {
			return offers, nil
		},
		claimFn: func(ctx context.Context, id string, now time.Time) (bool, error) {
			if id == "offer-1" {
				return false, errors.New("deadlock detected")
			}
			return true, nil
		},
	}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
	var cancelled []string
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		cancelled = append(cancelled, swapID)
		return "tx-" + swapID, nil
	}}

	svc := NewService(repo, nil, users, escrow)
	swept, err := svc.SweepExpiredOffers(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, swept)
	assert.Equal(t, []string{"offer-2"}, cancelled, "one failing claim must not stall the pass")
}

// A failed release must hand the claim back so a later tick retries, rather
// than stranding the offer in the sweeping state forever.
func TestSweepExpiredOffers_FailedReleaseReturnsClaimForRetry(t *testing.T) {
	ctx := context.Background()
	offers := []SwapOffer{*createdOffer("offer-1", "u1")}
	repo := &fakeRepo{listExpiredFn: func(ctx context.Context, now time.Time) ([]SwapOffer, error) {
		return offers, nil
	}}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		return "", errors.New("simulation failed")
	}}

	svc := NewService(repo, nil, users, escrow)
	swept, err := svc.SweepExpiredOffers(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, swept)
	assert.Equal(t, []string{"offer-1"}, repo.releasedClaimIDs, "the claim must be returned so a later tick retries")
	assert.Empty(t, repo.finalizedIDs)
}

func TestSweepExpiredOffers_SkipsOfferWhenOnChainCancelFails(t *testing.T) {
	ctx := context.Background()
	offers := []SwapOffer{*createdOffer("offer-1", "u1"), *createdOffer("offer-2", "u2")}
	repo := &fakeRepo{listExpiredFn: func(ctx context.Context, now time.Time) ([]SwapOffer, error) {
		return offers, nil
	}}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		if swapID == "offer-1" {
			return "", errors.New("simulation failed")
		}
		return "tx-" + swapID, nil
	}}

	svc := NewService(repo, nil, users, escrow)
	swept, err := svc.SweepExpiredOffers(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, swept)
}

func TestSweepExpiredOffers_SkipsOfferWhenOfferorUnresolvable(t *testing.T) {
	ctx := context.Background()
	offers := []SwapOffer{*createdOffer("offer-1", "gone-user")}
	repo := &fakeRepo{listExpiredFn: func(ctx context.Context, now time.Time) ([]SwapOffer, error) {
		return offers, nil
	}}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return nil, errors.New("user not found")
	}}
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		return "tx", nil
	}}

	svc := NewService(repo, nil, users, escrow)
	swept, err := svc.SweepExpiredOffers(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, swept)
}

func TestCancelSwapOffer_Success(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{getByIDFn: func(ctx context.Context, id string) (*SwapOffer, error) {
		return createdOffer("offer-1", "u1"), nil
	}}
	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}
	escrow := &fakeEscrow{cancelSwapFn: func(ctx context.Context, swapID, canceller string) (string, error) {
		return "tx", nil
	}}

	svc := NewService(repo, nil, users, escrow)
	offer, err := svc.CancelSwapOffer(ctx, "u1", "offer-1")
	require.NoError(t, err)
	assert.Equal(t, SwapOfferStatusCancelled, offer.Status)
}

func TestAcceptSwapOffer_ConcurrencyAndCAS(t *testing.T) {
	ctx := context.Background()
	offer := createdOffer("offer-1", "u1")

	var currentStatus SwapOfferStatus = SwapOfferStatusCreated
	var statusMu sync.Mutex

	repo := &fakeRepo{
		getByIDFn: func(ctx context.Context, id string) (*SwapOffer, error) {
			statusMu.Lock()
			defer statusMu.Unlock()
			copy := *offer
			copy.Status = currentStatus
			return &copy, nil
		},
		casFn: func(ctx context.Context, id string, expectedStatus, newStatus SwapOfferStatus, transactionHash *string) (bool, error) {
			statusMu.Lock()
			defer statusMu.Unlock()
			if currentStatus != expectedStatus {
				return false, nil
			}
			currentStatus = newStatus
			return true, nil
		},
	}

	users := &fakeUserService{getByIDFn: func(ctx context.Context, id string) (*user.User, error) {
		return walletUser(id), nil
	}}

	var acceptCalls atomic.Int32
	escrow := &fakeEscrow{
		acceptSwapFn: func(ctx context.Context, swapID string, acceptor string) (string, error) {
			acceptCalls.Add(1)
			time.Sleep(20 * time.Millisecond)
			return "tx-accept", nil
		},
	}

	svc := NewService(repo, nil, users, escrow)

	var wg sync.WaitGroup
	errs := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			_, err := svc.AcceptSwapOffer(ctx, uid, "offer-1")
			if err != nil {
				errs <- err
			}
		}("user-" + string(rune('A'+i)))
	}

	wg.Wait()
	close(errs)

	assert.Equal(t, int32(1), acceptCalls.Load(), "exacty one concurrent accept should proceed")
}
