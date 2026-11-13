package display

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the display goroutine and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func size(cols, rows int) func() (int, int, bool) {
	return func() (int, int, bool) { return cols, rows, true }
}

// A frame is drawn from the corner inside a synchronized update, followed by
// the status line, without clearing the screen when it fits.
func TestDisplayDrawsFrameAndStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out syncBuffer
		d := New(&out, Options{Terminal: true, Size: size(100, 10), Title: "app v1"})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { _ = d.Run(ctx); close(done) }()

		d.SetConnectionStatus("ICE connected")
		d.Show(Frame{Data: []byte("abc\ndef\n"), Rows: 2, Renderer: "classic"})
		synctest.Wait()
		cancel()
		<-done

		got := out.String()
		for _, want := range []string{
			enterAltScreen,
			beginSync + home + "abc\ndef\n" + sgrReset,
			"app v1 | classic | FPS -- | Frames 1 | Dropped 0 | Warnings 0",
			"ICE connected",
			clearToEnd + endSync,
			leaveAltScreen,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("output lacks %q:\n%q", want, got)
			}
		}
		if strings.Contains(got, clearScreen) {
			t.Error("screen cleared although the frame fits")
		}
		if strings.Count(got, leaveAltScreen) != 1 {
			t.Error("terminal not restored exactly once")
		}
	})
}

// A frame that doesn't fit with the status line would scroll the screen, so
// the display clears instead of drawing over it; Clear forces it too.
func TestDisplayClearsWhenNeeded(t *testing.T) {
	t.Parallel()

	d := New(nil, Options{Size: size(40, 10)})
	for _, tt := range []struct {
		f    Frame
		want bool
	}{
		{Frame{Rows: 8}, false},
		{Frame{Rows: 9}, true},
		{Frame{Rows: 2, Clear: true}, true},
		{Frame{Rows: 0}, false}, // unknown height (Sixel)
	} {
		var buf bytes.Buffer
		d.compose(&buf, tt.f, tt.f.Clear, time.Now())
		if got := strings.Contains(buf.String(), clearScreen); got != tt.want {
			t.Errorf("rows %d clear %v: cleared = %v, want %v", tt.f.Rows, tt.f.Clear, got, tt.want)
		}
	}
}

// Warnings and status changes are redrawn even when no frames arrive, and
// each status row is exactly as wide as the terminal.
func TestDisplayRedrawsStatusWithoutFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out syncBuffer
		d := New(&out, Options{Size: size(120, 10), Title: "app"})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = d.Run(ctx) }()

		time.Sleep(time.Second)
		synctest.Wait()
		d.Warn("Janus: connection lost\x1b[2J")
		time.Sleep(time.Second)
		synctest.Wait()

		got := out.String()
		if !strings.Contains(got, "Warnings 1") || !strings.Contains(got, "Starting | Janus: connection lost") {
			t.Fatalf("warning not shown:\n%q", got)
		}
		if strings.Contains(got, "\x1b[2J") {
			t.Fatal("control characters from a log line reached the terminal")
		}
		last := got[strings.LastIndex(got, home)+len(home):]
		if line := strings.SplitN(last, "\n", 3)[1]; len([]rune(strings.TrimSuffix(line, clearToEnd+endSync))) != 120 {
			t.Fatalf("status row is not 120 columns: %q", line)
		}
	})
}

func TestDisplayHelp(t *testing.T) {
	t.Parallel()

	d := New(nil, Options{})
	d.ToggleHelp()
	if _, second := d.statusLines("", time.Now()); second != helpLine {
		t.Fatalf("second status row = %q, want the help line", second)
	}
}

// Suspend gives the terminal back while the process is stopped, then takes
// the alternate screen again and redraws from a cleared screen.
func TestDisplaySuspend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out syncBuffer
		d := New(&out, Options{Terminal: true, Size: size(100, 10), Title: "app v1"})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { _ = d.Run(ctx); close(done) }()

		d.Show(Frame{Data: []byte("abc\n"), Rows: 1})
		synctest.Wait()
		var during string
		d.Suspend(ctx, func() { during = out.String() })
		synctest.Wait()
		cancel()
		<-done

		if !strings.HasSuffix(during, leaveAltScreen) {
			t.Errorf("the alternate screen was not left before stopping: %q", during)
		}
		after := strings.TrimPrefix(out.String(), during)
		if !strings.HasPrefix(after, enterAltScreen+beginSync+clearScreen+home+"abc\n") {
			t.Errorf("after resuming: %q, want the alternate screen, a clear and the frame", after)
		}
	})
}

// A note appears in the status line without counting as a warning.
func TestDisplayNote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out syncBuffer
		d := New(&out, Options{Size: size(100, 10), Title: "app v1"})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { _ = d.Run(ctx); close(done) }()

		d.Note("needs a font")
		time.Sleep(2 * statusInterval)
		synctest.Wait()
		cancel()
		<-done

		got := out.String()
		if !strings.Contains(got, "needs a font") || !strings.Contains(got, "Warnings 0") {
			t.Errorf("output %q, want the note and no warning counted", got)
		}
	})
}
