package decoder

import (
	"image"
	"os"
	"testing"

	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder/decodertest"
	"github.com/pion/rtp"
)

func clipPackets(tb testing.TB, name string) []*rtp.Packet {
	tb.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		tb.Fatal(err)
	}
	return decodertest.Packets(data, 15)
}

// countFrames counts the frames it receives, at the decoder's size.
type countFrames struct{ n int }

func (c *countFrames) Frame(*image.RGBA)    { c.n++ }
func (*countFrames) TargetSize() (int, int) { return 0, 0 }

// playPackets pushes packets through a new stream and returns the frame count
// and whether a keyframe was requested.
func playPackets(tb testing.TB, packets []*rtp.Packet) (frames int, keyframeRequested bool) {
	tb.Helper()
	var sink countFrames
	s, err := NewH264Stream(&sink, 0)
	if err != nil {
		tb.Fatal(err)
	}
	defer s.Close()
	for _, pkt := range packets {
		if s.Push(pkt) {
			keyframeRequested = true
		}
	}
	return sink.n, keyframeRequested
}

func TestH264StreamClip(t *testing.T) {
	t.Parallel()

	frames, keyframe := playPackets(t, clipPackets(t, "clip.h264"))
	if frames != 20 || keyframe {
		t.Fatalf("got %d frames (keyframe requested: %v), want 20 and no request", frames, keyframe)
	}
}

// Frames made of several slices are decoded once per frame, not once per
// slice (before: 60 partial frames for this 20-frame clip).
func TestH264StreamMultiSliceFrames(t *testing.T) {
	t.Parallel()

	frames, keyframe := playPackets(t, clipPackets(t, "clip-slices.h264"))
	if frames != 20 || keyframe {
		t.Fatalf("got %d frames (keyframe requested: %v), want 20 and no request", frames, keyframe)
	}
}

// Packets arriving out of order are put back in order.
func TestH264StreamReorderedPackets(t *testing.T) {
	t.Parallel()

	packets := clipPackets(t, "clip-slices.h264")
	for i := 1; i+1 < len(packets); i += 4 {
		packets[i], packets[i+1] = packets[i+1], packets[i]
	}
	frames, keyframe := playPackets(t, packets)
	if frames != 20 || keyframe {
		t.Fatalf("got %d frames (keyframe requested: %v), want 20 and no request", frames, keyframe)
	}
}

// A lost packet triggers a keyframe request. (The frame itself may still be
// shown: FFmpeg conceals a missing slice.)
func TestH264StreamLostPacketRequestsKeyframe(t *testing.T) {
	t.Parallel()

	packets := clipPackets(t, "clip-slices.h264")
	lost := len(packets) / 2
	packets = append(packets[:lost:lost], packets[lost+1:]...)
	if _, keyframe := playPackets(t, packets); !keyframe {
		t.Error("no keyframe requested after packet loss")
	}
}

// FuzzH264Stream pushes two arbitrary RTP payloads of one frame, then a packet
// of the next frame to release it. This covers everything that parses network
// input: pion's H.264 depacketizer and sample builder, and FFmpeg's decoder.
func FuzzH264Stream(f *testing.F) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F}
	pps := []byte{0x68, 0xEE, 0x06, 0xF2}
	stapA := []byte{24, 0, byte(len(sps))}
	stapA = append(append(stapA, sps...), 0, byte(len(pps)))
	stapA = append(stapA, pps...)
	f.Add([]byte{0x61, 0xAA}, []byte{})
	f.Add(stapA, []byte{0x65, 0x88, 0x84})
	f.Add([]byte{0x7C, 0x85, 0x01}, []byte{0x7C, 0x45, 0x02}) // FU-A start, end
	f.Add([]byte{24, 0x00, 0x00}, []byte{})                   // empty STAP-A unit

	s, err := NewH264Stream(&countFrames{}, 0)
	if err != nil {
		f.Fatal(err)
	}
	defer s.Close()
	var seq uint16
	var ts uint32
	f.Fuzz(func(t *testing.T, a, b []byte) {
		push := func(payload []byte, marker bool) {
			seq++
			s.Push(&rtp.Packet{
				Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: marker,
				Payload: payload,
			})
		}
		push(a, false)
		push(b, true)
		ts += 3000
		push([]byte{0x09, 0xf0}, true) // next frame: releases this one
	})
}
