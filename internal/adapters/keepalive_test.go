package adapters

import (
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Keepalive pings every keepaliveInterval (30 s) and stops on stopKeepalive.
// synctest runs this on a fake clock, so it takes no real time.
func TestKeepaliveInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newBaseAdapter("test-adapter")
		var pings atomic.Int32
		b.startKeepaliveWith(func() error { pings.Add(1); return nil })

		time.Sleep(29 * time.Second)
		synctest.Wait()
		if n := pings.Load(); n != 0 {
			t.Fatalf("%d pings after 29 s, want 0", n)
		}

		time.Sleep(62 * time.Second) // t = 91 s
		synctest.Wait()
		if n := pings.Load(); n != 3 {
			t.Fatalf("%d pings after 91 s, want 3", n)
		}

		b.stopKeepalive()
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := pings.Load(); n != 3 {
			t.Fatalf("%d pings after stopKeepalive, want 3", n)
		}
	}) // synctest.Test fails if the keepalive goroutine is still running
}

// A failed ping ends the keepalive loop instead of retrying forever.
func TestKeepaliveStopsAfterFailedPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newBaseAdapter("test-adapter")
		var pings atomic.Int32
		b.startKeepaliveWith(func() error { pings.Add(1); return errors.New("broken pipe") })

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if n := pings.Load(); n != 1 {
			t.Fatalf("%d pings, want 1", n)
		}
	})
}
