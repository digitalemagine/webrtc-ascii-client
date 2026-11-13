package encode

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"log/slog"
	"strconv"

	"github.com/mattn/go-sixel"
)

// sixelEncoder writes the image with the Sixel protocol: 256-color palette
// with dithering (go-sixel).
type sixelEncoder struct{ width, height int }

func (e *sixelEncoder) Encode(dst []byte, img *image.RGBA) []byte {
	if !hasSize(img, e.width, e.height) {
		return dst
	}
	buf := bytes.NewBuffer(dst)
	enc := sixel.NewEncoder(buf)
	enc.Colors = 256
	enc.Dither = true
	if err := enc.Encode(img); err != nil {
		slog.Warn("Sixel encoding failed", "error", err)
		return dst
	}
	return buf.Bytes()
}

// iterm2Encoder writes the image as a PNG with the iTerm2 inline image
// protocol (OSC 1337), sized in character cells.
type iterm2Encoder struct {
	width, height int
	png           png.Encoder
	pngBuf        bytes.Buffer
}

// iTerm2Cells is the display size in character cells for an image of w×h
// pixels, assuming cells of about 10×20 pixels, at least 40×20 cells.
func iTerm2Cells(w, h int) (cols, rows int) {
	return max(w/10, 40), max(h/20, 20)
}

func (e *iterm2Encoder) Encode(dst []byte, img *image.RGBA) []byte {
	if !hasSize(img, e.width, e.height) {
		return dst
	}
	e.pngBuf.Reset()
	if err := e.png.Encode(&e.pngBuf, img); err != nil {
		slog.Warn("PNG encoding failed", "error", err)
		return dst
	}
	cols, rows := iTerm2Cells(e.width, e.height)
	dst = append(dst, "\033]1337;File=inline=1;width="...)
	dst = strconv.AppendInt(dst, int64(cols), 10)
	dst = append(dst, ";height="...)
	dst = strconv.AppendInt(dst, int64(rows), 10)
	dst = append(dst, ':')
	dst = base64.StdEncoding.AppendEncode(dst, e.pngBuf.Bytes())
	return append(dst, '\a', '\n')
}
