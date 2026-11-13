// Package demo generates a moving test pattern, for trying the renderers
// without a server.
package demo

import (
	"context"
	"image"
	"math"
	"time"
)

// Output receives frames; pipeline.Pipeline implements it.
type Output interface {
	Frame(img *image.RGBA)
	TargetSize() (width, height int)
}

// FrameInterval is the demo's frame period (5 FPS).
const FrameInterval = 200 * time.Millisecond

// Run sends a frame to out every FrameInterval, generated at out's current
// target size, until ctx is canceled.
func Run(ctx context.Context, out Output) {
	ticker := time.NewTicker(FrameInterval)
	defer ticker.Stop()
	for frame := 0; ; frame++ {
		if w, h := out.TargetSize(); w > 0 && h > 0 {
			out.Frame(Pattern(w, h, frame))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pattern draws frame number n of the demo: three interfering waves, in
// gray, with coordinates normalized to a 160×120 reference so the pattern
// looks the same at any size.
func Pattern(w, h, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	t := float64(n) * 0.1
	sx, sy := 160/float64(w), 120/float64(h)
	for y := range h {
		fy := float64(y) * sy
		for x := range w {
			fx := float64(x) * sx
			v := math.Sin(fx*0.1+t) + math.Sin(fy*0.1+t*1.5) + math.Sin(math.Sqrt(fx*fx+fy*fy)*0.05+t*2)
			g := uint8(min(max((v+3)*42.5, 0), 255))
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = g, g, g, 255
		}
	}
	return img
}
