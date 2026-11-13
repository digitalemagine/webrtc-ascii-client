package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters/janustest"
)

func newTestPeerConnection(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

// runSession starts s.Run and returns a function that cancels it and
// returns its error.
func runSession(t *testing.T, s Session, pc *webrtc.PeerConnection) (stop func() error, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- s.Run(ctx, pc) }()
	t.Cleanup(cancel)
	return func() error {
		cancel()
		select {
		case err := <-errc:
			return err
		case <-time.After(3 * time.Second):
			t.Fatal("Run did not return after cancel")
			return nil
		}
	}, errc
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Against the mock Janus (which, like Janus, requires the janus-protocol
// subprotocol, J0): the session watches the stream, answers the offer with
// "start", trickles its candidates ending with "completed", and destroys
// the session when canceled.
func TestJanusRun(t *testing.T) {
	t.Parallel()

	srv := janustest.NewServer(t)
	stop, done := runSession(t, NewJanusAdapter(srv.URL, "1"), newTestPeerConnection(t))

	select {
	case <-srv.Answered():
	case err := <-done:
		t.Fatalf("Run returned before answering: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no answer applied by the server")
	}
	if n := srv.Rejected(); n != 0 {
		t.Fatalf("server rejected %d connection(s) for a missing subprotocol", n)
	}
	waitFor(t, "end-of-candidates trickle", func() bool {
		return slices.ContainsFunc(srv.Messages(), func(m map[string]any) bool {
			c, _ := m["candidate"].(map[string]any)
			return m["janus"] == "trickle" && c["completed"] == true
		})
	})

	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	waitFor(t, "destroy", func() bool { return slices.Contains(srv.Received(), "destroy") })
}

// Janus reports streaming plugin errors in plugindata of the event that
// follows the ack; Run must return that error at once, not wait forever.
func TestJanusWatchUnknownStreamReturnsError(t *testing.T) {
	t.Parallel()

	srv := janustest.NewServer(t)
	_, done := runSession(t, NewJanusAdapter(srv.URL, "999"), newTestPeerConnection(t))

	select {
	case err := <-done:
		for _, want := range []string{"No such mountpoint/stream 999", "455"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("error %v does not contain %q", err, want)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not fail; the error reply arrives immediately")
	}
}

// The keepalive goroutine reads the session ID that Run writes. Run with
// -race.
func TestJanusKeepaliveDuringRun(t *testing.T) {
	t.Parallel()

	srv := janustest.NewServer(t)
	a := NewJanusAdapter(srv.URL, "1")
	a.keepaliveInterval = time.Millisecond
	stop, _ := runSession(t, a, newTestPeerConnection(t))

	waitFor(t, "keepalives", func() bool {
		n := 0
		for _, m := range srv.Messages() {
			if m["janus"] == "keepalive" {
				if sid, _ := m["session_id"].(float64); int64(sid) != janustest.SessionID {
					t.Errorf("keepalive with session_id %v", m["session_id"])
				}
				n++
			}
		}
		return n >= 3
	})
	_ = stop()
}

// A connection that drops during setup is reported as such, right away,
// rather than as a timeout waiting for the reply.
func TestJanusConnectionLostDuringSetup(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{Subprotocols: []string{janusSubprotocol}}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_, _, _ = conn.ReadMessage() // the create request
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)

	a := NewJanusAdapter("ws"+strings.TrimPrefix(srv.URL, "http"), "1")
	done := make(chan error, 1)
	go func() { done <- a.Run(t.Context(), newTestPeerConnection(t)) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "signaling connection lost") {
			t.Fatalf("Run returned %v, want a lost connection", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when the connection dropped")
	}
}
