// Package decodertest has helpers for tests that feed H.264 to the decoder:
// splitting an Annex-B clip into access units and packetizing them as RTP.
package decodertest

import (
	"bytes"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// ClockRate is the RTP clock rate of H.264.
const ClockRate = 90000

// SplitNALs splits an Annex-B byte stream at its start codes and returns each
// NAL unit prefixed with a 4-byte start code.
func SplitNALs(data []byte) [][]byte {
	var nals [][]byte
	for part := range bytes.SplitSeq(data, []byte{0, 0, 1}) {
		part = bytes.TrimRight(part, "\x00") // 4-byte start codes leave a trailing zero
		if len(part) == 0 {
			continue
		}
		nals = append(nals, append([]byte{0, 0, 0, 1}, part...))
	}
	return nals
}

// AccessUnits splits an Annex-B stream into access units (frames). A new
// access unit starts at a parameter set, SEI or delimiter that follows a
// slice, or at a slice with first_mb_in_slice == 0 that follows a slice.
func AccessUnits(data []byte) [][]byte {
	var units [][]byte
	var cur []byte
	sawSlice := false
	for _, nal := range SplitNALs(data) {
		typ := nal[4] & 0x1f
		isSlice := typ == 1 || typ == 5
		firstSlice := isSlice && len(nal) > 5 && nal[5]&0x80 != 0 // ue(v) 0 is the single bit 1
		if sawSlice && (!isSlice || firstSlice) {
			units = append(units, cur)
			cur, sawSlice = nil, false
		}
		cur = append(cur, nal...)
		sawSlice = sawSlice || isSlice
	}
	return append(units, cur)
}

// NewPacketizer returns an RTP packetizer as a WebRTC sender would use for
// H.264: MTU 1200, payload type 96, marker on the last packet of each frame.
func NewPacketizer() rtp.Packetizer {
	return rtp.NewPacketizer(1200, 96, 0x1234, &codecs.H264Payloader{}, rtp.NewFixedSequencer(1), ClockRate)
}

// Packets packetizes the access units of an Annex-B clip, one RTP timestamp
// per frame at fps. A final access unit delimiter with the next timestamp
// releases the last frame from a sample builder.
func Packets(data []byte, fps int) []*rtp.Packet {
	p := NewPacketizer()
	var packets []*rtp.Packet
	for _, au := range AccessUnits(data) {
		packets = append(packets, p.Packetize(au, uint32(ClockRate/fps))...)
	}
	// The payloader drops access unit delimiters, so build that packet by hand.
	last := packets[len(packets)-1].Header
	last.SequenceNumber++
	last.Timestamp += uint32(ClockRate / fps)
	return append(packets, &rtp.Packet{Header: last, Payload: []byte{0x09, 0xf0}})
}
