package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/internal/client"
	"github.com/digitalemagine/webrtc-ascii-client/internal/h264rtp"
	"github.com/pion/rtp"
)

// errEndOfStream ends receiving when the video track ends.
var errEndOfStream = errors.New("end of stream")

// shutdownTimeout bounds the wait for the signaling cleanup once receiving
// ends, so an unresponsive server can't hang the command.
const shutdownTimeout = 3 * time.Second

// receive connects c and writes its first video track to w as H.264
// Annex-B until the track ends, writing fails, the connection fails, or ctx
// is canceled. A reader that goes away (EPIPE, e.g. "| head -c 1M"), the
// end of the track and cancellation are a normal end: they return nil.
func receive(ctx context.Context, c *client.WebRTCClient, w io.Writer) error {
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	c.SetOutput(&output{writer: h264rtp.NewWriter(w), stop: stop})

	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		select {
		case err = <-done:
		case <-time.After(shutdownTimeout):
			slog.Warn("Shutdown timed out; exiting anyway")
		}
	}

	// A failed connection also ends the track, so its error comes first.
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errEndOfStream):
		slog.Info("Video track ended")
	case errors.Is(cause, syscall.EPIPE):
		slog.Info("Output closed by the reader")
	case cause != nil && !errors.Is(cause, context.Canceled):
		return cause
	}
	return nil
}

// output gives the client's first video track a writer; further tracks are
// discarded, since one Annex-B stream can't carry two.
type output struct {
	writer  *h264rtp.Writer
	stop    context.CancelCauseFunc
	claimed atomic.Bool
}

func (o *output) Status(status string) { slog.Debug("Status", "status", status) }

func (o *output) NewTrack() (client.Track, error) {
	if !o.claimed.CompareAndSwap(false, true) {
		slog.Warn("Ignoring an additional video track: only the first one is written")
		return discardTrack{}, nil
	}
	return &track{output: o, assembler: h264rtp.NewAssembler()}, nil
}

// track writes access units as they are completed.
type track struct {
	*output
	assembler *h264rtp.Assembler
	failed    bool
}

// Push returns true (request a keyframe) after loss, and until the first
// keyframe has been written: nothing is written before it, so a keyframe
// that was asked for at the start but never came is asked for again.
func (t *track) Push(pkt *rtp.Packet) (needKeyframe bool) {
	if t.failed {
		return false
	}
	lost := t.assembler.Push(pkt, func(au []byte) {
		if t.failed {
			return
		}
		started := t.writer.Started()
		if err := t.writer.WriteAccessUnit(au); err != nil {
			t.failed = true
			t.stop(fmt.Errorf("writing the stream: %w", err))
			return
		}
		if !started && t.writer.Started() {
			slog.Info("Keyframe received, writing the stream")
		}
	})
	return lost || !t.writer.Started()
}

// Close ends receiving. On shutdown the context is already canceled and
// this changes nothing.
func (t *track) Close() { t.stop(errEndOfStream) }

type discardTrack struct{}

func (discardTrack) Push(*rtp.Packet) bool { return false }
func (discardTrack) Close()                {}
