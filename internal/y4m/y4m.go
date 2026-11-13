// Package y4m reads YUV4MPEG2 (Y4M) video, as written by
// `ffmpeg -f yuv4mpegpipe`, and converts frames to RGBA.
//
// A stream is a header line ("YUV4MPEG2 W<w> H<h> F<n>:<d> C<colorspace> …")
// followed by frames, each a "FRAME[ params]" line and the Y, Cb and Cr
// planes. Supported color spaces: 4:2:0 (C420jpeg, the default, C420mpeg2,
// C420paldv, C420), C422, C444 and Cmono. Samples are limited range (TV)
// unless the header has XCOLORRANGE=FULL, as ffmpeg writes it.
package y4m

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"strconv"
	"strings"
)

// signature starts every stream.
const signature = "YUV4MPEG2 "

// maxLine bounds header and frame-header lines: real ones are well below.
const maxLine = 4096

// maxPixels bounds the frame size a header may announce (16K × 16K).
const maxPixels = 16384 * 16384

// Header describes a stream.
type Header struct {
	Width, Height int
	// FrameRate is frames per second as a fraction; both are 0 if unknown.
	FrameRateNum, FrameRateDen int
	// Colorspace is the C parameter, e.g. "420jpeg", "444", "mono".
	Colorspace string
	// FullRange is true for XCOLORRANGE=FULL, false for limited (TV) range.
	FullRange bool
}

// FPS returns the frame rate, or 0 if the header has none.
func (h Header) FPS() float64 {
	if h.FrameRateNum <= 0 || h.FrameRateDen <= 0 {
		return 0
	}
	return float64(h.FrameRateNum) / float64(h.FrameRateDen)
}

// chroma subsampling: chroma plane size in units of the luma size.
type subsampling struct{ shiftX, shiftY uint } // 0 or 1

var colorspaces = map[string]*subsampling{
	"420jpeg":  {1, 1},
	"420mpeg2": {1, 1},
	"420paldv": {1, 1},
	"420":      {1, 1},
	"422":      {1, 0},
	"444":      {0, 0},
	"mono":     nil, // no chroma planes
}

// Reader reads frames from a Y4M stream. It is not safe for concurrent use.
type Reader struct {
	r      *bufio.Reader
	header Header
	sub    *subsampling
	buf    []byte // one frame's planes
	frames int
}

// NewReader reads the stream header from r.
func NewReader(r io.Reader) (*Reader, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	if magic, _ := br.Peek(len(signature)); string(magic) != signature {
		if len(magic) == 0 {
			return nil, errors.New("y4m: empty input")
		}
		return nil, fmt.Errorf("y4m: not a YUV4MPEG2 stream (starts with %q; convert with ffmpeg ... -f yuv4mpegpipe -)", truncate(magic))
	}
	line, err := readLine(br)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("y4m: empty input")
		}
		return nil, fmt.Errorf("y4m: header: %w", err)
	}
	h, err := parseHeader(line)
	if err != nil {
		return nil, err
	}
	sub := colorspaces[h.Colorspace]
	cw, ch := chromaSize(h, sub)
	size := h.Width*h.Height + 2*cw*ch
	return &Reader{r: br, header: h, sub: sub, buf: make([]byte, size)}, nil
}

// Header returns the stream header.
func (r *Reader) Header() Header { return r.header }

// ReadFrame reads the next frame and converts it to RGBA, into dst if it has
// the stream's size (otherwise into a new image), and returns it. At the end
// of the stream it returns io.EOF; a frame cut short returns
// io.ErrUnexpectedEOF.
func (r *Reader) ReadFrame(dst *image.RGBA) (*image.RGBA, error) {
	line, err := readLine(r.r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("y4m: frame %d: %w", r.frames+1, err)
	}
	if !bytes.Equal(line, []byte("FRAME")) && !bytes.HasPrefix(line, []byte("FRAME ")) {
		return nil, fmt.Errorf("y4m: frame %d: expected FRAME, got %q", r.frames+1, truncate(line))
	}
	if _, err := io.ReadFull(r.r, r.buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("y4m: frame %d: %w", r.frames+1, err)
	}
	r.frames++

	w, h := r.header.Width, r.header.Height
	if dst == nil || dst.Bounds().Dx() != w || dst.Bounds().Dy() != h {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	r.convert(dst)
	return dst, nil
}

func (r *Reader) convert(dst *image.RGBA) {
	w, h := r.header.Width, r.header.Height
	luma := r.buf[:w*h]
	cw, ch := chromaSize(r.header, r.sub)
	cb := r.buf[w*h : w*h+cw*ch]
	cr := r.buf[w*h+cw*ch:]
	toRGB := ycbcrLimited
	if r.header.FullRange {
		toRGB = ycbcrFull
	}
	for y := range h {
		row := dst.Pix[y*dst.Stride:]
		for x := range w {
			cbv, crv := uint8(128), uint8(128)
			if r.sub != nil {
				i := (y>>r.sub.shiftY)*cw + x>>r.sub.shiftX
				cbv, crv = cb[i], cr[i]
			}
			R, G, B := toRGB(luma[y*w+x], cbv, crv)
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = R, G, B, 255
		}
	}
}

func chromaSize(h Header, sub *subsampling) (w, ht int) {
	if sub == nil {
		return 0, 0
	}
	return (h.Width + (1<<sub.shiftX - 1)) >> sub.shiftX, (h.Height + (1<<sub.shiftY - 1)) >> sub.shiftY
}

// ycbcrLimited converts BT.601 limited-range YCbCr (Y 16–235, C 16–240).
func ycbcrLimited(y, cb, cr uint8) (r, g, b uint8) {
	c := 298 * (int(y) - 16)
	d, e := int(cb)-128, int(cr)-128
	return clamp((c + 409*e + 128) >> 8), clamp((c - 100*d - 208*e + 128) >> 8), clamp((c + 516*d + 128) >> 8)
}

// ycbcrFull converts BT.601 full-range YCbCr (JFIF).
func ycbcrFull(y, cb, cr uint8) (r, g, b uint8) {
	c := int(y) << 8
	d, e := int(cb)-128, int(cr)-128
	return clamp((c + 359*e + 128) >> 8), clamp((c - 88*d - 183*e + 128) >> 8), clamp((c + 454*d + 128) >> 8)
}

func clamp(v int) uint8 { return uint8(min(max(v, 0), 255)) }

func parseHeader(line []byte) (Header, error) {
	fields := strings.Fields(string(line))
	if len(fields) == 0 || fields[0] != "YUV4MPEG2" {
		return Header{}, fmt.Errorf("y4m: not a YUV4MPEG2 stream (starts with %q)", truncate(line))
	}
	h := Header{Colorspace: "420jpeg"}
	for _, f := range fields[1:] {
		val := f[1:]
		var err error
		switch f[0] {
		case 'W':
			h.Width, err = strconv.Atoi(val)
		case 'H':
			h.Height, err = strconv.Atoi(val)
		case 'F':
			num, den, ok := strings.Cut(val, ":")
			if !ok {
				err = errors.New("want num:den")
			} else if h.FrameRateNum, err = strconv.Atoi(num); err == nil {
				h.FrameRateDen, err = strconv.Atoi(den)
			}
		case 'C':
			h.Colorspace = val
		case 'X':
			if strings.EqualFold(val, "COLORRANGE=FULL") {
				h.FullRange = true
			}
		}
		if err != nil {
			return Header{}, fmt.Errorf("y4m: header parameter %q: %w", f, err)
		}
	}
	// Divide rather than multiply: the product of two huge values overflows.
	if h.Width <= 0 || h.Height <= 0 || h.Width > maxPixels/h.Height {
		return Header{}, fmt.Errorf("y4m: invalid frame size %dx%d", h.Width, h.Height)
	}
	if _, ok := colorspaces[h.Colorspace]; !ok {
		return Header{}, fmt.Errorf("y4m: unsupported color space C%s (convert with ffmpeg -pix_fmt yuv420p)", h.Colorspace)
	}
	return h, nil
}

// readLine reads a line up to maxLine bytes, without the newline.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, fmt.Errorf("line longer than %d bytes", maxLine)
		}
		if err == nil {
			return line[:len(line)-1], nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
}

func truncate(b []byte) string {
	if len(b) > 32 {
		b = b[:32]
	}
	return string(b)
}
