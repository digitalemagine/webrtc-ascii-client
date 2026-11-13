// Command bandwidth-analyzer measures how many bytes each renderer writes
// to the terminal per frame, for a fixed test image, and prints a Markdown
// table (used in docs/BANDWIDTH_ANALYSIS.md).
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
)

// fps is the frame rate the per-second figures assume.
const fps = 30

// statusBytes approximates what the display adds per frame: cursor and
// synchronized-update sequences and a two-line status line on an
// 80-column terminal.
const statusBytes = 2*80 + 40

func main() {
	configs := []struct {
		name string
		cfg  encode.Config
	}{
		{"Classic", encode.Config{Mode: encode.Classic, Width: 80, Height: 24}},
		{"Classic", encode.Config{Mode: encode.Classic, Width: 160, Height: 48}},
		{"Enhanced mono, quality 1", encode.Config{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 1}},
		{"Enhanced mono, quality 4", encode.Config{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 4}},
		{"Enhanced color, quality 2", encode.Config{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 2, Color: true}},
		{"Enhanced color, quality 4", encode.Config{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 4, Color: true}},
		{"Blocks mono", encode.Config{Mode: encode.Blocks, Width: 80, Height: 24}},
		{"Blocks color", encode.Config{Mode: encode.Blocks, Width: 80, Height: 24, Color: true}},
		{"Blocks mono", encode.Config{Mode: encode.Blocks, Width: 160, Height: 48}},
		{"Sextant mono", encode.Config{Mode: encode.Sextant, Width: 80, Height: 24}},
		{"Sextant color", encode.Config{Mode: encode.Sextant, Width: 80, Height: 24, Color: true}},
		{"Braille mono", encode.Config{Mode: encode.Braille, Width: 80, Height: 24}},
		{"Braille color", encode.Config{Mode: encode.Braille, Width: 80, Height: 24, Color: true}},
		{"Sixel", encode.Config{Mode: encode.Sixel, Width: 80, Height: 48}},
		{"Sixel", encode.Config{Mode: encode.Sixel, Width: 320, Height: 240}},
		{"iTerm2", encode.Config{Mode: encode.ITerm2, Width: 80, Height: 48}},
		{"iTerm2", encode.Config{Mode: encode.ITerm2, Width: 320, Height: 240}},
	}

	fmt.Printf("Bytes per frame for encode.Pattern at each renderer's pixel size; KB/s and MB/min at %d FPS.\n", fps)
	fmt.Printf("Frame bytes only; the display adds about %d bytes per frame (status line, cursor control).\n\n", statusBytes)
	fmt.Println("| Renderer | Size | Bytes/frame | KB/s | MB/min |")
	fmt.Println("|---|---|---:|---:|---:|")
	for _, c := range configs {
		enc, err := encode.New(c.cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		n := len(enc.Encode(nil, encode.Pattern(c.cfg.PixelSize())))
		size := fmt.Sprintf("%d×%d", c.cfg.Width, c.cfg.Height)
		if encode.IsImageMode(c.cfg.Mode) {
			size += " px"
		}
		kbps := float64(n*fps) / 1024
		fmt.Printf("| %s | %s | %s | %s | %.1f |\n", c.name, size, thousands(n), thousands(int(kbps+0.5)), kbps*60/1024)
	}
}

// thousands formats n with comma separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
