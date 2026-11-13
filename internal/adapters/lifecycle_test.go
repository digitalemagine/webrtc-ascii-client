package adapters

import "testing"

func TestBaseAdapterCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	b := newBaseAdapter("test-adapter")

	if err := b.close(); err != nil {
		t.Fatalf("first close returned error: %v", err)
	}
	if err := b.close(); err != nil {
		t.Fatalf("second close returned error: %v", err)
	}
	if !b.isCloseRequested() {
		t.Fatal("closeRequested should be true after close")
	}
}

func TestBaseAdapterStopKeepaliveIsIdempotent(t *testing.T) {
	t.Parallel()

	b := newBaseAdapter("test-adapter")
	b.startKeepalive(nil)

	b.stopKeepalive()
	b.stopKeepalive()

	select {
	case <-b.keepaliveDone:
		// closed as expected
	default:
		t.Fatal("keepaliveDone should be closed")
	}
}

func TestConnectWebSocketRejectsReuseAfterClose(t *testing.T) {
	t.Parallel()

	b := newBaseAdapter("test-adapter")
	if err := b.close(); err != nil {
		t.Fatalf("close returned error: %v", err)
	}

	err := b.connectWebSocket(t.Context(), "ws://127.0.0.1:0", nil)
	if err == nil {
		t.Fatal("expected error when connecting a closed adapter, got nil")
	}
}

func TestJanusTransactionHelpers(t *testing.T) {
	t.Parallel()

	a := NewJanusAdapter("ws://127.0.0.1:1", "1")
	txID := "tx-1"

	respCh := a.registerTransaction(txID)
	if respCh == nil {
		t.Fatal("registerTransaction returned nil channel")
	}

	popped, ok := a.popTransaction(txID)
	if !ok {
		t.Fatal("popTransaction did not find existing transaction")
	}
	if popped != respCh {
		t.Fatal("popTransaction returned unexpected channel")
	}
	if _, exists := a.transactions[txID]; exists {
		t.Fatal("transaction should be removed after pop")
	}

	a.registerTransaction("tx-2")
	a.registerTransaction("tx-3")
	a.removeTransaction("tx-2")
	if _, exists := a.transactions["tx-2"]; exists {
		t.Fatal("removeTransaction should remove transaction entry")
	}

	a.clearTransactions()
	if len(a.transactions) != 0 {
		t.Fatalf("expected empty transaction map after clear, got %d entries", len(a.transactions))
	}
}
