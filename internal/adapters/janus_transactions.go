package adapters

import (
	"crypto/rand"
	"math/big"
)

// generateTransaction returns a random 12-character alphanumeric string.
// Janus requires a transaction ID on every message, even fire-and-forget ones.
func (a *JanusAdapter) generateTransaction() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 12)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

// registerTransaction creates a response channel for the given transaction ID
// and stores it in the map so handleMessages can deliver the reply.
func (a *JanusAdapter) registerTransaction(transaction string) chan JanusMessage {
	respCh := make(chan JanusMessage, 1)
	a.transMu.Lock()
	a.transactions[transaction] = respCh
	a.transMu.Unlock()
	return respCh
}

// popTransaction atomically retrieves and removes the channel for a transaction.
// Returns (nil, false) if no handler was registered (fire-and-forget messages).
func (a *JanusAdapter) popTransaction(transaction string) (chan JanusMessage, bool) {
	a.transMu.Lock()
	defer a.transMu.Unlock()

	respCh, exists := a.transactions[transaction]
	if exists {
		delete(a.transactions, transaction)
	}
	return respCh, exists
}

// removeTransaction discards a pending transaction without delivering a response.
// Typically called via defer after a request completes or times out.
func (a *JanusAdapter) removeTransaction(transaction string) {
	a.transMu.Lock()
	delete(a.transactions, transaction)
	a.transMu.Unlock()
}

// clearTransactions discards all pending transactions, used during Close.
func (a *JanusAdapter) clearTransactions() {
	a.transMu.Lock()
	clear(a.transactions)
	a.transMu.Unlock()
}
