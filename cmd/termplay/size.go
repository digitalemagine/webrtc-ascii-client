package main

import (
	"math"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
)

// Approximate terminal cell size in pixels, for the image renderers.
const (
	cellWidthPx  = 10
	cellHeightPx = 20
	statusRows   = 2
)

// fitSize chooses the output size for a frameW×frameH input: characters for
// text renderers, pixels for sixel/iterm2. Explicit width or height win;
// a missing one follows the frame's aspect ratio (text cells are taken to be
// twice as tall as wide). With neither, the output fills the terminal
// (less the status line) if termCols/termRows are known, else 80 columns
// (640 pixels for images).
func fitSize(mode string, frameW, frameH, width, height, termCols, termRows int) (int, int) {
	aspect := float64(frameW) / float64(frameH) // width per height, in pixels
	unitAspect := 2 * aspect                    // columns per row, for text
	maxW, maxH, defaultW := termCols, termRows-statusRows, 80
	if encode.IsImageMode(mode) {
		unitAspect = aspect
		maxW, maxH, defaultW = termCols*cellWidthPx, (termRows-statusRows)*cellHeightPx, 640
	}
	round := func(v float64) int { return max(int(math.Round(v)), 1) }

	switch {
	case width > 0 && height > 0:
	case width > 0:
		height = round(float64(width) / unitAspect)
	case height > 0:
		width = round(float64(height) * unitAspect)
	case termCols > 0 && termRows > statusRows:
		width, height = maxW, round(float64(maxW)/unitAspect)
		if height > maxH {
			width, height = round(float64(maxH)*unitAspect), maxH
		}
	default:
		width, height = defaultW, round(float64(defaultW)/unitAspect)
	}
	return max(width, 1), max(height, 1)
}
