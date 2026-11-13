package encode

import (
	"image"
	"math"
	"unicode/utf8"
)

// classicEncoder draws one character per pixel from a 10-step brightness ramp.
type classicEncoder struct{ cols, rows int }

const classicRamp = " .:-=+*#%@"

func (e *classicEncoder) Encode(dst []byte, img *image.RGBA) []byte {
	if !hasSize(img, e.cols, e.rows) {
		return dst
	}
	for y := range e.rows {
		for x := range e.cols {
			r, g, b := pixel(img, x, y)
			// 8-bit luma scaled by 257/256, as with 16-bit samples: the
			// golden files depend on this rounding.
			lum := (299*int(r) + 587*int(g) + 114*int(b)) * 257 / (1000 * 256)
			dst = append(dst, classicRamp[lum*(len(classicRamp)-1)/255])
		}
		dst = append(dst, '\n')
	}
	return dst
}

// enhancedEncoder averages quality×quality pixels per cell in linear light,
// maps luminance to a 70-character ramp with gamma correction and
// Floyd–Steinberg error diffusion, and optionally colors each cell.
type enhancedEncoder struct {
	cols, rows, quality int
	color               bool

	lum, err []float64 // scratch, cols*rows
	colors   []rgb     // scratch, cols*rows
}

type rgb struct{ r, g, b uint8 }

var enhancedRamp = []rune(" .`'\"^,:;~-_=+!?*|/\\()[]{}1ljItfvcxzrnuJLFhkbdpqwm#KMWXVYZ0OQC&8%B@$")

func newEnhancedEncoder(c Config) *enhancedEncoder {
	n := c.Width * c.Height
	return &enhancedEncoder{
		cols: c.Width, rows: c.Height, quality: c.Quality, color: c.Color,
		lum: make([]float64, n), err: make([]float64, n), colors: make([]rgb, n),
	}
}

func (e *enhancedEncoder) Encode(dst []byte, img *image.RGBA) []byte {
	q := e.quality
	if !hasSize(img, e.cols*q, e.rows*q) {
		return dst
	}
	samples := float64(q * q)
	for y := range e.rows {
		for x := range e.cols {
			var lumSum, rSum, gSum, bSum float64
			for sy := range q {
				for sx := range q {
					r8, g8, b8 := pixel(img, x*q+sx, y*q+sy)
					lr, lg, lb := srgbToLinearLUT[r8], srgbToLinearLUT[g8], srgbToLinearLUT[b8]
					lumSum += float64(lumR*lr) + float64(lumG*lg) + float64(lumB*lb)
					rSum += lr
					gSum += lg
					bSum += lb
				}
			}
			i := y*e.cols + x
			e.lum[i] = lumSum / samples
			if e.color {
				e.colors[i] = rgb{
					floatToUint8(linearToSrgb(rSum / samples)),
					floatToUint8(linearToSrgb(gSum / samples)),
					floatToUint8(linearToSrgb(bSum / samples)),
				}
			}
		}
	}

	clear(e.err)
	rampMax := float64(len(enhancedRamp) - 1)
	for y := range e.rows {
		var last rgb
		haveColor := false
		for x := range e.cols {
			i := y*e.cols + x
			perceptual := clamp01(gammaCorrect(clamp01(e.lum[i])) + e.err[i])
			idx := min(max(int(math.Round(perceptual*rampMax)), 0), len(enhancedRamp)-1)
			diff := perceptual - float64(idx)/rampMax
			if x+1 < e.cols {
				e.err[i+1] += float64(diff * 7.0 / 16.0)
			}
			if y+1 < e.rows {
				if x > 0 {
					e.err[i+e.cols-1] += float64(diff * 3.0 / 16.0)
				}
				e.err[i+e.cols] += float64(diff * 5.0 / 16.0)
				if x+1 < e.cols {
					e.err[i+e.cols+1] += float64(diff * 1.0 / 16.0)
				}
			}

			if e.color {
				if c := e.colors[i]; !haveColor || c != last {
					dst = appendColor(dst, fgLayer, c.r, c.g, c.b)
					last, haveColor = c, true
				}
			}
			dst = utf8.AppendRune(dst, enhancedRamp[idx])
		}
		if e.color && haveColor {
			dst = append(dst, sgrReset...)
		}
		dst = append(dst, '\n')
	}
	return dst
}

// blocksEncoder draws two pixels per cell with the upper half block: the
// top pixel as foreground, the bottom one as background.
type blocksEncoder struct {
	cols, rows int
	color      bool
}

func (e *blocksEncoder) Encode(dst []byte, img *image.RGBA) []byte {
	if !hasSize(img, e.cols, e.rows*2) {
		return dst
	}
	for y := range e.rows {
		for x := range e.cols {
			tr, tg, tb := pixel(img, x, 2*y)
			br, bg, bb := pixel(img, x, 2*y+1)
			if !e.color {
				// Gray levels, still written as 24-bit colors.
				t, b := luma8(tr, tg, tb), luma8(br, bg, bb)
				tr, tg, tb, br, bg, bb = t, t, t, b, b, b
			}
			if tr == br && tg == bg && tb == bb {
				dst = appendColor(dst, bgLayer, tr, tg, tb)
				dst = append(dst, ' ')
			} else {
				dst = appendColor(dst, fgLayer, tr, tg, tb)
				dst = appendColor(dst, bgLayer, br, bg, bb)
				dst = append(dst, "▀"...)
			}
		}
		dst = append(dst, sgrReset+"\n"...)
	}
	return dst
}

// brailleEncoder draws 2×4 pixels per cell as braille dots. Without color
// a dot is on when its pixel's luma is above 128. In color each cell gets
// two colors: the eight pixels, ordered by luma, are split where the color
// error is smallest; the lighter part becomes the dots (foreground), the
// darker part the background.
type brailleEncoder struct {
	cols, rows int
	color      bool
}

// brailleDots maps (row, column) within a cell to the Unicode dot bit.
var brailleDots = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

func (e *brailleEncoder) Encode(dst []byte, img *image.RGBA) []byte {
	if !hasSize(img, e.cols*2, e.rows*4) {
		return dst
	}
	const threshold = 128
	var pixels [8]rgb
	var bits [8]uint8
	for y := range e.rows {
		var lastFg, lastBg rgb
		haveFg, haveBg := false, false
		for x := range e.cols {
			for dy := range 4 {
				for dx := range 2 {
					r, g, b := pixel(img, 2*x+dx, 4*y+dy)
					pixels[dy*2+dx] = rgb{r, g, b}
					bits[dy*2+dx] = brailleDots[dy][dx]
				}
			}
			if !e.color {
				var code uint8
				for i, p := range pixels {
					if luma8(p.r, p.g, p.b) > threshold {
						code |= bits[i]
					}
				}
				dst = utf8.AppendRune(dst, rune(0x2800+uint16(code)))
				continue
			}
			code, fg, bg := splitBraille(pixels, bits)
			// Write only the colors that change; a blank cell shows no
			// foreground, so it keeps the previous one.
			if code != 0 && (!haveFg || fg != lastFg) {
				dst = appendColor(dst, fgLayer, fg.r, fg.g, fg.b)
				lastFg, haveFg = fg, true
			}
			if !haveBg || bg != lastBg {
				dst = appendColor(dst, bgLayer, bg.r, bg.g, bg.b)
				lastBg, haveBg = bg, true
			}
			dst = utf8.AppendRune(dst, rune(0x2800+uint16(code)))
		}
		if haveFg || haveBg {
			dst = append(dst, sgrReset...)
		}
		dst = append(dst, '\n')
	}
	return dst
}

// splitBraille chooses the two colors of a braille cell. It orders the
// pixels by luma and tries every split into a darker and a lighter part,
// keeping the one with the smallest color error; the lighter part's dots
// are set in code. A cell best shown in one color has no dots.
func splitBraille(pixels [8]rgb, bits [8]uint8) (code uint8, fg, bg rgb) {
	// Insertion sort by luma; stable, so ties keep the pixel order.
	var order [8]int
	var lumas [8]uint8
	for i, p := range pixels {
		l := luma8(p.r, p.g, p.b)
		j := i
		for ; j > 0 && lumas[j-1] > l; j-- {
			order[j], lumas[j] = order[j-1], lumas[j-1]
		}
		order[j], lumas[j] = i, l
	}
	mean := func(from, to int) rgb {
		var r, g, b uint32
		for _, i := range order[from:to] {
			r += uint32(pixels[i].r)
			g += uint32(pixels[i].g)
			b += uint32(pixels[i].b)
		}
		n := uint32(to - from)
		return rgb{uint8((r + n/2) / n), uint8((g + n/2) / n), uint8((b + n/2) / n)}
	}
	// split k: order[:k] is the background, order[k:] the dots; k = 8 is
	// one color, no dots.
	bestK, bestErr := 8, uint32(0)
	bg = mean(0, 8)
	for _, i := range order {
		bestErr += manhattanDistance(pixels[i], bg)
	}
	for k := 1; k < 8 && bestErr > 0; k++ {
		lo, hi := mean(0, k), mean(k, 8)
		var err uint32
		for j, i := range order {
			if j < k {
				err += manhattanDistance(pixels[i], lo)
			} else {
				err += manhattanDistance(pixels[i], hi)
			}
		}
		if err < bestErr {
			bestK, bestErr, bg, fg = k, err, lo, hi
		}
	}
	for _, i := range order[bestK:] {
		code |= bits[i]
	}
	if bestK == 8 {
		fg = rgb{}
	}
	return code, fg, bg
}
