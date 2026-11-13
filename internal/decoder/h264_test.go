package decoder

import (
	"os"
	"testing"

	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder/decodertest"
)

// readClipNALs returns the NAL units of testdata/clip.h264, each with its
// 4-byte start code, in stream order.
func readClipNALs(tb testing.TB) [][]byte {
	tb.Helper()
	data, err := os.ReadFile("testdata/clip.h264")
	if err != nil {
		tb.Fatal(err)
	}
	return decodertest.SplitNALs(data)
}

func newTestDecoder(tb testing.TB) *H264Decoder {
	tb.Helper()
	d, err := NewH264Decoder()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(d.Close)
	return d
}

// Feeding the clip NAL by NAL yields every frame, as RGBA with the clip's size.
func TestDecodeClip(t *testing.T) {
	t.Parallel()

	d := newTestDecoder(t)
	frames := 0
	for _, nal := range readClipNALs(t) {
		img, err := d.DecodeFrame(nal)
		if err != nil {
			continue // SEI before SPS is rejected; the client logs and skips these too
		}
		if img == nil {
			continue
		}
		frames++
		if b := img.Bounds(); b.Dx() != 320 || b.Dy() != 240 {
			t.Fatalf("frame %d is %v, want 320x240", frames, b)
		}
		// testsrc has no transparency; RGBA must be opaque.
		for y := range 240 {
			row := img.Pix[y*img.Stride : y*img.Stride+320*4]
			for i := 3; i < len(row); i += 4 {
				if row[i] != 255 {
					t.Fatalf("frame %d: alpha %d at row %d, byte %d", frames, row[i], y, i)
				}
			}
		}
	}
	if frames != 20 {
		t.Fatalf("decoded %d frames, want 20", frames)
	}
}

// Close is idempotent, safe on a nil decoder, and decoding afterwards fails
// cleanly instead of touching freed C memory.
func TestDecoderCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	d, err := NewH264Decoder()
	if err != nil {
		t.Fatal(err)
	}
	nals := readClipNALs(t)
	for _, nal := range nals[:5] {
		_, _ = d.DecodeFrame(nal)
	}
	d.Close()
	d.Close()
	if _, err := d.DecodeFrame(nals[5]); err == nil {
		t.Fatal("DecodeFrame after Close returned no error")
	}

	var nilDecoder *H264Decoder
	nilDecoder.Close()
}

func BenchmarkDecodeClip(b *testing.B) {
	nals := readClipNALs(b)
	b.ReportAllocs()
	for b.Loop() {
		d, err := NewH264Decoder()
		if err != nil {
			b.Fatal(err)
		}
		for _, nal := range nals {
			_, _ = d.DecodeFrame(nal)
		}
		d.Close()
	}
}

// Frames come out at the requested size, also when it changes mid-stream.
func TestDecodeScalesToOutputSize(t *testing.T) {
	t.Parallel()

	d := newTestDecoder(t)
	sizes := [][2]int{{80, 48}, {33, 17}, {0, 0}}
	frame := 0
	for _, nal := range readClipNALs(t) {
		want := sizes[min(frame/7, len(sizes)-1)]
		d.SetOutputSize(want[0], want[1])
		img, err := d.DecodeFrame(nal)
		if err != nil || img == nil {
			continue
		}
		if want == [2]int{0, 0} {
			want = [2]int{320, 240}
		}
		if b := img.Bounds(); b.Dx() != want[0] || b.Dy() != want[1] {
			t.Fatalf("frame %d is %dx%d, want %dx%d", frame, b.Dx(), b.Dy(), want[0], want[1])
		}
		frame++
	}
	if frame != 20 {
		t.Fatalf("decoded %d frames, want 20", frame)
	}
}

func TestNewPaddedRGBALeavesRoomAfterEachRow(t *testing.T) {
	t.Parallel()
	for _, w := range []int{1, 13, 16, 97, 1920} {
		img := newPaddedRGBA(w, 3)
		if img.Stride%64 != 0 || img.Stride-4*w < rowPadding {
			t.Errorf("width %d: stride %d, want a multiple of 64 with %d bytes after the row", w, img.Stride, rowPadding)
		}
		if len(img.Pix) != 3*img.Stride {
			t.Errorf("width %d: %d bytes, want %d", w, len(img.Pix), 3*img.Stride)
		}
	}
}
