package wallet_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ThreadSafeWallet simulates row-level locked wallet transactions (e.g. Postgres SELECT ... FOR UPDATE)
type ThreadSafeWallet struct {
	mu      sync.Mutex
	balance int64 // store balance in micro-units (cents) to test exact rounding & prevent lost updates
}

func (w *ThreadSafeWallet) Deposit(amountCents int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balance += amountCents
}

func (w *ThreadSafeWallet) Withdraw(amountCents int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.balance < amountCents {
		return false
	}
	w.balance -= amountCents
	return true
}

func (w *ThreadSafeWallet) Balance() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balance
}

func TestWalletBalance_ConcurrentTransactions_LostUpdatePrevention(t *testing.T) {
	w := &ThreadSafeWallet{balance: 100000} // $1,000.00 initial balance

	var successfulWithdrawals atomic.Int64
	var failedWithdrawals atomic.Int64
	var wg sync.WaitGroup

	numGoroutines := 50
	depositsPerRoutine := 20
	depositAmount := int64(500) // $5.00
	withdrawAmount := int64(700) // $7.00

	// Run concurrent deposits
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < depositsPerRoutine; j++ {
				w.Deposit(depositAmount)
			}
		}()
	}

	// Run concurrent withdrawals
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < depositsPerRoutine; j++ {
				if w.Withdraw(withdrawAmount) {
					successfulWithdrawals.Add(1)
				} else {
					failedWithdrawals.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	expectedBalance := int64(100000) + int64(numGoroutines*depositsPerRoutine)*depositAmount - successfulWithdrawals.Load()*withdrawAmount
	assert.Equal(t, expectedBalance, w.Balance(), "Balance must perfectly match after concurrent transactions without lost updates")
	assert.True(t, w.Balance() >= 0, "Balance must never be negative")
}
