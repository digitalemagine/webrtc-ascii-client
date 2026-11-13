package decoder

import (
	"image"
	"log/slog"

	"github.com/digitalemagine/webrtc-ascii-client/internal/h264rtp"
	"github.com/pion/rtp"
)

// FrameSink receives decoded frames. Its methods are called from the
// goroutine that pushes packets.
type FrameSink interface {
	// Frame receives a decoded frame of TargetSize pixels.
	Frame(img *image.RGBA)
	// TargetSize is the size to scale frames to; it may change at any time.
	TargetSize() (width, height int)
}

// H264Stream turns the RTP packets of one H.264 track into decoded frames:
// h264rtp.Assembler builds access units, which are decoded and scaled to the
// sink's target size.
//
// Not safe for concurrent use: one goroutine owns it, pushes to it and closes it.
type H264Stream struct {
	assembler  *h264rtp.Assembler
	decoder    *H264Decoder
	sink       FrameSink
	outW, outH int
}

// NewH264Stream creates a stream with its own decoder, sending frames to sink.
func NewH264Stream(sink FrameSink, verbosity int) (*H264Stream, error) {
	dec, err := NewH264DecoderWithVerbosity(verbosity)
	if err != nil {
		return nil, err
	}
	return &H264Stream{assembler: h264rtp.NewAssembler(), decoder: dec, sink: sink}, nil
}

// Push adds one RTP packet and sends every frame it completes to the sink.
// It returns true when a keyframe should be requested from the sender: an
// access unit was lost, or one failed to decode, so the picture is broken
// until the next keyframe.
func (s *H264Stream) Push(pkt *rtp.Packet) (needKeyframe bool) {
	if w, h := s.sink.TargetSize(); w != s.outW || h != s.outH {
		s.outW, s.outH = w, h
		s.decoder.SetOutputSize(w, h)
	}
	lost := s.assembler.Push(pkt, func(au []byte) {
		img, err := s.decoder.DecodeFrame(au)
		if err != nil {
			slog.Debug("H.264: decode error", "error", err)
			needKeyframe = true
			return
		}
		if img != nil {
			s.sink.Frame(img)
		}
	})
	return needKeyframe || lost
}

// Close frees the decoder. It is idempotent.
func (s *H264Stream) Close() {
	s.decoder.Close()
}
