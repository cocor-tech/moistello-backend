package mobilemoney_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/mobilemoney"
	"github.com/moistello/backend/internal/domain/mobilemoney/mocks"
)

func newFakeProvider(name, currency string) *mocks.Provider {
	return &mocks.Provider{NameVal: name, CurrencyVal: currency}
}

func TestService_InitiateOnramp_PersistsAfterProviderAccepts(t *testing.T) {
	repo := new(mocks.Repository)
	provider := newFakeProvider("mpesa", "KES")
	registry := mobilemoney.NewRegistry()
	registry.Register(provider)
	svc := mobilemoney.NewService(repo, registry)
	ctx := context.Background()

	req := mobilemoney.OnrampRequest{Amount: 1000, Currency: "KES", PhoneNumber: "254712345678", DestinationAddress: "GABC"}

	repo.On("GetByIdempotencyKey", ctx, "user-1", "key-1").Return(nil, nil)
	provider.On("InitiateOnramp", ctx, req).Return(&mobilemoney.OnrampResult{ProviderRef: "ref-1", Status: mobilemoney.StatusPending}, nil)
	repo.On("Create", ctx, mock.MatchedBy(func(t *mobilemoney.Transaction) bool {
		return t.UserID == "user-1" && t.Provider == "mpesa" && t.ProviderRef == "ref-1" &&
			t.Direction == mobilemoney.DirectionOnramp && t.Status == mobilemoney.StatusPending
	})).Return(nil)

	txn, err := svc.InitiateOnramp(ctx, "user-1", req, "key-1")
	require.NoError(t, err)
	assert.Equal(t, "ref-1", txn.ProviderRef)
	repo.AssertExpectations(t)
	provider.AssertExpectations(t)
}

func TestService_InitiateOnramp_IdempotentReplay(t *testing.T) {
	repo := new(mocks.Repository)
	provider := newFakeProvider("mpesa", "KES")
	registry := mobilemoney.NewRegistry()
	registry.Register(provider)
	svc := mobilemoney.NewService(repo, registry)
	ctx := context.Background()

	existing := &mobilemoney.Transaction{ID: "txn-1", ProviderRef: "ref-1", Status: mobilemoney.StatusCompleted}
	repo.On("GetByIdempotencyKey", ctx, "user-1", "key-1").Return(existing, nil)

	req := mobilemoney.OnrampRequest{Amount: 1000, Currency: "KES", PhoneNumber: "254712345678"}
	txn, err := svc.InitiateOnramp(ctx, "user-1", req, "key-1")
	require.NoError(t, err)
	assert.Equal(t, existing, txn)
	provider.AssertNotCalled(t, "InitiateOnramp", mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestService_InitiateOnramp_RequiresIdempotencyKey(t *testing.T) {
	repo := new(mocks.Repository)
	registry := mobilemoney.NewRegistry()
	svc := mobilemoney.NewService(repo, registry)

	_, err := svc.InitiateOnramp(context.Background(), "user-1", mobilemoney.OnrampRequest{Currency: "KES"}, "")
	assert.Error(t, err)
	repo.AssertNotCalled(t, "GetByIdempotencyKey", mock.Anything, mock.Anything, mock.Anything)
}

func TestService_InitiateOnramp_UnsupportedCurrency(t *testing.T) {
	repo := new(mocks.Repository)
	registry := mobilemoney.NewRegistry()
	svc := mobilemoney.NewService(repo, registry)
	ctx := context.Background()

	repo.On("GetByIdempotencyKey", ctx, "user-1", "key-1").Return(nil, nil)

	_, err := svc.InitiateOnramp(ctx, "user-1", mobilemoney.OnrampRequest{Currency: "XYZ"}, "key-1")
	assert.Error(t, err)
}

func TestService_InitiateOfframp_PersistsAfterProviderAccepts(t *testing.T) {
	repo := new(mocks.Repository)
	provider := newFakeProvider("mpesa", "KES")
	registry := mobilemoney.NewRegistry()
	registry.Register(provider)
	svc := mobilemoney.NewService(repo, registry)
	ctx := context.Background()

	req := mobilemoney.OfframpRequest{Amount: 500, Currency: "KES", PhoneNumber: "254712345678", AccountName: "Jane"}

	repo.On("GetByIdempotencyKey", ctx, "user-1", "key-2").Return(nil, nil)
	provider.On("InitiateOfframp", ctx, req).Return(&mobilemoney.OfframpResult{ProviderRef: "ref-2", Status: mobilemoney.StatusPending}, nil)
	repo.On("Create", ctx, mock.MatchedBy(func(t *mobilemoney.Transaction) bool {
		return t.Direction == mobilemoney.DirectionOfframp && t.ProviderRef == "ref-2"
	})).Return(nil)

	txn, err := svc.InitiateOfframp(ctx, "user-1", req, "key-2")
	require.NoError(t, err)
	assert.Equal(t, "ref-2", txn.ProviderRef)
	repo.AssertExpectations(t)
	provider.AssertExpectations(t)
}

func TestService_ListsProvidersAndCurrencies(t *testing.T) {
	ctx := context.Background()
	registry := mobilemoney.NewRegistry()
	registry.Register(newFakeProvider("mpesa", "KES"))
	registry.Register(newFakeProvider("mtn", "UGX"))
	svc := mobilemoney.NewService(new(mocks.Repository), registry)

	assert.ElementsMatch(t, []mobilemoney.ProviderInfo{
		{Name: "mpesa", Currency: "KES"},
		{Name: "mtn", Currency: "UGX"},
	}, svc.ListProviders(ctx))
	assert.ElementsMatch(t, []string{"KES", "UGX"}, svc.GetSupportedCurrencies(ctx))

	emptyService := mobilemoney.NewService(new(mocks.Repository), nil)
	assert.Empty(t, emptyService.ListProviders(ctx))
	assert.Empty(t, emptyService.GetSupportedCurrencies(ctx))
}

func TestService_GetTransaction(t *testing.T) {
	ctx := context.Background()
	repo := new(mocks.Repository)
	svc := mobilemoney.NewService(repo, nil)
	want := &mobilemoney.Transaction{ID: "txn-1", UserID: "user-1"}
	repo.On("GetByID", ctx, "txn-1").Return(want, nil).Once()
	repo.On("GetByID", ctx, "missing").Return((*mobilemoney.Transaction)(nil), assert.AnError).Once()

	got, err := svc.GetTransaction(ctx, "txn-1")
	require.NoError(t, err)
	assert.Same(t, want, got)

	got, err = svc.GetTransaction(ctx, "missing")
	assert.Nil(t, got)
	assert.ErrorIs(t, err, assert.AnError)
	repo.AssertExpectations(t)
}

func TestService_Reconcile_UpdatesSettledTransactions(t *testing.T) {
	repo := new(mocks.Repository)
	provider := newFakeProvider("mpesa", "KES")
	registry := mobilemoney.NewRegistry()
	registry.Register(provider)
	svc := mobilemoney.NewService(repo, registry)
	ctx := context.Background()

	pending := []mobilemoney.Transaction{
		{ID: "txn-1", Currency: "KES", ProviderRef: "ref-1"},
		{ID: "txn-2", Currency: "KES", ProviderRef: "ref-2"},
		{ID: "txn-3", Currency: "KES", ProviderRef: "ref-3"},
	}
	repo.On("ListPending", ctx, 30).Return(pending, nil)

	provider.On("GetStatus", ctx, "ref-1").Return(&mobilemoney.StatusResult{ProviderRef: "ref-1", Status: mobilemoney.StatusCompleted}, nil)
	provider.On("GetStatus", ctx, "ref-2").Return(&mobilemoney.StatusResult{ProviderRef: "ref-2", Status: mobilemoney.StatusPending}, nil)
	provider.On("GetStatus", ctx, "ref-3").Return(&mobilemoney.StatusResult{ProviderRef: "ref-3", Status: mobilemoney.StatusFailed, FailureReason: "insufficient funds"}, nil)

	repo.On("UpdateStatus", ctx, "txn-1", mobilemoney.StatusCompleted, (*string)(nil)).Return(nil)
	repo.On("UpdateStatus", ctx, "txn-3", mobilemoney.StatusFailed, mock.MatchedBy(func(reason *string) bool {
		return reason != nil && *reason == "insufficient funds"
	})).Return(nil)

	count, err := svc.Reconcile(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "only the two settled transactions should be updated; the still-pending one is left alone")
	repo.AssertExpectations(t)
	repo.AssertNotCalled(t, "UpdateStatus", ctx, "txn-2", mock.Anything, mock.Anything)
}
