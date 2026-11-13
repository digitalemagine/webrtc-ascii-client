package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// browserCandidate is a candidate as a browser's RTCIceCandidate.toJSON()
// sends it. It points nowhere; it only has to be accepted.
var browserCandidate = map[string]any{
	"candidate":        "candidate:842163049 1 udp 1677729535 192.0.2.1 46154 typ srflx raddr 0.0.0.0 rport 0 generation 0 ufrag sXgE network-cost 999",
	"sdpMid":           "0",
	"sdpMLineIndex":    0,
	"usernameFragment": "sXgE",
}

// simpleServer is a Simple signaling server with its own peer connection:
// on join it sends an offer and a browser-shaped candidate (before the
// client could answer), applies the answer, and records what the client sent.
type simpleServer struct {
	url      string
	answered chan struct{}

	mu       sync.Mutex
	messages []map[string]any
}

func newSimpleServer(t *testing.T) *simpleServer {
	t.Helper()
	s := &simpleServer{answered: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		pc := newTestPeerConnection(t)
		var once sync.Once
		for {
			var msg map[string]any
			if conn.ReadJSON(&msg) != nil {
				return
			}
			s.mu.Lock()
			s.messages = append(s.messages, msg)
			s.mu.Unlock()
			switch msg["type"] {
			case "join":
				offer := offerVideo(t, pc)
				_ = conn.WriteJSON(map[string]any{"type": "offer", "sdp": offer})
				_ = conn.WriteJSON(map[string]any{"type": "ice", "ice": browserCandidate})
			case "answer":
				sdp, _ := msg["sdp"].(map[string]any)
				err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp["sdp"].(string)})
				if err != nil {
					t.Errorf("server: %v", err)
				}
				once.Do(func() { close(s.answered) })
			}
		}
	}))
	t.Cleanup(srv.Close)
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return s
}

func (s *simpleServer) received(msgType string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, m := range s.messages {
		if m["type"] == msgType {
			out = append(out, m)
		}
	}
	return out
}

// offerVideo returns an offer with one H.264 track and all candidates.
func offerVideo(t *testing.T, pc *webrtc.PeerConnection) webrtc.SessionDescription {
	t.Helper()
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pc.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	return *pc.LocalDescription()
}

// The session joins, answers the offer (a candidate that arrived before
// the offer was applied is held back, not rejected), and sends its
// candidates as W3C RTCIceCandidateInit, not pion's internal struct.
func TestSimpleRun(t *testing.T) {
	t.Parallel()

	srv := newSimpleServer(t)
	stop, done := runSession(t, NewSimpleAdapter(srv.url, "room"), newTestPeerConnection(t))

	select {
	case <-srv.answered:
	case err := <-done:
		t.Fatalf("Run returned before answering: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no answer")
	}

	join := srv.received("join")
	if len(join) != 1 || join[0]["sessionId"] != "room" {
		t.Fatalf("join messages %v", join)
	}
	if _, ok := join[0]["roomId"]; ok {
		t.Error("join still carries the legacy roomId")
	}

	waitFor(t, "a local candidate", func() bool { return len(srv.received("ice")) > 0 })
	ice, _ := srv.received("ice")[0]["ice"].(map[string]any)
	var keys []string
	for k := range ice {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"candidate", "sdpMLineIndex", "sdpMid", "usernameFragment"}; !slices.Equal(keys, want) {
		t.Errorf("ice keys = %v, want %v", keys, want)
	}
	if c, _ := ice["candidate"].(string); !strings.HasPrefix(c, "candidate:") {
		t.Errorf("candidate = %q", ice["candidate"])
	}

	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

// A failed signaling connection ends Run with an error.
func TestSimpleRunConnectionLost(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = conn.ReadJSON(&msg) // join
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)

	_, done := runSession(t, NewSimpleAdapter("ws"+strings.TrimPrefix(srv.URL, "http"), "room"), newTestPeerConnection(t))
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "signaling connection lost") {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not notice the closed connection")
	}
}
