package client

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters/janustest"
	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder/decodertest"
	"github.com/digitalemagine/webrtc-ascii-client/internal/h264rtp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func newTestClient(t *testing.T) *WebRTCClient {
	t.Helper()
	c, err := NewWebRTCClient("simple", "ws://127.0.0.1:1/ws", "test")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeOutput records the last status and gives each video track an
// assembler that counts access units.
type fakeOutput struct {
	units  atomic.Int64
	misuse atomic.Bool // a Track was used after Close
	closed atomic.Int64
	mu     sync.Mutex
	status string
}

func (o *fakeOutput) Status(s string) { o.mu.Lock(); o.status = s; o.mu.Unlock() }

func (o *fakeOutput) NewTrack() (Track, error) {
	return &countingTrack{out: o, assembler: h264rtp.NewAssembler()}, nil
}

type countingTrack struct {
	out       *fakeOutput
	assembler *h264rtp.Assembler
	closed    bool
}

func (t *countingTrack) Push(pkt *rtp.Packet) bool {
	if t.closed {
		t.out.misuse.Store(true)
	}
	return t.assembler.Push(pkt, func([]byte) { t.out.units.Add(1) })
}

func (t *countingTrack) Close() {
	if t.closed {
		t.out.misuse.Store(true)
	}
	t.closed = true
	t.out.closed.Add(1)
}

// An unknown adapter type is an error for the caller, not a process exit.
func TestNewWebRTCClientRejectsUnknownAdapter(t *testing.T) {
	t.Parallel()

	c, err := NewWebRTCClient("carrier-pigeon", "ws://127.0.0.1:1/ws", "test")
	if err == nil || c != nil {
		t.Fatalf("got client %v, error %v; want an error", c, err)
	}
}

// fakeTrack plays the decoder's test clip as RTP in a loop until closed,
// like a TrackRemote until its peer connection is closed.
type fakeTrack struct {
	units      [][]byte
	packetizer rtp.Packetizer
	queue      []*rtp.Packet
	next       int
	closed     chan struct{}
	once       sync.Once
}

func newFakeTrack(t *testing.T) *fakeTrack {
	t.Helper()
	data, err := os.ReadFile("../decoder/testdata/clip.h264")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeTrack{
		units:      decodertest.AccessUnits(data),
		packetizer: decodertest.NewPacketizer(),
		closed:     make(chan struct{}),
	}
}

func (f *fakeTrack) read() (*rtp.Packet, error) {
	select {
	case <-f.closed:
		return nil, io.EOF
	default:
	}
	if len(f.queue) == 0 {
		f.queue = f.packetizer.Packetize(f.units[f.next%len(f.units)], decodertest.ClockRate/15)
		f.next++
	}
	p := f.queue[0]
	f.queue = f.queue[1:]
	return p, nil
}

func (f *fakeTrack) close() { f.once.Do(func() { close(f.closed) }) }

var h264Codec = webrtc.RTPCodecParameters{MimeType: webrtc.MimeTypeH264}

// Shutting down while packets flow must not close the Track under the track
// handler, which may be inside the decoder at that moment: the handler owns
// its Track, closes it once, and shutdown waits for it.
func TestShutdownWhileReceiving(t *testing.T) {
	t.Parallel()

	for range 5 {
		c := newTestClient(t)
		out := &fakeOutput{}
		c.SetOutput(out)

		track := newFakeTrack(t)
		go c.runTrack(func(ctx context.Context) { c.handleVideoTrack(ctx, h264Codec, track.read, func() {}) })

		// Let some frames through, then shut down as Connect does.
		waitFor(t, "an access unit", func() bool { return out.units.Load() > 0 })

		c.stopTracks()
		track.close() // as closing the peer connection ends ReadRTP

		done := make(chan struct{})
		go func() { c.tracks.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("track handler did not stop")
		}
		if out.closed.Load() != 1 || out.misuse.Load() {
			t.Fatalf("Track closed %d times, used after Close: %v; want closed once, not used after",
				out.closed.Load(), out.misuse.Load())
		}
	}
}

// A track that arrives after shutdown started is not handled.
func TestRunTrackAfterStop(t *testing.T) {
	t.Parallel()

	c := newTestClient(t)
	c.stopTracks()

	ran := false
	c.runTrack(func(context.Context) { ran = true })
	if ran {
		t.Fatal("track handler ran after stopTracks")
	}
}

// Canceling Connect's context (Ctrl-C, quit key) must run the cleanup, so
// no Janus session is left behind on the server.
func TestConnectCancelDestroysJanusSession(t *testing.T) {
	t.Parallel()

	srv := janustest.NewServer(t)
	c, err := NewWebRTCClient("janus", srv.URL, "1")
	if err != nil {
		t.Fatal(err)
	}
	c.SetSTUNServers(nil)
	c.SetOutput(&fakeOutput{})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()

	waitFor(t, "watch request", func() bool { return slices.Contains(srv.Received(), "message") })
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Connect returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Connect did not return after cancel")
	}
	waitFor(t, "destroy", func() bool { return slices.Contains(srv.Received(), "destroy") })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The track handler asks for a keyframe as soon as the track starts, so the
// picture appears without waiting for the sender's next scheduled keyframe.
func TestTrackStartRequestsKeyframe(t *testing.T) {
	t.Parallel()

	c := newTestClient(t)
	track := newFakeTrack(t)
	var requests atomic.Int32
	done := make(chan struct{})
	go func() {
		c.runTrack(func(ctx context.Context) {
			c.handleVideoTrack(ctx, h264Codec, track.read, func() { requests.Add(1) })
		})
		close(done)
	}()
	waitFor(t, "keyframe request", func() bool { return requests.Load() > 0 })
	c.stopTracks()
	track.close()
	<-done
	if n := requests.Load(); n != 1 {
		t.Errorf("%d keyframe requests for a clean stream, want 1", n)
	}
}

// End to end on localhost: signaling with the mock Janus, ICE, DTLS/SRTP,
// RTP and sample building. The mock's track plays the test clip; access
// units must reach the output's Track.
func TestConnectReceivesVideoEndToEnd(t *testing.T) {
	t.Parallel()

	srv := janustest.NewServer(t)
	c, err := NewWebRTCClient("janus", srv.URL, "1")
	if err != nil {
		t.Fatal(err)
	}
	c.SetSTUNServers(nil)
	out := &fakeOutput{}
	c.SetOutput(out)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Connect(ctx) }()
	defer func() {
		cancel()
		_ = <-done
	}()

	select {
	case <-srv.Answered():
	case cerr := <-done:
		t.Fatalf("Connect returned: %v", cerr)
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}

	data, err := os.ReadFile("../decoder/testdata/clip.h264")
	if err != nil {
		t.Fatal(err)
	}
	units := decodertest.AccessUnits(data)
	track := srv.Track()
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; out.units.Load() < 5; i++ {
		if time.Now().After(deadline) {
			t.Fatalf("only %d access units after 10 s", out.units.Load())
		}
		if err := track.WriteSample(media.Sample{Data: units[i%len(units)], Duration: time.Second / 15}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second / 15)
	}
}
