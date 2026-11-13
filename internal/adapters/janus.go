package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/pion/webrtc/v4"
)

// janusSubprotocol is the WebSocket subprotocol the Janus API requires.
const janusSubprotocol = "janus-protocol"

// JanusAdapter watches a mountpoint of the Janus streaming plugin: create a
// session, attach the plugin, watch the mountpoint, answer the plugin's
// offer, trickle candidates both ways, destroy the session on exit.
//
// See also:
//   - janus_types.go        — protocol message types
//   - janus_transactions.go — transaction map helpers
//   - janus_session.go      — session setup (create, attach, list, watch)
//   - janus_messages.go     — inbound message loop
type JanusAdapter struct {
	baseAdapter
	url      string
	streamID string

	// Written by Run, read by keepalive and by pion's ICE callback on
	// other goroutines.
	sessionID atomic.Int64
	handleID  atomic.Int64

	transactions map[string]chan JanusMessage
	transMu      sync.Mutex

	candidates *remoteCandidates
	lost       chan error // the message loop ended: connection lost or hangup
}

// NewJanusAdapter returns a Janus session for serverURL watching streamID.
func NewJanusAdapter(serverURL, streamID string) *JanusAdapter {
	return &JanusAdapter{
		baseAdapter:  newBaseAdapter("Janus WebRTC Gateway (Streaming Plugin)"),
		url:          serverURL,
		streamID:     streamID,
		transactions: make(map[string]chan JanusMessage),
		lost:         make(chan error, 1),
	}
}

// Run implements Session.
func (a *JanusAdapter) Run(ctx context.Context, pc *webrtc.PeerConnection) error {
	slog.Info("Janus: connecting", "url", a.url, "stream", a.streamID)
	// Janus drops WebSocket connections that don't request its subprotocol.
	if err := a.connectWebSocket(ctx, a.url, []string{janusSubprotocol}); err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	a.candidates = newRemoteCandidates(pc)
	go a.handleMessages()

	if err := a.createSession(ctx); err != nil {
		return fmt.Errorf("failed to create Janus session: %w", err)
	}
	// Janus keepalives refer to the session, so start them once it exists.
	a.startKeepalive(a.sendJanusKeepalive)

	if err := a.attachPlugin(ctx); err != nil {
		return fmt.Errorf("failed to attach streaming plugin: %w", err)
	}
	if err := a.listStreams(ctx); err != nil {
		slog.Debug("Janus: failed to list streams", "error", err)
	}
	offer, err := a.watchStream(ctx)
	if err != nil {
		return err
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if sendErr := a.trickle(c); sendErr != nil {
			slog.Warn("Janus: failed to send ICE candidate", "error", sendErr)
		}
	})
	answer, err := a.candidates.answer(offer)
	if err != nil {
		return err
	}
	if err := a.sendStart(answer); err != nil {
		return fmt.Errorf("send answer: %w", err)
	}
	slog.Info("Janus: answer sent")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-a.lost:
		return err
	}
}

// sendStart sends the answer with the streaming plugin's "start" request.
// Like all fire-and-forget messages it carries a transaction ID, as Janus
// requires, but no reply is awaited.
func (a *JanusAdapter) sendStart(answer webrtc.SessionDescription) error {
	return a.sendJSON(JanusMessage{
		Janus:       "message",
		Transaction: a.generateTransaction(),
		SessionID:   new(a.sessionID.Load()),
		HandleID:    new(a.handleID.Load()),
		Body:        JanusBody{Request: "start"},
		JSEP:        &answer,
	})
}

// trickle sends a local candidate, or the end of candidates for nil.
func (a *JanusAdapter) trickle(c *webrtc.ICECandidate) error {
	jc := &JanusCandidate{Completed: true}
	if c != nil {
		init := c.ToJSON()
		if init.SDPMid == nil || init.SDPMLineIndex == nil {
			return errors.New("ICE candidate missing sdpMid or sdpMLineIndex")
		}
		jc = &JanusCandidate{Candidate: init.Candidate, SdpMid: init.SDPMid, SdpMLineIndex: init.SDPMLineIndex}
	}
	return a.sendJSON(JanusMessage{
		Janus:       "trickle",
		Transaction: a.generateTransaction(),
		SessionID:   new(a.sessionID.Load()),
		HandleID:    new(a.handleID.Load()),
		Candidate:   jc,
	})
}

// Close destroys the Janus session, if one was created, and closes the connection.
func (a *JanusAdapter) Close() error {
	if sid := a.sessionID.Load(); sid != 0 {
		destroyMsg := JanusMessage{Janus: "destroy", Transaction: a.generateTransaction(), SessionID: &sid}
		if err := a.sendJSON(destroyMsg); err != nil {
			slog.Warn("Janus: failed to destroy session", "error", err)
		}
	}
	err := a.close()
	a.clearTransactions()
	return err
}

// sendJanusKeepalive sends a Janus keepalive for the active session.
func (a *JanusAdapter) sendJanusKeepalive() error {
	if sid := a.sessionID.Load(); sid != 0 {
		return a.sendJSON(JanusMessage{Janus: "keepalive", Transaction: a.generateTransaction(), SessionID: &sid})
	}
	return nil
}
