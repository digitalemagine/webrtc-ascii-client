package adapters

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A peer that stops answering pings must be detected: sending a ping is not
// proof that the peer is alive, so it must not extend the read deadline.
func TestReadTimesOutWhenPeerStopsAnsweringPings(t *testing.T) {
	t.Parallel()

	// The server never reads, so it never answers pings with pongs.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	b := newBaseAdapter("test-adapter")
	b.readTimeout = 200 * time.Millisecond
	b.keepaliveInterval = 20 * time.Millisecond
	t.Cleanup(func() { _ = b.close() })

	if err := b.connectWebSocket(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil); err != nil {
		t.Fatalf("connectWebSocket: %v", err)
	}
	b.startKeepalive(nil)

	readErr := make(chan error, 1)
	go func() {
		_, _, err := b.wsConn.ReadMessage()
		readErr <- err
	}()

	select {
	case err := <-readErr:
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("read returned %v, want a timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not time out: pings keep extending the deadline")
	}
}

// close must send the close frame even when the write deadline left by the
// last message has passed, as it has after ten idle seconds.
func TestCloseSendsCloseFrameAfterIdle(t *testing.T) {
	t.Parallel()

	readErr := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		readErr <- err
	}))
	t.Cleanup(srv.Close)

	b := newBaseAdapter("test-adapter")
	if err := b.connectWebSocket(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil); err != nil {
		t.Fatalf("connectWebSocket: %v", err)
	}
	if err := b.wsConn.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	_ = b.close()

	select {
	case err := <-readErr:
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Fatalf("server read ended with %v, want a normal closure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server saw no close")
	}
}
