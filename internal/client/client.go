package client

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

const (
	// Connection monitoring interval
	connectionMonitorInterval = 2 * time.Second
)

// DefaultSTUNServers are used unless SetSTUNServers is called.
var DefaultSTUNServers = []string{"stun:stun.l.google.com:19302"}

type peerConnectionStateProvider interface {
	ConnectionState() webrtc.PeerConnectionState
}

// WebRTCClient represents a WebRTC client that can connect to various signaling servers
type WebRTCClient struct {
	session        adapters.Session
	peerConnection *webrtc.PeerConnection
	stunServers    []string

	// output receives status and video tracks; set before Connect.
	output Output

	statusMu sync.RWMutex
	status   string

	// Track handlers run on pion's OnTrack goroutines. Each one owns its
	// Track. stopTracks refuses new handlers and cancels tracksCtx; the
	// handlers are then waited for after the peer connection is closed.
	tracksMu      sync.Mutex
	tracksStopped bool
	tracks        sync.WaitGroup
	tracksCtx     context.Context
	cancelTracks  context.CancelFunc
}

// Output receives what the client produces. Its methods are called from
// pion's goroutines and must be safe for concurrent use.
type Output interface {
	// Status receives the connection state, e.g. "ICE connected".
	Status(status string)
	// NewTrack returns the consumer of a new H.264 video track.
	NewTrack() (Track, error)
}

// Track consumes the RTP packets of one video track, e.g. by decoding them
// (decoder.H264Stream). One goroutine owns it: its methods are never called
// concurrently.
type Track interface {
	// Push takes one RTP packet. It returns true when a keyframe should be
	// requested from the sender, e.g. after loss or a decode error.
	Push(pkt *rtp.Packet) (needKeyframe bool)
	// Close is called once, when the track ends.
	Close()
}

// discardOutput is used until SetOutput is called.
type discardOutput struct{}

func (discardOutput) Status(string)            {}
func (discardOutput) NewTrack() (Track, error) { return discardTrack{}, nil }

type discardTrack struct{}

func (discardTrack) Push(*rtp.Packet) bool { return false }
func (discardTrack) Close()                {}

// NewWebRTCClient creates a client for one signaling server. kind is one of
// adapters.Kinds(), or "auto" to detect it from the URL; sessionID is the
// room or stream (unused by WHEP).
func NewWebRTCClient(kind, serverURL, sessionID string) (*WebRTCClient, error) {
	if kind == "auto" {
		kind = adapters.DetectAdapterType(serverURL)
	}
	session, err := adapters.NewSession(kind, serverURL, sessionID)
	if err != nil {
		return nil, err
	}

	tracksCtx, cancelTracks := context.WithCancel(context.Background())
	return &WebRTCClient{
		session:      session,
		status:       "Initializing",
		output:       discardOutput{},
		stunServers:  DefaultSTUNServers,
		tracksCtx:    tracksCtx,
		cancelTracks: cancelTracks,
	}, nil
}

// SetOutput sets where status and video tracks go. Call it before Connect.
func (c *WebRTCClient) SetOutput(o Output) {
	c.output = o
	o.Status(c.GetStatus())
}

// SetSTUNServers sets the STUN server URLs used for ICE ("stun:host:port").
// An empty list disables STUN: only host candidates are gathered.
func (c *WebRTCClient) SetSTUNServers(urls []string) {
	c.stunServers = urls
}

// SetStatus updates the connection status
func (c *WebRTCClient) SetStatus(status string) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.status = status
	c.output.Status(status)
}

// GetStatus returns the current connection status
func (c *WebRTCClient) GetStatus() string {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

// Connect negotiates a peer connection through the signaling session and
// plays video until the connection or the signaling fails, or ctx is
// canceled. Before returning, the session cleans up (Janus destroy, WHEP
// DELETE), the peer connection is closed and track handlers are waited for.
// Cancellation returns ctx.Err().
func (c *WebRTCClient) Connect(ctx context.Context) error {
	c.SetStatus("Creating peer connection")
	var webrtcConfig webrtc.Configuration
	if len(c.stunServers) > 0 {
		webrtcConfig.ICEServers = []webrtc.ICEServer{{URLs: c.stunServers}}
	}

	var err error
	c.peerConnection, err = webrtc.NewPeerConnection(webrtcConfig)
	if err != nil {
		c.SetStatus("Failed to create peer connection")
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	defer func() {
		// Stop track handlers first: closing the peer connection ends their
		// ReadRTP calls, and each handler closes its own Track on return.
		c.stopTracks()
		_ = c.peerConnection.Close()
		c.tracks.Wait()
	}()
	c.SetStatus("Peer connection created")

	c.peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			c.SetStatus("Video track received")
			slog.Info("Received video track", "codec", track.Codec().MimeType, "via", c.session.String())
			c.runTrack(func(ctx context.Context) {
				readPacket := func() (*rtp.Packet, error) {
					packet, _, err := track.ReadRTP()
					return packet, err
				}
				requestKeyframe := func() {
					pli := &rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}
					if err := c.peerConnection.WriteRTCP([]rtcp.Packet{pli}); err != nil {
						slog.Debug("Failed to send keyframe request", "error", err)
					}
				}
				c.handleVideoTrack(ctx, track.Codec(), readPacket, requestKeyframe)
			})
		}
	})

	// ICE connection state monitoring
	c.peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		stateStr := state.String()
		c.SetStatus("ICE: " + stateStr)
		slog.Info("ICE connection state", "state", stateStr)

		switch state {
		case webrtc.ICEConnectionStateConnected:
			c.SetStatus("ICE connected")
		case webrtc.ICEConnectionStateCompleted:
			c.SetStatus("ICE connection completed")
		case webrtc.ICEConnectionStateDisconnected:
			c.SetStatus("ICE disconnected")
		case webrtc.ICEConnectionStateFailed:
			c.SetStatus("ICE connection failed")
		case webrtc.ICEConnectionStateClosed:
			c.SetStatus("ICE connection closed")
		}
	})

	// Peer connection state monitoring
	c.peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		stateStr := state.String()
		slog.Info("Peer connection state", "state", stateStr)

		switch state {
		case webrtc.PeerConnectionStateConnected:
			c.SetStatus("Peer connection established")
		case webrtc.PeerConnectionStateFailed:
			c.SetStatus("Peer connection failed")
		case webrtc.PeerConnectionStateDisconnected:
			c.SetStatus("Peer connection disconnected")
		case webrtc.PeerConnectionStateClosed:
			c.SetStatus("Peer connection closed")
		}
	})

	// ICE gathering state monitoring
	c.peerConnection.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		stateStr := state.String()
		slog.Debug("ICE gathering state", "state", stateStr)

		switch state {
		case webrtc.ICEGatheringStateGathering:
			c.SetStatus("Gathering ICE candidates")
		case webrtc.ICEGatheringStateComplete:
			c.SetStatus("ICE gathering complete")
		}
	})

	// The session negotiates and keeps signaling until it fails or ctx
	// ends; the monitor watches the peer connection meanwhile.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.SetStatus("Signaling via " + c.session.String())
	sessionDone := make(chan error, 1)
	go func() { sessionDone <- c.session.Run(ctx, c.peerConnection) }()
	monitorErr := make(chan error, 1)
	go c.monitorConnection(ctx, monitorErr)

	select {
	case err := <-sessionDone:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.SetStatus("Signaling failed")
		return fmt.Errorf("%s: %w", c.session.String(), err)
	case err := <-monitorErr:
		c.SetStatus("Connection error")
		cancel()
		_ = <-sessionDone // let the session clean up; its error is expected after cancel
		return fmt.Errorf("connection lost: %w", err)
	case <-ctx.Done():
		c.SetStatus("Connection canceled")
		_ = <-sessionDone
		return ctx.Err()
	}
}

// runTrack runs a track handler on the calling goroutine (pion's OnTrack
// goroutine), unless the client is shutting down.
func (c *WebRTCClient) runTrack(handle func(ctx context.Context)) {
	c.tracksMu.Lock()
	if c.tracksStopped {
		c.tracksMu.Unlock()
		return
	}
	c.tracks.Add(1)
	c.tracksMu.Unlock()
	defer c.tracks.Done()

	handle(c.tracksCtx)
}

// stopTracks refuses new track handlers and tells running ones to stop.
// The caller must also unblock their reads (close the peer connection)
// before waiting on c.tracks.
func (c *WebRTCClient) stopTracks() {
	c.tracksMu.Lock()
	c.tracksStopped = true
	c.tracksMu.Unlock()
	c.cancelTracks()
}

// keyframeRequestInterval limits keyframe requests (RTCP PLI) per track.
const keyframeRequestInterval = time.Second

// handleVideoTrack passes a video track's packets to a new Track from the
// output until readPacket fails or ctx is canceled. It owns the Track:
// nothing else may use or close it. requestKeyframe asks the sender for a keyframe; it is
// called when the track starts (so the picture appears without waiting for
// the next scheduled keyframe) and when the Track asks for one, at most once
// per keyframeRequestInterval.
func (c *WebRTCClient) handleVideoTrack(ctx context.Context, codec webrtc.RTPCodecParameters,
	readPacket func() (*rtp.Packet, error), requestKeyframe func(),
) {
	// Defence in depth: this loop parses untrusted network input. A bug there
	// must end video processing, not crash the process.
	defer func() {
		if r := recover(); r != nil {
			c.SetStatus("Video processing stopped (internal error)")
			slog.Error("Video track handler panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	if msg, supported := validateVideoCodec(codec.MimeType); !supported {
		c.SetStatus(msg)
		slog.Error("Video track rejected", "reason", msg)
		return
	}

	c.SetStatus(fmt.Sprintf("Processing video (%s)", codec.MimeType))
	track, err := c.output.NewTrack()
	if err != nil {
		c.SetStatus("Failed to set up the video track")
		slog.Error("Failed to set up the video track", "error", err)
		return
	}
	defer track.Close()

	var lastRequest time.Time
	askForKeyframe := func() {
		if time.Since(lastRequest) >= keyframeRequestInterval {
			lastRequest = time.Now()
			requestKeyframe()
		}
	}
	askForKeyframe()

	c.SetStatus("Receiving video stream")
	for ctx.Err() == nil {
		packet, err := readPacket()
		if err != nil {
			if ctx.Err() == nil {
				slog.Info("Video track ended", "reason", err)
			}
			return
		}
		if track.Push(packet) {
			askForKeyframe()
		}
	}
}

// validateVideoCodec reports whether a codec is currently supported.
func validateVideoCodec(mimeType string) (message string, supported bool) {
	switch strings.ToLower(mimeType) {
	case "video/h264":
		return "", true
	case "video/vp8", "video/vp9":
		return fmt.Sprintf("Codec %s is not supported (H.264 only).", mimeType), false
	default:
		return "Unsupported codec: " + mimeType, false
	}
}

// monitorConnection monitors the connection health and reports errors
func (c *WebRTCClient) monitorConnection(ctx context.Context, errCh chan<- error) {
	monitorConnectionState(ctx, c.peerConnection, errCh, connectionMonitorInterval)
}

func monitorConnectionState(
	ctx context.Context,
	stateProvider peerConnectionStateProvider,
	errCh chan<- error,
	tickerInterval time.Duration,
) {
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if stateProvider == nil {
				continue
			}

			state := stateProvider.ConnectionState()
			if state == webrtc.PeerConnectionStateFailed ||
				state == webrtc.PeerConnectionStateClosed {
				select {
				case errCh <- fmt.Errorf("peer connection failed: %s", state):
				default:
				}
				return
			}
		}
	}
}
