package encode

import (
	"image"
	"unicode/utf8"
)

// sextantEncoder draws 2×3 pixels per cell with the Unicode sextant
// characters (U+1FB00–U+1FB3B), choosing for each cell the split into
// foreground and background pixels with the smallest color error. Without
// color only the character is written.
type sextantEncoder struct {
	cols, rows int
	color      bool
}

// SextantFontNotice tells users what the sextant renderer needs, for an
// application to show once. It mentions this project's -render flag.
const SextantFontNotice = "The sextant renderer needs a font with Unicode 13 \"Symbols for Legacy Computing\" " +
	"(U+1FB00-U+1FB3B), such as Cascadia Code or GNU Unifont. If you see \ufffd or boxes, " +
	"switch font or use -render braille or -render blocks."

func (e *sextantEncoder) Encode(dst []byte, img *image.RGBA) []byte {
	if !hasSize(img, e.cols*2, e.rows*3) {
		return dst
	}
	for y := range e.rows {
		var lastFg, lastBg rgb
		haveFg, haveBg := false, false
		for x := range e.cols {
			// Cell layout: 0 1 / 2 3 / 4 5
			var pixels [6]rgb
			for i := range 6 {
				r, g, b := pixel(img, 2*x+i%2, 3*y+i/2)
				pixels[i] = rgb{r, g, b}
			}
			char, fg, bg := findBestSextant(pixels)
			// Write only the colors that change; a blank cell shows no
			// foreground, so it keeps the previous one.
			if e.color {
				if char != ' ' && (!haveFg || fg != lastFg) {
					dst = appendColor(dst, fgLayer, fg.r, fg.g, fg.b)
					lastFg, haveFg = fg, true
				}
				if !haveBg || bg != lastBg {
					dst = appendColor(dst, bgLayer, bg.r, bg.g, bg.b)
					lastBg, haveBg = bg, true
				}
			}
			dst = utf8.AppendRune(dst, char)
		}
		if haveFg || haveBg {
			dst = append(dst, sgrReset...)
		}
		dst = append(dst, '\n')
	}
	return dst
}

var sextantChars = [64]rune{
	'\u2588',     // 0: Full block (111111)
	'\U0001FB3B', // 1: Sextant-23456 (011111)
	'\U0001FB3A', // 2: Sextant-13456 (101111)
	'\U0001FB39', // 3: Sextant-3456 (001111)
	'\U0001FB38', // 4: Sextant-12456 (110111)
	'\U0001FB37', // 5: Sextant-2456 (010111)
	'\U0001FB36', // 6: Sextant-1456 (100111)
	'\U0001FB35', // 7: Sextant-456 (000111)
	'\U0001FB34', // 8: Sextant-12356 (111011)
	'\U0001FB33', // 9: Sextant-2356 (011011)
	'\U0001FB32', // 10: Sextant-1356 (101011)
	'\U0001FB31', // 11: Sextant-356 (001011)
	'\U0001FB30', // 12: Sextant-1256 (110011)
	'\U0001FB2F', // 13: Sextant-256 (010011)
	'\U0001FB2E', // 14: Sextant-156 (100011)
	'\U0001FB2D', // 15: Sextant-56 (000011)
	'\U0001FB2C', // 16: Sextant-12346 (111101)
	'\U0001FB2B', // 17: Sextant-2346 (011101)
	'\U0001FB2A', // 18: Sextant-1346 (101101)
	'\U0001FB29', // 19: Sextant-346 (001101)
	'\U0001FB28', // 20: Sextant-1246 (110101)
	'\u2590',     // 21: Right half block (010101) - Sextant-246
	'\U0001FB27', // 22: Sextant-146 (100101)
	'\U0001FB26', // 23: Sextant-46 (000101)
	'\U0001FB25', // 24: Sextant-1236 (111001)
	'\U0001FB24', // 25: Sextant-236 (011001)
	'\U0001FB23', // 26: Sextant-136 (101001)
	'\U0001FB22', // 27: Sextant-36 (001001)
	'\U0001FB21', // 28: Sextant-126 (110001)
	'\U0001FB20', // 29: Sextant-26 (010001)
	'\U0001FB1F', // 30: Sextant-16 (100001)
	'\U0001FB1E', // 31: Sextant-6 (000001)
	'\U0001FB1D', // 32: Sextant-12345 (111110)
	'\U0001FB1C', // 33: Sextant-2345 (011110)
	'\U0001FB1B', // 34: Sextant-1345 (101110)
	'\U0001FB1A', // 35: Sextant-345 (001110)
	'\U0001FB19', // 36: Sextant-1245 (110110)
	'\U0001FB18', // 37: Sextant-245 (010110)
	'\U0001FB17', // 38: Sextant-145 (100110)
	'\U0001FB16', // 39: Sextant-45 (000110)
	'\U0001FB15', // 40: Sextant-1235 (111010)
	'\U0001FB14', // 41: Sextant-235 (011010)
	'\u258C',     // 42: Left half block (101010) - Sextant-135
	'\U0001FB13', // 43: Sextant-35 (001010)
	'\U0001FB12', // 44: Sextant-125 (110010)
	'\U0001FB11', // 45: Sextant-25 (010010)
	'\U0001FB10', // 46: Sextant-15 (100010)
	'\U0001FB0F', // 47: Sextant-5 (000010)
	'\U0001FB0E', // 48: Sextant-1234 (111100)
	'\U0001FB0D', // 49: Sextant-234 (011100)
	'\U0001FB0C', // 50: Sextant-134 (101100)
	'\U0001FB0B', // 51: Sextant-34 (001100)
	'\U0001FB0A', // 52: Sextant-124 (110100)
	'\U0001FB09', // 53: Sextant-24 (010100)
	'\U0001FB08', // 54: Sextant-14 (100100)
	'\U0001FB07', // 55: Sextant-4 (000100)
	'\U0001FB06', // 56: Sextant-123 (111000)
	'\U0001FB05', // 57: Sextant-23 (011000)
	'\U0001FB04', // 58: Sextant-13 (101000)
	'\U0001FB03', // 59: Sextant-3 (001000)
	'\U0001FB02', // 60: Sextant-12 (110000)
	'\U0001FB01', // 61: Sextant-2 (010000)
	'\U0001FB00', // 62: Sextant-1 (100000)
	' ',          // 63: Empty (000000)
}

// Representative partitions (32 entries) used for color optimization
// These represent key patterns covering 0-3 bits set, providing efficient
// coverage of the partition space without testing all 64 combinations
var sextantPartitions = [32]uint8{
	0,                  // 0 bits set
	1, 2, 4, 8, 16, 32, // 1 bit set (6 ways)
	3, 5, 9, 17, 33, 6, 10, 18, 34, 12, 20, 36, 24, 40, 48, // 2 bits set (15 of 15 ways)
	7, 11, 19, 35, 13, 21, 37, 25, 41, 49, // 3 bits set (10 of 20 ways)
}

// manhattanDistance returns the L1 distance between colors (kept tight for hot path)
func manhattanDistance(c1, c2 rgb) uint32 {
	var dist uint32
	if c1.r > c2.r {
		dist += uint32(c1.r - c2.r)
	} else {
		dist += uint32(c2.r - c1.r)
	}
	if c1.g > c2.g {
		dist += uint32(c1.g - c2.g)
	} else {
		dist += uint32(c2.g - c1.g)
	}
	if c1.b > c2.b {
		dist += uint32(c1.b - c2.b)
	} else {
		dist += uint32(c2.b - c1.b)
	}
	return dist
}

// findBestSextant finds the optimal sextant character and colors for a 2x3 pixel region
// using the color partition optimization algorithm from Notcurses
func findBestSextant(pixels [6]rgb) (char rune, fgColor, bgColor rgb) {
	bestPartitionIdx := 0
	minError := uint32(0xFFFFFFFF)
	var bestFg, bestBg rgb

	// Try each representative partition without allocating slices
	for i, partition := range sextantPartitions {
		var fgCount, bgCount uint32
		var fgRSum, fgGSum, fgBSum uint32
		var bgRSum, bgGSum, bgBSum uint32

		// Accumulate sums for foreground/background buckets
		for bit := range uint8(6) {
			p := pixels[bit]
			if partition&(1<<bit) != 0 {
				fgCount++
				fgRSum += uint32(p.r)
				fgGSum += uint32(p.g)
				fgBSum += uint32(p.b)
			} else {
				bgCount++
				bgRSum += uint32(p.r)
				bgGSum += uint32(p.g)
				bgBSum += uint32(p.b)
			}
		}

		// Division-by-zero protection for degenerate partitions.
		//
		// fgCount==0 occurs when partition=0 (all 6 bits=0, all background).
		// Partition 0 is intentionally included in sextantPartitions[] because it's useful
		// for monochrome regions where all 6 pixels are similar - it allows a perfect match
		// with minimal error by using only the background color.
		if fgCount == 0 {
			fgCount = 1
		}

		// bgCount==0 occurs when partition=63 (all 6 bits=1, all foreground).
		// Partition 63 is NOT in the sextantPartitions array, so bgCount==0 should never
		// happen in practice, but we protect against it defensively.
		//
		// For both cases: Setting count to 1 means dividing the sum by 1 (sum/1 = sum),
		// which produces a valid RGB even though we're not actually "averaging" any pixels.
		// This works correctly because the sum accumulation loop skipped those pixels entirely.
		if bgCount == 0 {
			bgCount = 1
		}

		fg := rgb{
			r: uint8((fgRSum + fgCount/2) / fgCount),
			g: uint8((fgGSum + fgCount/2) / fgCount),
			b: uint8((fgBSum + fgCount/2) / fgCount),
		}
		bg := rgb{
			r: uint8((bgRSum + bgCount/2) / bgCount),
			g: uint8((bgGSum + bgCount/2) / bgCount),
			b: uint8((bgBSum + bgCount/2) / bgCount),
		}

		// Calculate total error using Manhattan distance
		var totalError uint32
		for bit := range uint8(6) {
			p := pixels[bit]
			if partition&(1<<bit) != 0 {
				totalError += manhattanDistance(p, fg)
			} else {
				totalError += manhattanDistance(p, bg)
			}
		}

		if totalError < minError {
			minError = totalError
			bestPartitionIdx = i
			bestFg = fg
			bestBg = bg
			// Stop only on an exact match: a looser threshold ends the
			// search at partition 0 (one color) in smooth areas, so
			// gradients turn into flat cells.
			if totalError == 0 {
				break
			}
		}
	}

	charIndex := 63 - sextantPartitions[bestPartitionIdx]
	return sextantChars[charIndex], bestFg, bestBg
}
