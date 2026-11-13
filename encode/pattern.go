package encode

import (
	"image"
	"image/color"
)

// Pattern returns a deterministic w×h test image: gradients with a texture
// (like compression noise) and flat white, black and red rectangles (like
// text and objects), for examples, tests and benchmarks.
func Pattern(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			noise := (x * y) % 64
			c := func(v int) uint8 { return uint8(min(max(v+noise-32, 0), 255)) }
			img.SetRGBA(x, y, color.RGBA{c(x * 255 / w), c(y * 255 / h), c((x + y) * 255 / (w + h)), 255})
		}
	}
	fill := func(x0, y0, x1, y1 int, c color.RGBA) { // in percent of the size
		for y := y0 * h / 100; y < y1*h/100; y++ {
			for x := x0 * w / 100; x < x1*w/100; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(10, 10, 30, 20, color.RGBA{255, 255, 255, 255})
	fill(40, 40, 60, 50, color.RGBA{0, 0, 0, 255})
	fill(60, 20, 80, 40, color.RGBA{255, 0, 0, 255})
	return img
}
