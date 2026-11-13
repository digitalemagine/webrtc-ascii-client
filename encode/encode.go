// Package encode turns images into terminal output: ASCII art, Unicode
// blocks, braille or sextant characters, or real images (Sixel, iTerm2).
//
// Choose a renderer and a size with a Config, scale your image to
// Config.PixelSize(), and call Encode:
//
//	cfg := encode.Config{Mode: encode.Sextant, Color: true, Width: 80, Height: 24}
//	enc, err := encode.New(cfg)
//	...
//	frame := enc.Encode(buf[:0], img) // img is cfg.PixelSize() pixels
//	os.Stdout.Write(frame)
//
// Text renderers (classic, enhanced, blocks, braille, sextant) are sized in
// characters and emit one line per row, each ending in "\n"; color uses
// 24-bit ANSI SGR sequences, reset at the end of each line. Image renderers
// (sixel, iterm2) are sized in pixels and emit one escape sequence.
// Encoders don't move the cursor or clear the screen: to animate, put
// "\x1b[H" (cursor home) before each frame.
//
// Encode appends to the buffer and doesn't allocate for text renderers, so
// reusing one buffer per stream keeps the steady state allocation-free.
// Output is deterministic: the same image gives the same bytes on every
// platform. The API may still change before 1.0.
package encode

import (
	"fmt"
	"image"
	"slices"
	"strconv"
)

// Encoder turns one image into one frame of terminal output.
//
// The image must be exactly Config.PixelSize(); any other size returns dst
// unchanged (a frame decoded for the previous configuration). An Encoder may
// keep scratch buffers, so it is not safe for concurrent use.
type Encoder interface {
	Encode(dst []byte, img *image.RGBA) []byte
}

// Renderer modes, in the order of the interactive keys 1–7.
const (
	Classic  = "classic"
	Enhanced = "enhanced"
	Blocks   = "blocks"
	Braille  = "braille"
	Sextant  = "sextant"
	Sixel    = "sixel"
	ITerm2   = "iterm2"
)

// Modes lists the renderer modes in key order.
var Modes = []string{Classic, Enhanced, Blocks, Braille, Sextant, Sixel, ITerm2}

// Config describes what to encode.
type Config struct {
	Mode  string
	Color bool // enhanced, blocks, braille, sextant
	// Quality is the enhanced renderer's supersampling factor, 1–4.
	Quality int
	// Width and Height are in characters for text modes and in pixels for
	// the image modes (sixel, iterm2).
	Width, Height int
}

// IsImageMode reports whether mode draws real images sized in pixels.
func IsImageMode(mode string) bool {
	return mode == Sixel || mode == ITerm2
}

// HasColorOption reports whether mode has a color and a monochrome form.
func HasColorOption(mode string) bool {
	return mode == Enhanced || mode == Blocks || mode == Braille || mode == Sextant
}

// HasQuality reports whether mode uses Config.Quality.
func HasQuality(mode string) bool {
	return mode == Enhanced
}

// Validate reports a configuration that New would reject.
func (c Config) Validate() error {
	if !slices.Contains(Modes, c.Mode) {
		return fmt.Errorf("unknown renderer %q (want one of %v)", c.Mode, Modes)
	}
	if c.Width <= 0 || c.Height <= 0 {
		return fmt.Errorf("invalid size %dx%d (must be positive)", c.Width, c.Height)
	}
	if HasQuality(c.Mode) && (c.Quality < 1 || c.Quality > 4) {
		return fmt.Errorf("invalid quality %d (want 1-4)", c.Quality)
	}
	return nil
}

// PixelSize is the image size the encoder expects: scale each image to it
// before calling Encode.
func (c Config) PixelSize() (width, height int) {
	switch c.Mode {
	case Enhanced:
		return c.Width * c.Quality, c.Height * c.Quality
	case Blocks:
		return c.Width, c.Height * 2
	case Braille:
		return c.Width * 2, c.Height * 4
	case Sextant:
		return c.Width * 2, c.Height * 3
	default: // classic: one pixel per cell; image modes: pixels
		return c.Width, c.Height
	}
}

// Rows is how many terminal rows a frame occupies, so the display can place
// the status line and know whether the frame fits; 0 if unknown (Sixel,
// whose height in rows depends on the terminal's cell size).
func (c Config) Rows() int {
	switch c.Mode {
	case Sixel:
		return 0
	case ITerm2:
		_, rows := iTerm2Cells(c.Width, c.Height)
		return rows
	default:
		return c.Height
	}
}

// New returns the encoder for c.
func New(c Config) (Encoder, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	switch c.Mode {
	case Classic:
		return &classicEncoder{cols: c.Width, rows: c.Height}, nil
	case Enhanced:
		return newEnhancedEncoder(c), nil
	case Blocks:
		return &blocksEncoder{cols: c.Width, rows: c.Height, color: c.Color}, nil
	case Braille:
		return &brailleEncoder{cols: c.Width, rows: c.Height, color: c.Color}, nil
	case Sextant:
		return &sextantEncoder{cols: c.Width, rows: c.Height, color: c.Color}, nil
	case Sixel:
		return &sixelEncoder{width: c.Width, height: c.Height}, nil
	default:
		return &iterm2Encoder{width: c.Width, height: c.Height}, nil
	}
}

// hasSize reports whether img is exactly w×h pixels.
func hasSize(img *image.RGBA, w, h int) bool {
	b := img.Bounds()
	return b.Dx() == w && b.Dy() == h
}

// pixel returns the RGB of img at (x, y), relative to its bounds.
func pixel(img *image.RGBA, x, y int) (r, g, b uint8) {
	i := y*img.Stride + x*4
	return img.Pix[i], img.Pix[i+1], img.Pix[i+2]
}

// luma8 is the Rec. 601 luma of an 8-bit RGB color, in integer arithmetic.
func luma8(r, g, b uint8) uint8 {
	return uint8((299*int(r) + 587*int(g) + 114*int(b)) / 1000)
}

// appendColor appends an SGR 24-bit color sequence: layer 38 for the
// foreground, 48 for the background.
func appendColor(dst []byte, layer int, r, g, b uint8) []byte {
	dst = append(dst, '\033', '[')
	dst = strconv.AppendInt(dst, int64(layer), 10)
	dst = append(dst, ";2;"...)
	dst = strconv.AppendInt(dst, int64(r), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(g), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(b), 10)
	return append(dst, 'm')
}

// Determinism, for contributors: golden files pin the output, and Go may
// fuse x*y+z into one fused multiply-add on arm64 (differently with -race),
// which changes the last bit and can flip a character. So luma uses integer
// arithmetic, and floating-point products that feed an addition are
// converted explicitly (float64(x*y)), which forces rounding and prevents
// fusion.

const (
	sgrReset = "\033[0m"
	fgLayer  = 38
	bgLayer  = 48
)
