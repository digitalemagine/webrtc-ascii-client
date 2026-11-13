package demo

import (
	"image"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	sizes [][2]int
	w, h  int
}

func (r *recorder) Frame(img *image.RGBA) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sizes = append(r.sizes, [2]int{img.Bounds().Dx(), img.Bounds().Dy()})
}

func (r *recorder) TargetSize() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.w, r.h
}

// The demo sends 5 frames a second at the output's current size.
func TestRunFollowsTargetSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &recorder{w: 80, h: 24}
		go Run(t.Context(), out)

		time.Sleep(time.Second - time.Millisecond)
		synctest.Wait()
		out.mu.Lock()
		out.w, out.h = 40, 12
		out.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()

		out.mu.Lock()
		defer out.mu.Unlock()
		if len(out.sizes) != 10 {
			t.Fatalf("%d frames in 2 s, want 10", len(out.sizes))
		}
		if out.sizes[0] != [2]int{80, 24} || out.sizes[9] != [2]int{40, 12} {
			t.Fatalf("sizes %v", out.sizes)
		}
	})
}

func TestPatternMoves(t *testing.T) {
	t.Parallel()
	a, b := Pattern(32, 16, 0), Pattern(32, 16, 1)
	if string(a.Pix) == string(b.Pix) {
		t.Fatal("consecutive frames are identical")
	}
}
