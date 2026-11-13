// Command rendercompare is the helper behind docs/RENDERER_COMPARISON.md: it
// renders a picture with the encode package, and draws captured terminal
// screens (from tmux capture-pane -e) as PNG without a font, so sextant and
// braille output of different tools can be compared pixel for pixel.
// run.sh in this directory drives it; it isn't a user-facing command.
//
//	rendercompare encode -r sextant -cols 96 -rows 27 in.png > out.ans
//	rendercompare scale -w 192 -h 81 in.png out.png
//	rendercompare raster -cols 96 -rows 27 screen.ans out.png
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"os"
	"strconv"
	"strings"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
	"github.com/digitalemagine/webrtc-ascii-client/internal/resize"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: rendercompare encode|scale|raster [options] ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "encode":
		err = runEncode(os.Args[2:])
	case "scale":
		err = runScale(os.Args[2:])
	case "raster":
		err = runRaster(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rendercompare:", err)
		os.Exit(1)
	}
}

// runEncode scales a picture to the renderer's pixel grid (as termplay does)
// and writes the encoded frame to stdout.
func runEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ExitOnError)
	mode := fs.String("r", encode.Sextant, "renderer")
	cols := fs.Int("cols", 96, "width in characters")
	rows := fs.Int("rows", 27, "height in characters")
	mono := fs.Bool("mono", false, "without color")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("encode: one input picture expected")
	}
	img, err := load(fs.Arg(0))
	if err != nil {
		return err
	}
	cfg := encode.Config{Mode: *mode, Color: !*mono, Width: *cols, Height: *rows}
	enc, err := encode.New(cfg)
	if err != nil {
		return err
	}
	w, h := cfg.PixelSize()
	_, err = os.Stdout.Write(enc.Encode(nil, resize.Area(img, w, h)))
	return err
}

// runScale writes a picture scaled by area averaging: the pre-scaled input
// every tool gets, and the reference the metrics compare against.
func runScale(args []string) error {
	fs := flag.NewFlagSet("scale", flag.ExitOnError)
	w := fs.Int("w", 192, "width in pixels")
	h := fs.Int("h", 81, "height in pixels")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		return errors.New("scale: input and output expected")
	}
	img, err := load(fs.Arg(0))
	if err != nil {
		return err
	}
	return save(fs.Arg(1), resize.Area(img, *w, *h))
}

func load(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img, nil
}

func save(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Cell size of the raster: 12×24 divides evenly into sextant (2×3), quadrant
// (2×2), braille (2×4) and eighth-block (8) subdivisions.
const cellW, cellH = 12, 24

var (
	defaultFG = color.RGBA{0xd0, 0xd0, 0xd0, 0xff}
	defaultBG = color.RGBA{0, 0, 0, 0xff}
)

// runRaster draws a screen captured with tmux capture-pane -e -p.
func runRaster(args []string) error {
	fs := flag.NewFlagSet("raster", flag.ExitOnError)
	cols := fs.Int("cols", 96, "screen width in characters")
	rows := fs.Int("rows", 27, "screen height in characters")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		return errors.New("raster: input and output expected")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	img := image.NewRGBA(image.Rect(0, 0, *cols*cellW, *rows*cellH))
	draw.Draw(img, img.Bounds(), &image.Uniform{defaultBG}, image.Point{}, draw.Src)
	unknown := map[rune]int{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(nil, 1<<20)
	// Colors carry over from one line to the next: capture-pane writes an
	// SGR sequence only where the attributes change.
	fg, bg := defaultFG, defaultBG
	for y := 0; sc.Scan() && y < *rows; y++ {
		x := 0
		line := sc.Text()
		for i := 0; i < len(line) && x < *cols; {
			if strings.HasPrefix(line[i:], "\x1b[") {
				end := strings.IndexByte(line[i:], 'm')
				if end < 0 {
					return fmt.Errorf("line %d: unterminated escape sequence", y+1)
				}
				sgr(line[i+2:i+end], &fg, &bg)
				i += end + 1
				continue
			}
			r, size := decodeRune(line[i:])
			i += size
			if !drawCell(img, x, y, r, fg, bg) {
				unknown[r]++
			}
			x++
		}
	}
	for r, n := range unknown {
		fmt.Fprintf(os.Stderr, "rendercompare: %U drawn as background (%d cells)\n", r, n)
	}
	return save(fs.Arg(1), img)
}

func decodeRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 1
}

// sgr applies one SGR sequence (parameters without "\x1b[" and "m").
func sgr(params string, fg, bg *color.RGBA) {
	p := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	if len(p) == 0 {
		*fg, *bg = defaultFG, defaultBG
		return
	}
	n := func(i int) uint8 {
		if i >= len(p) {
			return 0
		}
		v, _ := strconv.Atoi(p[i])
		return uint8(v)
	}
	for i := 0; i < len(p); i++ {
		switch v, _ := strconv.Atoi(p[i]); {
		case v == 0:
			*fg, *bg = defaultFG, defaultBG
		case v == 39:
			*fg = defaultFG
		case v == 49:
			*bg = defaultBG
		case (v == 38 || v == 48) && i+1 < len(p):
			var c color.RGBA
			switch p[i+1] {
			case "2":
				c = color.RGBA{n(i + 2), n(i + 3), n(i + 4), 0xff}
				i += 4
			case "5":
				c = xterm256(n(i + 2))
				i += 2
			}
			if v == 38 {
				*fg = c
			} else {
				*bg = c
			}
		case v >= 30 && v <= 37:
			*fg = xterm256(uint8(v - 30))
		case v >= 40 && v <= 47:
			*bg = xterm256(uint8(v - 40))
		case v >= 90 && v <= 97:
			*fg = xterm256(uint8(v - 90 + 8))
		case v >= 100 && v <= 107:
			*bg = xterm256(uint8(v - 100 + 8))
		}
	}
}

// xterm256 returns xterm's default color for a 256-color palette index.
func xterm256(i uint8) color.RGBA {
	base := [16][3]uint8{
		{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
		{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	switch {
	case i < 16:
		return color.RGBA{base[i][0], base[i][1], base[i][2], 0xff}
	case i < 232:
		lv := func(v uint8) uint8 {
			if v == 0 {
				return 0
			}
			return 55 + 40*v
		}
		i -= 16
		return color.RGBA{lv(i / 36), lv(i / 6 % 6), lv(i % 6), 0xff}
	default:
		g := 8 + 10*(i-232)
		return color.RGBA{g, g, g, 0xff}
	}
}

// drawCell draws one character cell; it reports false for characters it
// doesn't know, which are left as background.
func drawCell(img *image.RGBA, cx, cy int, r rune, fg, bg color.RGBA) bool {
	x0, y0 := cx*cellW, cy*cellH
	fill := func(x, y, w, h int, c color.RGBA) {
		draw.Draw(img, image.Rect(x0+x, y0+y, x0+x+w, y0+y+h), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	fill(0, 0, cellW, cellH, bg)
	// grid fills the parts of a cols×rows subdivision whose bit is set,
	// bit 0 at the top left, row by row.
	grid := func(bits, cols, rows int) {
		w, h := cellW/cols, cellH/rows
		for i := range cols * rows {
			if bits&(1<<i) != 0 {
				fill(i%cols*w, i/cols*h, w, h, fg)
			}
		}
	}
	switch {
	case r == ' ' || r == 0x2800:
	case r >= 0x1FB00 && r <= 0x1FB3B:
		// Sextants in order of their bit pattern, skipping empty, left
		// half (21), right half (42) and full (63), which exist elsewhere.
		bits := int(r-0x1FB00) + 1
		if bits >= 21 {
			bits++
		}
		if bits >= 42 {
			bits++
		}
		grid(bits, 2, 3)
	case r == 0x2580: // upper half
		fill(0, 0, cellW, cellH/2, fg)
	case r >= 0x2581 && r <= 0x2588: // lower eighths up to full block
		h := int(r-0x2580) * cellH / 8
		fill(0, cellH-h, cellW, h, fg)
	case r >= 0x2589 && r <= 0x258F: // left seven eighths down to one eighth
		w := int(0x2590-r) * cellW / 8
		fill(0, 0, w, cellH, fg)
	case r == 0x2590: // right half
		fill(cellW/2, 0, cellW/2, cellH, fg)
	case r >= 0x2591 && r <= 0x2593: // shades: 1/4, 1/2, 3/4 foreground
		fill(0, 0, cellW, cellH, mix(fg, bg, int(r-0x2590)))
	case r == 0x2594: // upper eighth
		fill(0, 0, cellW, cellH/8, fg)
	case r == 0x2595: // right eighth
		fill(cellW-cellW/8, 0, cellW/8, cellH, fg)
	case r >= 0x2596 && r <= 0x259F: // quadrants
		grid([...]int{4, 8, 1, 13, 9, 7, 11, 2, 6, 14}[r-0x2596], 2, 2)
	case r >= 0x2800 && r <= 0x28FF:
		brailleDots(img, x0, y0, int(r-0x2800), fg)
	default:
		return false
	}
	return true
}

// mix returns q quarters of fg over bg.
func mix(fg, bg color.RGBA, q int) color.RGBA {
	m := func(a, b uint8) uint8 { return uint8((int(a)*q + int(b)*(4-q)) / 4) }
	return color.RGBA{m(fg.R, bg.R), m(fg.G, bg.G), m(fg.B, bg.B), 0xff}
}

// brailleDots draws the raised dots of a braille pattern as round dots in a
// 2×4 grid, as terminals do.
func brailleDots(img *image.RGBA, x0, y0, bits int, fg color.RGBA) {
	// Dot numbering: 1 2 3 7 down the left column, 4 5 6 8 down the right.
	pos := [8][2]int{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {0, 3}, {1, 3}}
	const w, h, rad = cellW / 2, cellH / 4, 2
	for i, p := range pos {
		if bits&(1<<i) == 0 {
			continue
		}
		cx, cy := x0+p[0]*w+w/2, y0+p[1]*h+h/2
		for dy := -rad; dy < rad; dy++ {
			for dx := -rad; dx < rad; dx++ {
				// Distance from the center of the pixel to the dot's center.
				if (2*dx+1)*(2*dx+1)+(2*dy+1)*(2*dy+1) <= 4*rad*rad {
					img.SetRGBA(cx+dx, cy+dy, fg)
				}
			}
		}
	}
}
