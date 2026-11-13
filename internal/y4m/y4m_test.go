package y4m

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// stream builds a Y4M stream of 2×2 frames in 4:2:0 with the given header
// extras and one (y, cb, cr) per frame.
func stream(extra string, frames ...[3]byte) []byte {
	var b bytes.Buffer
	b.WriteString("YUV4MPEG2 W2 H2 F25:1 Ip A1:1" + extra + "\n")
	for _, f := range frames {
		b.WriteString("FRAME\n")
		b.Write([]byte{f[0], f[0], f[0], f[0], f[1], f[2]})
	}
	return b.Bytes()
}

func near(a, b uint8) bool { return a-b <= 1 || b-a <= 1 }

// BT.601 reference colors convert to the expected RGB in both ranges.
func TestColors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		extra string
		yuv   [3]byte
		rgb   [3]uint8
	}{
		{"limited black", "", [3]byte{16, 128, 128}, [3]uint8{0, 0, 0}},
		{"limited white", "", [3]byte{235, 128, 128}, [3]uint8{255, 255, 255}},
		{"limited red", "", [3]byte{81, 90, 240}, [3]uint8{255, 0, 0}},
		{"limited blue", "", [3]byte{41, 240, 110}, [3]uint8{0, 0, 255}},
		{"full black", " XCOLORRANGE=FULL", [3]byte{0, 128, 128}, [3]uint8{0, 0, 0}},
		{"full white", " XCOLORRANGE=FULL", [3]byte{255, 128, 128}, [3]uint8{255, 255, 255}},
		{"full red", " XCOLORRANGE=FULL", [3]byte{76, 85, 255}, [3]uint8{254, 0, 0}},
	}
	for _, tt := range tests {
		r, err := NewReader(bytes.NewReader(stream(tt.extra, tt.yuv)))
		if err != nil {
			t.Fatal(err)
		}
		img, err := r.ReadFrame(nil)
		if err != nil {
			t.Fatal(err)
		}
		p := img.Pix
		if !near(p[0], tt.rgb[0]) || !near(p[1], tt.rgb[1]) || !near(p[2], tt.rgb[2]) || p[3] != 255 {
			t.Errorf("%s: got %v, want %v", tt.name, p[:4], tt.rgb)
		}
	}
}

// Every supported color space matches FFmpeg's conversion of the same
// frames. FFmpeg interpolates chroma and the reader repeats it, so color
// edges differ; the mean error must stay small.
func TestMatchesFFmpeg(t *testing.T) {
	t.Parallel()

	for _, pf := range []string{"yuv420p", "yuvj420p", "yuv422p", "yuv444p", "gray"} {
		t.Run(pf, func(t *testing.T) {
			t.Parallel()
			f, err := os.Open("testdata/" + pf + ".y4m")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			want, err := os.ReadFile("testdata/" + pf + ".rgba")
			if err != nil {
				t.Fatal(err)
			}
			r, err := NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			if h := r.Header(); h.Width != 32 || h.Height != 24 || h.FPS() != 10 {
				t.Fatalf("header %+v", h)
			}
			var got []byte
			for {
				img, err := r.ReadFrame(nil)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, img.Pix...)
			}
			if len(got) != len(want) {
				t.Fatalf("decoded %d bytes, want %d (2 frames)", len(got), len(want))
			}
			var sum, n int
			for i := range got {
				if i%4 == 3 {
					continue
				}
				d := int(got[i]) - int(want[i])
				sum += max(d, -d)
				n++
			}
			if mean := float64(sum) / float64(n); mean > 4 {
				t.Errorf("mean error %.2f per channel, want < 4", mean)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()

	full := stream("", [3]byte{16, 128, 128})
	tests := []struct {
		name  string
		input string
		want  string // in the error from NewReader or the first ReadFrame
	}{
		{"empty", "", "empty input"},
		{"not y4m", "RIFF....WAVE", "not a YUV4MPEG2 stream"},
		{"no size", "YUV4MPEG2 F25:1\n", "invalid frame size"},
		{"huge", "YUV4MPEG2 W100000 H100000\n", "invalid frame size"},
		{"size overflows int", "YUV4MPEG2 W4294967296 H4294967296\n", "invalid frame size"},
		{"bad color space", "YUV4MPEG2 W2 H2 C420p10\n", "unsupported color space"},
		{"bad frame rate", "YUV4MPEG2 W2 H2 F25\n", "header parameter"},
		{"header without newline", "YUV4MPEG2 W2 H2", "unexpected EOF"},
		{"truncated frame", string(full[:len(full)-1]), "unexpected EOF"},
		{"not a frame", "YUV4MPEG2 W2 H2\nFROG\n", "expected FRAME"},
		{"endless line", "YUV4MPEG2 W2 H2 " + strings.Repeat("X", 5000) + "\n", "longer than"},
	}
	for _, tt := range tests {
		r, err := NewReader(strings.NewReader(tt.input))
		if err == nil {
			_, err = r.ReadFrame(nil)
		}
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %v, want %q", tt.name, err, tt.want)
		}
	}
}

// A clean end of stream is io.EOF, and dst is reused when it fits.
func TestEOFAndReuse(t *testing.T) {
	t.Parallel()

	r, err := NewReader(bytes.NewReader(stream("", [3]byte{16, 128, 128}, [3]byte{235, 128, 128})))
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.ReadFrame(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.ReadFrame(first)
	if err != nil || second != first || second.Pix[0] != 255 {
		t.Fatalf("second frame: %v, reused %v, first byte %d", err, second == first, second.Pix[0])
	}
	if _, err := r.ReadFrame(nil); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last frame: %v, want io.EOF", err)
	}
}

// FuzzReader: arbitrary input must not panic or allocate absurdly.
func FuzzReader(f *testing.F) {
	f.Add(stream("", [3]byte{16, 128, 128}))
	f.Add([]byte("YUV4MPEG2 W3 H3 C422\nFRAME x=1\n" + strings.Repeat("\x80", 27)))
	f.Add([]byte("YUV4MPEG2 W1 H1 Cmono\nFRAME\n\x10"))
	f.Add([]byte("YUV4MPEG2 W4294967296 H4294967296 F25:1\nFRAME\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		if h := r.Header(); h.Width*h.Height > 1<<20 {
			return // valid but too big to be worth decoding here
		}
		for range 4 {
			if _, err := r.ReadFrame(nil); err != nil {
				return
			}
		}
	})
}
