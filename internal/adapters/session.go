// Package adapters implements WebRTC signaling: the Simple WebSocket
// protocol, the Janus streaming plugin, and WHEP (MediaMTX and others).
package adapters

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/pion/webrtc/v4"
)

// Session negotiates a peer connection with one signaling protocol and keeps
// signaling alive (trickle ICE, keepalive) until ctx is canceled or the
// signaling connection fails. Each protocol decides who offers: Simple and
// Janus answer the server's offer; WHEP sends an offer. Run cleans up
// (Janus destroy, WHEP DELETE) before returning.
type Session interface {
	Run(ctx context.Context, pc *webrtc.PeerConnection) error
	String() string
}

// Session kinds.
const (
	KindSimple = "simple"
	KindJanus  = "janus"
	KindWHEP   = "whep"
)

// Kinds lists the supported session kinds.
func Kinds() []string { return []string{KindSimple, KindJanus, KindWHEP} }

// NeedsSessionID reports whether a kind needs a session (room, stream) ID in
// addition to the URL. WHEP identifies the stream by its URL.
func NeedsSessionID(kind string) bool { return kind != KindWHEP }

// NewSession returns a session of the given kind. sessionID is the room
// (Simple) or mountpoint (Janus); WHEP ignores it.
func NewSession(kind, serverURL, sessionID string) (Session, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	switch kind {
	case KindSimple:
		return NewSimpleAdapter(u.String(), sessionID), nil
	case KindJanus:
		return NewJanusAdapter(u.String(), sessionID), nil
	case KindWHEP:
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("WHEP needs an http:// or https:// URL, got %q", serverURL)
		}
		return NewWHEPSession(u.String()), nil
	default:
		return nil, fmt.Errorf("unsupported adapter type %q (want one of %v)", kind, Kinds())
	}
}

// DetectAdapterType guesses the session kind from the URL: http(s) URLs are
// WHEP; a path containing "janus" or port 8188 is Janus; anything else is Simple.
func DetectAdapterType(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return KindSimple
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return KindWHEP
	}
	if strings.Contains(strings.ToLower(u.Path), "janus") || u.Port() == "8188" {
		return KindJanus
	}
	return KindSimple
}

// remoteCandidates adds the peer's ICE candidates to the peer connection,
// holding back those that arrive before the remote description is set
// (pion rejects them then).
type remoteCandidates struct {
	pc      *webrtc.PeerConnection
	mu      sync.Mutex
	pending []webrtc.ICECandidateInit
}

func newRemoteCandidates(pc *webrtc.PeerConnection) *remoteCandidates {
	return &remoteCandidates{pc: pc}
}

func (r *remoteCandidates) add(c webrtc.ICECandidateInit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pc.RemoteDescription() == nil {
		r.pending = append(r.pending, c)
		return nil
	}
	return r.pc.AddICECandidate(c)
}

// setRemoteDescription sets the remote description and adds the candidates
// held back until now.
func (r *remoteCandidates) setRemoteDescription(desc webrtc.SessionDescription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.pc.SetRemoteDescription(desc); err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}
	for _, c := range r.pending {
		if err := r.pc.AddICECandidate(c); err != nil {
			return fmt.Errorf("add ICE candidate: %w", err)
		}
	}
	r.pending = nil
	return nil
}

// answer applies the remote offer and returns the local answer.
func (r *remoteCandidates) answer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := r.setRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	answer, err := r.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("create answer: %w", err)
	}
	if err := r.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("set local description: %w", err)
	}
	return answer, nil
}
