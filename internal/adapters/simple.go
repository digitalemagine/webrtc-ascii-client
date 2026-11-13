package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/pion/webrtc/v4"
)

// SimpleSignalingMessage is the Simple protocol's JSON message. SDP and ICE
// use the W3C dictionaries (RTCSessionDescriptionInit, RTCIceCandidateInit),
// so a browser can send what toJSON() returns.
type SimpleSignalingMessage struct {
	Type      string                     `json:"type"`
	SessionID string                     `json:"sessionId,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	ICE       *webrtc.ICECandidateInit   `json:"ice,omitempty"`
}

// SimpleAdapter is the Simple WebSocket protocol (see docs/SIGNALING.md): join a
// session, answer the server's offer, trickle candidates both ways.
type SimpleAdapter struct {
	baseAdapter
	url       string
	sessionID string
}

// NewSimpleAdapter returns a Simple session for serverURL and sessionID.
func NewSimpleAdapter(serverURL, sessionID string) *SimpleAdapter {
	return &SimpleAdapter{baseAdapter: newBaseAdapter("Simple WebSocket Signaling"), url: serverURL, sessionID: sessionID}
}

// Run implements Session.
func (a *SimpleAdapter) Run(ctx context.Context, pc *webrtc.PeerConnection) error {
	slog.Info("Simple: connecting", "url", a.url, "session", a.sessionID)
	if err := a.connectWebSocket(ctx, a.url, nil); err != nil {
		return err
	}
	defer func() { _ = a.close() }()

	candidates := newRemoteCandidates(pc)
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // the protocol has no end-of-candidates message
		}
		if err := a.sendICECandidate(c); err != nil {
			slog.Warn("Simple: failed to send ICE candidate", "error", err)
		}
	})
	a.startKeepalive(a.sendAppPing)

	if err := a.sendJSON(SimpleSignalingMessage{Type: "join", SessionID: a.sessionID}); err != nil {
		return fmt.Errorf("send join: %w", err)
	}
	slog.Info("Simple: joined", "session", a.sessionID)

	msgs, readErr := a.readMessages(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			return fmt.Errorf("signaling connection lost: %w", err)
		case msg := <-msgs:
			if err := a.handle(msg, candidates); err != nil {
				return err
			}
		}
	}
}

func (a *SimpleAdapter) handle(msg SimpleSignalingMessage, candidates *remoteCandidates) error {
	switch msg.Type {
	case "offer":
		if msg.SDP == nil {
			return errors.New("offer without sdp")
		}
		slog.Info("Simple: received offer")
		answer, err := candidates.answer(*msg.SDP)
		if err != nil {
			return err
		}
		if err := a.sendJSON(SimpleSignalingMessage{Type: "answer", SessionID: a.sessionID, SDP: &answer}); err != nil {
			return fmt.Errorf("send answer: %w", err)
		}
		slog.Info("Simple: answer sent")
	case "ice":
		if msg.ICE != nil {
			if err := candidates.add(*msg.ICE); err != nil {
				slog.Warn("Simple: failed to add ICE candidate", "error", err)
			}
		}
	default:
		slog.Debug("Simple: ignoring message", "type", msg.Type)
	}
	return nil
}

// readMessages reads messages until the connection fails or ctx ends.
func (a *SimpleAdapter) readMessages(ctx context.Context) (<-chan SimpleSignalingMessage, <-chan error) {
	msgs := make(chan SimpleSignalingMessage)
	errs := make(chan error, 1)
	go func() {
		for {
			var msg SimpleSignalingMessage
			if err := a.wsConn.ReadJSON(&msg); err != nil {
				errs <- err
				return
			}
			select {
			case msgs <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return msgs, errs
}

// sendICECandidate sends a local candidate as RTCIceCandidateInit.
func (a *SimpleAdapter) sendICECandidate(candidate *webrtc.ICECandidate) error {
	init := candidate.ToJSON()
	return a.sendJSON(SimpleSignalingMessage{Type: "ice", SessionID: a.sessionID, ICE: &init})
}

func (a *SimpleAdapter) sendAppPing() error {
	return a.sendJSON(SimpleSignalingMessage{Type: "ping", SessionID: a.sessionID})
}
