package adapters

import (
	"fmt"
	"log/slog"

	"github.com/pion/webrtc/v4"
)

// handleMessages reads from the WebSocket until the connection ends, and
// delivers each message: replies to waiting transactions, the server's
// trickle candidates to the peer connection. When it ends (read error, or
// Janus hanging up the PeerConnection) it reports on a.lost.
func (a *JanusAdapter) handleMessages() {
	for {
		var msg JanusMessage
		if err := a.wsConn.ReadJSON(&msg); err != nil {
			if !a.isCloseRequested() {
				a.lost <- fmt.Errorf("signaling connection lost: %w", err)
			}
			return
		}

		switch msg.Janus {
		case "ack":
			// Only confirms receipt. Requests the plugin handles asynchronously
			// (watch) get their real reply later with the same transaction, so
			// an ack must not complete the transaction.
			continue
		case "trickle":
			a.addRemoteCandidate(msg.Candidate)
			continue
		case "hangup":
			a.lost <- fmt.Errorf("janus hung up the connection: %s", msg.Reason)
			return
		case "error":
			if msg.Error != nil {
				slog.Warn("Janus: error", "reason", msg.Error.Reason, "code", msg.Error.Code)
			}
		case "webrtcup", "media", "slowlink":
			slog.Debug("Janus: event", "janus", msg.Janus)
		}

		if msg.Transaction != "" {
			if respCh, ok := a.popTransaction(msg.Transaction); ok {
				select {
				case respCh <- msg:
				default:
					slog.Debug("Janus: dropping duplicate reply", "transaction", msg.Transaction)
				}
			}
		}
	}
}

func (a *JanusAdapter) addRemoteCandidate(c *JanusCandidate) {
	if c == nil || c.Completed {
		return
	}
	init := webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SdpMid, SDPMLineIndex: c.SdpMLineIndex}
	if err := a.candidates.add(init); err != nil {
		slog.Warn("Janus: failed to add ICE candidate", "error", err)
	}
}
