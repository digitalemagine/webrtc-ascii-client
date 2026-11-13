package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters/janustest"
	"github.com/digitalemagine/webrtc-ascii-client/internal/client"
	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder/decodertest"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// syncBuffer is a bytes.Buffer safe for one writer and concurrent readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.buf.Bytes())
}

// startReceiving connects to a new mock Janus and receives into w. It
// returns the server, its video track once answered, and receive's result.
func startReceiving(t *testing.T, ctx context.Context, w io.Writer) (
	*janustest.Server, *webrtc.TrackLocalStaticSample, <-chan error,
) {
	t.Helper()
	srv := janustest.NewServer(t)
	c, err := client.NewWebRTCClient("janus", srv.URL, "1")
	if err != nil {
		t.Fatal(err)
	}
	c.SetSTUNServers(nil)
	done := make(chan error, 1)
	go func() { done <- receive(ctx, c, w) }()
	select {
	case <-srv.Answered():
	case err := <-done:
		t.Fatalf("receive returned: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
	return srv, srv.Track(), done
}

func waitForDestroy(t *testing.T, srv *janustest.Server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !slices.Contains(srv.Received(), "destroy") {
		if time.Now().After(deadline) {
			t.Fatal("Janus session not destroyed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func clipUnits(t *testing.T) [][]byte {
	t.Helper()
	data, err := os.ReadFile("../../internal/decoder/testdata/clip.h264")
	if err != nil {
		t.Fatal(err)
	}
	return decodertest.AccessUnits(data)
}

// sendClip sends the clip's frames once; the first one is its only keyframe.
func sendClip(t *testing.T, track *webrtc.TrackLocalStaticSample, units [][]byte) {
	t.Helper()
	for _, au := range units {
		if err := track.WriteSample(media.Sample{Data: au, Duration: time.Second / 15}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// End to end on localhost: mock Janus → ICE/DTLS/SRTP → Annex-B. The output
// starts at a keyframe and ffmpeg decodes all of it without errors.
func TestReceiveWritesDecodableStream(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out syncBuffer
	srv, track, done := startReceiving(t, ctx, &out)
	units := clipUnits(t)

	// Packets sent before ICE completes are lost: repeat the clip until
	// the output has started, then send it once more in full.
	deadline := time.Now().Add(10 * time.Second)
	for len(out.Bytes()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no output after 10 s")
		}
		sendClip(t, track, units)
	}
	sendClip(t, track, units)
	sendClip(t, track, units[:1]) // releases the last frame of the clip
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("receive: %v", err)
	}
	waitForDestroy(t, srv)

	data := out.Bytes()
	nals := decodertest.SplitNALs(data)
	if len(nals) == 0 || nals[0][4]&0x1f != 7 {
		t.Fatalf("output does not start with an SPS: % x", data[:min(len(data), 16)])
	}

	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not found; output not decoded")
	}
	path := filepath.Join(t.TempDir(), "out.h264")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ffprobe, "-v", "error", "-f", "h264", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("ffprobe: %v\n%s", err, stderr.String())
	}
	frames, err := strconv.Atoi(strings.TrimSpace(string(stdout)))
	if err != nil || frames < len(units) {
		t.Fatalf("ffprobe decoded %q frames, want at least %d", stdout, len(units))
	}
}

// When the reader goes away (EPIPE), receiving ends normally and the
// signaling session is cleaned up.
func TestReceiveReaderGoneCleansUp(t *testing.T) {
	t.Parallel()

	srv, track, done := startReceiving(t, t.Context(), writerFunc(func([]byte) (int, error) {
		return 0, syscall.EPIPE
	}))
	units := clipUnits(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("receive: %v, want nil", err)
			}
			waitForDestroy(t, srv)
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("receive did not return after EPIPE")
		}
		sendClip(t, track, units)
	}
}
