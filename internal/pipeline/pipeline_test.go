package pipeline

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
	"github.com/digitalemagine/webrtc-ascii-client/internal/display"
)

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

func start(t *testing.T, cfg encode.Config, opts Options) (*Pipeline, *syncBuffer, context.CancelFunc) {
	t.Helper()
	var out syncBuffer
	d := display.New(&out, display.Options{Title: "test"})
	p, err := New(cfg, d, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = p.Run(ctx) }()
	return p, &out, cancel
}

// A frame of the target size is encoded and shown; after Reconfigure the
// target size follows, and a frame still in the old size is skipped.
func TestPipelineEncodesAndReconfigures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := encode.Config{Mode: encode.Classic, Width: 8, Height: 2}
		p, out, cancel := start(t, cfg, Options{})
		defer cancel()

		if w, h := p.TargetSize(); w != 8 || h != 2 {
			t.Fatalf("TargetSize = %dx%d, want 8x2", w, h)
		}
		p.Frame(encode.Pattern(8, 2))
		synctest.Wait()
		if !strings.Contains(out.String(), "test | classic | FPS") {
			t.Fatalf("frame not shown: %q", out.String())
		}

		if err := p.Reconfigure(encode.Config{Mode: encode.Braille, Width: 4, Height: 1}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if w, h := p.TargetSize(); w != 8 || h != 4 {
			t.Fatalf("TargetSize after reconfigure = %dx%d, want 8x4", w, h)
		}
		before := strings.Count(out.String(), "| braille |")
		p.Frame(encode.Pattern(8, 2)) // old size: skipped
		synctest.Wait()
		if strings.Count(out.String(), "| braille |") != before {
			t.Fatal("a frame of the old size was shown")
		}
		p.Frame(encode.Pattern(8, 4))
		synctest.Wait()
		if !strings.Contains(out.String(), "\033[2J") || !strings.Contains(out.String(), "| braille |") {
			t.Fatalf("new renderer not shown with a clear: %q", out.String())
		}
	})
}

func TestPipelineRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	d := display.New(&bytes.Buffer{}, display.Options{})
	if _, err := New(encode.Config{Mode: "nope", Width: 1, Height: 1}, d, Options{}); err == nil {
		t.Fatal("New accepted an invalid config")
	}
	p, err := New(encode.Config{Mode: encode.Classic, Width: 1, Height: 1}, d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Reconfigure(encode.Config{Mode: encode.Enhanced, Width: 1, Height: 1, Quality: 9}); err == nil {
		t.Fatal("Reconfigure accepted an invalid config")
	}
}

// The controller lowers quality when encoding exceeds the frame budget and
// raises it back, never above the starting quality, with a cooldown.
func TestQualityController(t *testing.T) {
	t.Parallel()

	cfg := encode.Config{Mode: encode.Enhanced, Quality: 3, Width: 1, Height: 1}
	c := newQualityController(cfg, Options{Adaptive: true, TargetFPS: 20}) // 50 ms budget
	now := time.Unix(0, 0)
	feed := func(d time.Duration, n int) (changes []int) {
		for range n {
			now = now.Add(100 * time.Millisecond)
			if q, ok := c.observe(d, now); ok {
				changes = append(changes, q)
			}
		}
		return changes
	}
	if got := feed(80*time.Millisecond, 70); len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("slow encoding: quality changes %v, want [2 1]", got)
	}
	if got := feed(10*time.Millisecond, 100); len(got) != 2 || got[1] != 3 {
		t.Fatalf("fast encoding: quality changes %v, want back up to 3", got)
	}

	off := newQualityController(encode.Config{Mode: encode.Classic, Width: 1, Height: 1}, Options{Adaptive: true, TargetFPS: 20})
	if _, ok := off.observe(time.Second, now.Add(time.Hour)); ok {
		t.Fatal("controller active for a renderer without quality")
	}
}
