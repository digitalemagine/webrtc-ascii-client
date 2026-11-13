package h264rtp

import (
	"bytes"
	"os"
	"testing"

	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder/decodertest"
	"github.com/pion/rtp"
)

func readClip(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := os.ReadFile("../decoder/testdata/" + name)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// assemble pushes packets through a new assembler and returns the access
// units and whether a loss was reported.
func assemble(packets []*rtp.Packet) (units [][]byte, lost bool) {
	a := NewAssembler()
	for _, pkt := range packets {
		if a.Push(pkt, func(au []byte) { units = append(units, au) }) {
			lost = true
		}
	}
	return units, lost
}

// The access units come out as they went in: same NAL units, one per frame.
func TestAssemblerClip(t *testing.T) {
	t.Parallel()

	for _, clip := range []string{"clip.h264", "clip-slices.h264"} {
		t.Run(clip, func(t *testing.T) {
			t.Parallel()

			data := readClip(t, clip)
			units, lost := assemble(decodertest.Packets(data, 15))
			if lost {
				t.Error("loss reported for a complete stream")
			}
			want := decodertest.AccessUnits(data)
			if len(units) != len(want) {
				t.Fatalf("got %d access units, want %d", len(units), len(want))
			}
			for i := range want {
				// The payloader drops access unit delimiters and the
				// depacketizer writes 4-byte start codes: compare NAL units.
				if got, w := nalTypes(units[i]), nalTypes(want[i]); !bytes.Equal(got, w) {
					t.Errorf("access unit %d: NAL types %v, want %v", i, got, w)
				}
			}
		})
	}
}

// nalTypes lists the NAL unit types of an Annex-B buffer, without delimiters.
func nalTypes(au []byte) []byte {
	var types []byte
	for _, nal := range decodertest.SplitNALs(au) {
		if typ := nal[4] & 0x1f; typ != 9 {
			types = append(types, typ)
		}
	}
	return types
}

// Packets arriving out of order are put back in order.
func TestAssemblerReorderedPackets(t *testing.T) {
	t.Parallel()

	packets := decodertest.Packets(readClip(t, "clip-slices.h264"), 15)
	for i := 1; i+1 < len(packets); i += 4 {
		packets[i], packets[i+1] = packets[i+1], packets[i]
	}
	if units, lost := assemble(packets); len(units) != 20 || lost {
		t.Fatalf("got %d access units (loss reported: %v), want 20 and no loss", len(units), lost)
	}
}

// A lost packet is reported. (What remains of its access unit may still be
// emitted: a decoder can conceal a missing slice.)
func TestAssemblerLostPacket(t *testing.T) {
	t.Parallel()

	packets := decodertest.Packets(readClip(t, "clip-slices.h264"), 15)
	lostAt := len(packets) / 2
	packets = append(packets[:lostAt:lostAt], packets[lostAt+1:]...)
	if _, lost := assemble(packets); !lost {
		t.Error("loss not reported")
	}
}

// FuzzAssembler pushes two arbitrary RTP payloads of one frame, then a packet
// of the next frame to release it: pion's depacketizer and sample builder
// parse network input.
func FuzzAssembler(f *testing.F) {
	f.Add([]byte{0x61, 0xAA}, []byte{})
	f.Add([]byte{0x7C, 0x85, 0x01}, []byte{0x7C, 0x45, 0x02}) // FU-A start, end
	f.Add([]byte{24, 0x00, 0x00}, []byte{})                   // empty STAP-A unit

	a := NewAssembler()
	var seq uint16
	var ts uint32
	f.Fuzz(func(t *testing.T, p1, p2 []byte) {
		push := func(payload []byte, marker bool) {
			seq++
			a.Push(&rtp.Packet{
				Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: marker,
				Payload: payload,
			}, func([]byte) {})
		}
		push(p1, false)
		push(p2, true)
		ts += 3000
		push([]byte{0x09, 0xf0}, true)
	})
}
