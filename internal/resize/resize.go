// Package resize scales RGBA images by area averaging: each destination pixel
// is the mean of the source pixels it covers (at least one), which is good
// for shrinking and degrades to nearest-neighbor when enlarging. Integer
// arithmetic keeps results identical on every platform.
package resize

import "image"

// Area returns src scaled to w×h. If src already has that size, it is
// returned unchanged.
func Area(src *image.RGBA, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == w && sh == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if sw == 0 || sh == 0 {
		return dst
	}
	// Source ranges per destination column and row, computed once.
	xs, ys := spans(sw, w), spans(sh, h)
	for y := range h {
		y0, y1 := ys[y], max(ys[y+1], ys[y]+1)
		row := dst.Pix[y*dst.Stride:]
		for x := range w {
			x0, x1 := xs[x], max(xs[x+1], xs[x]+1)
			var r, g, b, a, n int
			for sy := y0; sy < y1; sy++ {
				p := src.Pix[(sy+sb.Min.Y-src.Rect.Min.Y)*src.Stride+(x0+sb.Min.X-src.Rect.Min.X)*4:]
				for i := 0; i < (x1-x0)*4; i += 4 {
					r += int(p[i])
					g += int(p[i+1])
					b += int(p[i+2])
					a += int(p[i+3])
				}
				n += x1 - x0
			}
			row[4*x] = uint8((r + n/2) / n)
			row[4*x+1] = uint8((g + n/2) / n)
			row[4*x+2] = uint8((b + n/2) / n)
			row[4*x+3] = uint8((a + n/2) / n)
		}
	}
	return dst
}

// spans returns the n+1 boundaries that split size source pixels into n
// destination pixels.
func spans(size, n int) []int {
	s := make([]int, n+1)
	for i := range s {
		s[i] = i * size / n
	}
	return s
}
