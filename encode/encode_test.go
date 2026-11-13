package encode

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

var goldenConfigs = []Config{
	{Mode: Classic, Width: 32, Height: 10},
	{Mode: Enhanced, Width: 32, Height: 10, Quality: 1},
	{Mode: Enhanced, Width: 32, Height: 10, Quality: 4},
	{Mode: Enhanced, Width: 32, Height: 10, Quality: 2, Color: true},
	{Mode: Blocks, Width: 32, Height: 10},
	{Mode: Blocks, Width: 32, Height: 10, Color: true},
	{Mode: Braille, Width: 32, Height: 10},
	{Mode: Braille, Width: 32, Height: 10, Color: true},
	{Mode: Sextant, Width: 32, Height: 10},
	{Mode: Sextant, Width: 32, Height: 10, Color: true},
	{Mode: Sixel, Width: 64, Height: 48},
	{Mode: ITerm2, Width: 64, Height: 48},
}

func goldenName(c Config) string {
	name := c.Mode
	if HasQuality(c.Mode) {
		name += fmt.Sprintf("-q%d", c.Quality)
	}
	if c.Color {
		name += "-color"
	}
	return name + ".golden"
}

// Each renderer's output for a fixed image is pinned in testdata/. Run
// `go test ./internal/encode -update` after an intended change and review
// the diff (e.g. `cat` the files in a terminal).
func TestGolden(t *testing.T) {
	for _, c := range goldenConfigs {
		t.Run(goldenName(c), func(t *testing.T) {
			enc, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			got := enc.Encode(nil, Pattern(c.PixelSize()))
			path := filepath.Join("testdata", goldenName(c))
			if *update {
				if werr := os.WriteFile(path, got, 0o644); werr != nil {
					t.Fatal(werr)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("output differs from %s (%d bytes, want %d); run with -update if intended", path, len(got), len(want))
			}
		})
	}
}

// Encoding appends to dst and is repeatable with reused scratch buffers.
func TestEncodeAppendsAndRepeats(t *testing.T) {
	t.Parallel()
	for _, c := range goldenConfigs {
		enc, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		img := Pattern(c.PixelSize())
		first := enc.Encode([]byte("prefix"), img)
		second := enc.Encode(nil, img)
		if !bytes.HasPrefix(first, []byte("prefix")) || !bytes.Equal(first[len("prefix"):], second) {
			t.Errorf("%s: not appending or not repeatable", goldenName(c))
		}
	}
}

// A frame of another size (decoded for a previous configuration) is skipped.
func TestEncodeSkipsWrongSize(t *testing.T) {
	t.Parallel()
	for _, c := range goldenConfigs {
		enc, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		w, h := c.PixelSize()
		if got := enc.Encode(nil, Pattern(w+1, h)); len(got) != 0 {
			t.Errorf("%s: encoded a %dx%d image, want nothing", goldenName(c), w+1, h)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	for _, c := range []Config{
		{Mode: "ascii-art", Width: 80, Height: 24},
		{Mode: Classic, Width: 0, Height: 24},
		{Mode: Enhanced, Width: 80, Height: 24, Quality: 5},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("New(%+v) accepted an invalid config", c)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	for _, c := range []Config{
		{Mode: Classic, Width: 160, Height: 48},
		{Mode: Enhanced, Width: 160, Height: 48, Quality: 2, Color: true},
		{Mode: Blocks, Width: 160, Height: 48, Color: true},
		{Mode: Braille, Width: 160, Height: 48, Color: true},
		{Mode: Sextant, Width: 160, Height: 48, Color: true},
		{Mode: Sixel, Width: 320, Height: 240},
		{Mode: ITerm2, Width: 320, Height: 240},
	} {
		b.Run(goldenName(c), func(b *testing.B) {
			enc, err := New(c)
			if err != nil {
				b.Fatal(err)
			}
			img := Pattern(c.PixelSize())
			var buf []byte
			b.ReportAllocs()
			for b.Loop() {
				buf = enc.Encode(buf[:0], img)
			}
			b.SetBytes(int64(len(buf)))
		})
	}
}
