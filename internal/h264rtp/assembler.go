// Package h264rtp turns the RTP packets of an H.264 track into access units
// (one complete frame each) in Annex-B format. It is pure Go: decoding is
// left to the caller.
package h264rtp

import (
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

const (
	// ClockRate is the RTP clock rate of H.264 (RFC 6184).
	ClockRate = 90000

	// maxLatePackets is how many packets the sample builder waits for a
	// missing one before giving up on its access unit. It must exceed the
	// packet count of the largest frame (a 1080p keyframe can be a few hundred).
	maxLatePackets = 512

	// maxLateDelay bounds the same wait in time.
	maxLateDelay = 500 * time.Millisecond
)

// Assembler reorders packets, drops incomplete access units after loss, and
// joins the NAL units of each access unit (all slices of a frame, plus
// SPS/PPS) into one Annex-B buffer, using pion's samplebuilder with
// codecs.H264Packet.
//
// An access unit is released when the first packet of the next one arrives,
// so output lags input by at most one frame interval.
//
// Not safe for concurrent use.
type Assembler struct {
	builder *samplebuilder.SampleBuilder
}

// NewAssembler returns an assembler for one track.
func NewAssembler() *Assembler {
	return &Assembler{
		builder: samplebuilder.New(maxLatePackets, &codecs.H264Packet{}, ClockRate,
			samplebuilder.WithMaxTimeDelay(maxLateDelay)),
	}
}

// Push adds one RTP packet and calls emit for every access unit it completes,
// in order. emit owns the buffer it is given. Push returns true when packets
// were lost before one of those access units: the picture is broken until the
// next keyframe, so the caller should ask the sender for one.
func (a *Assembler) Push(pkt *rtp.Packet, emit func(au []byte)) (lost bool) {
	a.builder.Push(pkt)
	for sample := a.builder.Pop(); sample != nil; sample = a.builder.Pop() {
		if sample.PrevDroppedPackets > 0 {
			lost = true
		}
		emit(sample.Data)
	}
	return lost
}
