package client

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/webrtc/v4"
)

// fakePeerStateProvider reports a state that the test can change.
type fakePeerStateProvider struct {
	state atomic.Int32
}

func newFakePeer(state webrtc.PeerConnectionState) *fakePeerStateProvider {
	f := &fakePeerStateProvider{}
	f.set(state)
	return f
}

func (f *fakePeerStateProvider) set(state webrtc.PeerConnectionState) { f.state.Store(int32(state)) }

func (f *fakePeerStateProvider) ConnectionState() webrtc.PeerConnectionState {
	return webrtc.PeerConnectionState(f.state.Load())
}

// The tests run on synctest's fake clock with the real monitoring interval.

func TestMonitorConnectionStateContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go monitorConnectionState(ctx, nil, errCh, connectionMonitorInterval)

		time.Sleep(10 * time.Second)
		cancel()
		synctest.Wait() // the monitor has returned, or synctest.Test fails

		select {
		case err := <-errCh:
			t.Fatalf("error on cancel: %v", err)
		default:
		}
	})
}

// A connection that fails is reported at the next check, at most one interval later.
func TestMonitorConnectionStateFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer := newFakePeer(webrtc.PeerConnectionStateConnected)
		errCh := make(chan error, 1)
		go monitorConnectionState(t.Context(), peer, errCh, connectionMonitorInterval)

		start := time.Now()
		time.Sleep(5 * time.Second)
		peer.set(webrtc.PeerConnectionStateFailed)

		err := <-errCh
		if !strings.Contains(err.Error(), "failed") {
			t.Fatalf("error = %v", err)
		}
		if elapsed := time.Since(start); elapsed != 6*time.Second {
			t.Fatalf("failure reported after %v, want 6s (next check after 5s)", elapsed)
		}
	})
}

func TestMonitorConnectionStateConnectedThenCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go monitorConnectionState(ctx, newFakePeer(webrtc.PeerConnectionStateConnected), errCh, connectionMonitorInterval)

		time.Sleep(time.Hour)
		cancel()
		synctest.Wait()

		select {
		case err := <-errCh:
			t.Fatalf("error for a connected peer: %v", err)
		default:
		}
	})
}
