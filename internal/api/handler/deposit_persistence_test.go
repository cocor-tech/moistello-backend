package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/deposit"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/withdrawal"
	"github.com/moistello/backend/internal/domain/yellowcard"
)

// newFakeYellowCardServer stands in for Yellow Card's API for quotes,
// receives (deposits), and sends (withdrawals).
func newFakeYellowCardServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/quotes"):
			_ = json.NewEncoder(w).Encode(yellowcard.Quote{
				QuoteID: "q-1", FromCurrency: "NGN", ToCurrency: "USDC",
				FromAmount: 50000, ToAmount: 33.33, Rate: 1500,
			})
		case r.URL.Path == "/receive":
			_ = json.NewEncoder(w).Encode(yellowcard.ReceiveResponse{
				ReceiveID:  "r-1",
				Status:     "pending",
				PaymentRef: "ignored", // handler generates its own ref
				ExpiresAt:  time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
			})
		case r.URL.Path == "/send":
			_ = json.NewEncoder(w).Encode(yellowcard.SendResponse{
				SendID: "s-1", Status: "processing", Fee: 0.25, NetAmount: 99.75,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newTestYCClient(t *testing.T, server *httptest.Server) *yellowcard.Client {
	t.Helper()
	c := yellowcard.NewClient("", "", "GPLATFORMADDR")
	c.SetBaseURL(server.URL)
	c.SetHTTPClient(server.Client())
	return c
}

func TestDepositHandler_InitiateDeposit_PersistsRecord(t *testing.T) {
	server := newFakeYellowCardServer(t)
	defer server.Close()

	deposits := newFakeDepositRepo()
	h := handler.NewDepositHandler(newTestYCClient(t, server), &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}).WithRepositories(deposits, newFakeWithdrawalRepo())

	r := setupTestDepositRouter(h)
	reqBody, _ := json.Marshal(map[string]any{"amountNgn": 50000})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallet/deposit", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	require.Len(t, deposits.byPaymentRef, 1)

	var d *deposit.Deposit
	for _, rec := range deposits.byPaymentRef {
		d = rec
	}
	require.NotNil(t, d)
	assert.Equal(t, "test-user-123", d.UserID)
	assert.Equal(t, float64(50000), d.AmountNGN)
	assert.Equal(t, "r-1", d.ReceiveID)
	assert.Equal(t, deposit.DepositStatusPending, d.Status)
	assert.Equal(t, "GABC12345", d.DestinationAddress)
	assert.NotNil(t, d.ExpiresAt)
}

func TestDepositHandler_InitiateWithdraw_PersistsRecord(t *testing.T) {
	server := newFakeYellowCardServer(t)
	defer server.Close()

	withdrawals := newFakeWithdrawalRepo()
	h := handler.NewDepositHandler(newTestYCClient(t, server), &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}).WithRepositories(newFakeDepositRepo(), withdrawals)

	r := setupTestDepositRouter(h)
	reqBody, _ := json.Marshal(map[string]any{
		"amountUsdc": 100, "bankCode": "044", "accountNumber": "0123456789", "accountName": "Jane Doe",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallet/withdraw", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, withdrawals.byPaymentRef, 1)

	var wd *withdrawal.Withdrawal
	for _, rec := range withdrawals.byPaymentRef {
		wd = rec
	}
	require.NotNil(t, wd)
	assert.Equal(t, "test-user-123", wd.UserID)
	assert.Equal(t, "100.0000000", wd.AmountUSDC.String())
	assert.Equal(t, "33.3300000", wd.EstimatedNGN.String(), "quote amount must keep its decimals")
	require.NotNil(t, wd.YellowCardTxID)
	assert.Equal(t, "s-1", *wd.YellowCardTxID)
}

// failingDepositRepo always errors on Create, simulating a database outage
// after Yellow Card has already accepted the transaction.
type failingDepositRepo struct{ *fakeDepositRepo }

func (f *failingDepositRepo) Create(ctx context.Context, d *deposit.Deposit) error {
	return errors.New("db unavailable")
}

func TestDepositHandler_InitiateDeposit_PersistenceFailure_ReturnsError(t *testing.T) {
	server := newFakeYellowCardServer(t)
	defer server.Close()

	h := handler.NewDepositHandler(newTestYCClient(t, server), &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}).WithRepositories(&failingDepositRepo{newFakeDepositRepo()}, newFakeWithdrawalRepo())

	r := setupTestDepositRouter(h)
	reqBody, _ := json.Marshal(map[string]any{"amountNgn": 50000})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallet/deposit", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// The deposit exists on Yellow Card's side but we failed to record it —
	// this must surface as an error rather than silently returning success
	// for a transaction the platform can no longer track.
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
