package h264rtp

import (
	"bytes"
	"errors"
	"testing"

	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder/decodertest"
)

// nal builds an Annex-B NAL unit of the given type with a 4-byte start code.
func nal(typ byte, payload ...byte) []byte {
	return append([]byte{0, 0, 0, 1, 0x60 | typ}, payload...)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

var (
	sps   = nal(nalSPS, 0x64, 0x00, 0x1f)
	pps   = nal(nalPPS, 0xee, 0x06)
	idr   = nal(nalIDR, 0x88, 0x84)
	slice = nal(1, 0x9a, 0x00)
)

func TestWriterStartsAtKeyframe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		units [][]byte
		want  []byte
	}{
		{
			name:  "parameter sets with the IDR",
			units: [][]byte{slice, cat(sps, pps, idr), slice},
			want:  cat(sps, pps, idr, slice),
		},
		{
			name:  "parameter sets earlier, in their own access unit",
			units: [][]byte{cat(sps, pps), slice, idr, slice},
			want:  cat(sps, pps, idr, slice),
		},
		{
			name:  "only the latest parameter sets",
			units: [][]byte{cat(nal(nalSPS, 1), nal(nalPPS, 1)), cat(sps, pps), cat(idr), slice},
			want:  cat(sps, pps, idr, slice),
		},
		{
			name:  "PPS with the IDR, SPS earlier",
			units: [][]byte{sps, cat(pps, idr)},
			want:  cat(sps, pps, idr),
		},
		{
			name:  "parameter sets after the start are passed through",
			units: [][]byte{cat(sps, pps, idr), slice, cat(sps, pps, idr)},
			want:  cat(sps, pps, idr, slice, sps, pps, idr),
		},
		{
			name:  "no IDR, no output",
			units: [][]byte{cat(sps, pps), slice, slice},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			w := NewWriter(&out)
			for _, au := range tt.units {
				if err := w.WriteAccessUnit(au); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(out.Bytes(), tt.want) {
				t.Errorf("wrote % x\nwant  % x", out.Bytes(), tt.want)
			}
			if w.Started() != (tt.want != nil) {
				t.Errorf("Started() = %v", w.Started())
			}
		})
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestWriterReturnsWriteErrors(t *testing.T) {
	t.Parallel()

	errBroken := errors.New("broken pipe")
	w := NewWriter(failWriter{errBroken})
	if err := w.WriteAccessUnit(slice); err != nil {
		t.Fatalf("dropped access unit: %v, want no error", err)
	}
	if err := w.WriteAccessUnit(cat(sps, pps, idr)); !errors.Is(err, errBroken) {
		t.Fatalf("got %v, want %v", err, errBroken)
	}
	if err := w.WriteAccessUnit(slice); !errors.Is(err, errBroken) {
		t.Fatalf("got %v, want %v", err, errBroken)
	}
}

// The test clip through the assembler and the writer comes out whole: it
// starts with an IDR, so nothing is dropped.
func TestWriterClip(t *testing.T) {
	t.Parallel()

	data := readClip(t, "clip-slices.h264")
	var out bytes.Buffer
	w := NewWriter(&out)
	a := NewAssembler()
	for _, pkt := range decodertest.Packets(data, 15) {
		a.Push(pkt, func(au []byte) {
			if err := w.WriteAccessUnit(au); err != nil {
				t.Fatal(err)
			}
		})
	}
	if got, want := nalTypes(out.Bytes()), nalTypes(data); !bytes.Equal(got, want) {
		t.Errorf("NAL types %v\nwant      %v", got, want)
	}
}
