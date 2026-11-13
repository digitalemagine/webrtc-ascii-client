package h264rtp

import (
	"bytes"
	"io"
)

// NAL unit types (H.264 Table 7-1).
const (
	nalIDR = 5
	nalSPS = 7
	nalPPS = 8
)

// startCode prefixes every NAL unit the Writer adds.
var startCode = []byte{0, 0, 0, 1}

// Writer writes access units as an H.264 Annex-B byte stream that a decoder
// can start on: access units before the first IDR picture are dropped, and
// if that picture's access unit carries no SPS or PPS, the latest ones seen
// are written before it. After that, access units are written as they come.
//
// Not safe for concurrent use.
type Writer struct {
	w        io.Writer
	started  bool
	sps, pps []byte // latest parameter sets before the start, with start codes
	buf      []byte
}

// NewWriter returns a Writer to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Started reports whether the first IDR picture has been written.
func (w *Writer) Started() bool { return w.started }

// WriteAccessUnit writes one Annex-B access unit, with one Write call to the
// underlying writer, or drops it while waiting for the first IDR picture.
func (w *Writer) WriteAccessUnit(au []byte) error {
	if w.started {
		_, err := w.w.Write(au)
		return err
	}

	var idr, hasSPS, hasPPS bool
	for nal := range nalUnits(au) {
		switch nal[0] & 0x1f {
		case nalIDR:
			idr = true
		case nalSPS:
			hasSPS = true
			w.sps = append(append(w.sps[:0], startCode...), nal...)
		case nalPPS:
			hasPPS = true
			w.pps = append(append(w.pps[:0], startCode...), nal...)
		}
	}
	if !idr {
		return nil
	}
	w.started = true
	out := au
	if !hasSPS || !hasPPS {
		w.buf = w.buf[:0]
		if !hasSPS {
			w.buf = append(w.buf, w.sps...)
		}
		if !hasPPS {
			w.buf = append(w.buf, w.pps...)
		}
		w.buf = append(w.buf, au...)
		out = w.buf
	}
	_, err := w.w.Write(out)
	return err
}

// nalUnits yields the NAL units of an Annex-B buffer without their start
// codes. Emulation prevention guarantees that 0x000001 occurs only as a
// start code; zero bytes before a start code (4-byte start codes,
// trailing_zero_8bits) are trimmed.
func nalUnits(au []byte) func(yield func([]byte) bool) {
	return func(yield func([]byte) bool) {
		for part := range bytes.SplitSeq(au, []byte{0, 0, 1}) {
			part = bytes.TrimRight(part, "\x00")
			if len(part) > 0 && !yield(part) {
				return
			}
		}
	}
}
