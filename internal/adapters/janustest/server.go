// Package janustest provides a minimal Janus WebSocket server for tests.
//
// It speaks enough of the Janus API (create, attach, keepalive, trickle,
// destroy, and the streaming plugin's list/watch/start) to drive the client's
// Janus adapter. Like a stock Janus, it refuses WebSocket connections that
// don't ask for the "janus-protocol" subprotocol. Watching stream StreamID
// creates a real pion peer connection with an H.264 track, so a client can
// connect and receive whatever a test writes to Track.
//
// The reply to watching an unknown stream is the one Janus 1.1.4 sent
// (testdata/watch-unknown-stream.json); the other replies follow the Janus
// documentation.
package janustest

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// Subprotocol is the WebSocket subprotocol Janus requires.
const Subprotocol = "janus-protocol"

// Identifiers the server hands out.
const (
	SessionID int64 = 7798262264079476
	HandleID  int64 = 4850354630330792
)

// StreamID is the only stream the server knows; watching any other ID fails
// with error 455, as on a real Janus.
const StreamID = 1

// Candidate is an extra trickle candidate the server sends right after the
// watch event, before the client has answered. It points nowhere.
const Candidate = "candidate:1 1 udp 2130706431 127.0.0.1 9 typ host"

//go:embed testdata/watch-unknown-stream.json
var watchUnknownStream []byte

// Server is a mock Janus WebSocket endpoint.
type Server struct {
	// URL is the ws:// address to connect to.
	URL string

	srv *httptest.Server

	mu         sync.Mutex
	received   []map[string]any
	rejected   int
	track      *webrtc.TrackLocalStaticSample
	answered   chan struct{} // closed when a client's answer has been applied
	answerOnce sync.Once
}

// NewServer starts a server that is closed when the test ends.
func NewServer(t testing.TB) *Server {
	t.Helper()

	s := &Server{answered: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	s.URL = "ws" + strings.TrimPrefix(s.srv.URL, "http")
	return s
}

// Received returns the "janus" type of every message received, in order.
func (s *Server) Received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]string, 0, len(s.received))
	for _, m := range s.received {
		t, _ := m["janus"].(string)
		types = append(types, t)
	}
	return types
}

// Messages returns every message received, decoded as JSON objects.
func (s *Server) Messages() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.received)
}

// Track returns the video track of the most recent watch, or nil. Samples
// written to it reach a connected client.
func (s *Server) Track() *webrtc.TrackLocalStaticSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.track
}

// Answered is closed once a client's answer to a watch has been applied.
func (s *Server) Answered() <-chan struct{} { return s.answered }

// Rejected returns how many connections were refused for lacking the subprotocol.
func (s *Server) Rejected() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rejected
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if !slices.Contains(websocket.Subprotocols(r), Subprotocol) {
		s.mu.Lock()
		s.rejected++
		s.mu.Unlock()
		// Stock Janus drops the connection without a response.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "missing janus-protocol subprotocol", http.StatusBadRequest)
		return
	}

	up := websocket.Upgrader{Subprotocols: []string{Subprotocol}}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Like Janus, read and write independently: a client that closes while
	// replies are still being written must still have its earlier requests
	// (e.g. destroy) read and recorded.
	out := make(chan map[string]any, 64)
	defer close(out)
	go func() {
		for reply := range out {
			if conn.WriteJSON(reply) != nil {
				for range out { // keep draining so the reader never blocks
				}
				return
			}
		}
	}()

	c := &connection{server: s}
	defer c.close()
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		s.mu.Lock()
		s.received = append(s.received, msg)
		s.mu.Unlock()

		for _, reply := range c.replies(msg) {
			out <- reply
		}
	}
}

// connection is the state of one client connection: the peer connection
// created by a watch.
type connection struct {
	server *Server
	pc     *webrtc.PeerConnection
}

func (c *connection) close() {
	if c.pc != nil {
		_ = c.pc.Close()
	}
}

// replies returns the messages Janus sends in response to msg.
func (c *connection) replies(msg map[string]any) []map[string]any {
	tx, _ := msg["transaction"].(string)
	base := func(janus string) map[string]any {
		return map[string]any{"janus": janus, "transaction": tx, "session_id": SessionID}
	}

	switch msg["janus"] {
	case "create":
		return []map[string]any{{"janus": "success", "transaction": tx, "data": map[string]any{"id": SessionID}}}
	case "attach":
		r := base("success")
		r["data"] = map[string]any{"id": HandleID}
		return []map[string]any{r}
	case "keepalive", "trickle":
		return []map[string]any{base("ack")}
	case "destroy":
		return []map[string]any{base("success")}
	case "message":
		body, _ := msg["body"].(map[string]any)
		request, _ := body["request"].(string)
		return c.pluginReplies(tx, request, body["id"], msg["jsep"])
	default:
		r := base("error")
		r["error"] = map[string]any{"code": 453, "reason": "Unknown request"}
		return []map[string]any{r}
	}
}

// pluginReplies answers streaming plugin requests. Synchronous requests (list)
// get a single "success"; asynchronous ones (watch, start) get an "ack"
// followed by an "event" with the same transaction.
func (c *connection) pluginReplies(tx, request string, id, jsep any) []map[string]any {
	ack := map[string]any{"janus": "ack", "transaction": tx, "session_id": SessionID}
	event := func(data map[string]any) map[string]any {
		return map[string]any{
			"janus":       "event",
			"transaction": tx,
			"session_id":  SessionID,
			"sender":      HandleID,
			"plugindata":  map[string]any{"plugin": "janus.plugin.streaming", "data": data},
		}
	}

	switch request {
	case "list":
		r := event(map[string]any{
			"streaming": "list",
			"list":      []any{map[string]any{"id": StreamID, "description": "janustest"}},
		})
		r["janus"] = "success"
		return []map[string]any{r}
	case "watch":
		if n, ok := id.(float64); !ok || n != StreamID {
			var r map[string]any
			if err := json.Unmarshal(watchUnknownStream, &r); err != nil {
				panic(err)
			}
			r["transaction"] = tx
			return []map[string]any{ack, r}
		}
		offer, err := c.offer()
		if err != nil {
			return []map[string]any{event(map[string]any{"streaming": "event", "error_code": 500, "error": err.Error()})}
		}
		watched := event(map[string]any{"streaming": "event", "result": map[string]any{"status": "preparing"}})
		watched["jsep"] = map[string]any{"type": "offer", "sdp": offer}
		trickle := map[string]any{
			"janus":      "trickle",
			"session_id": SessionID,
			"sender":     HandleID,
			"candidate":  map[string]any{"sdpMid": "0", "sdpMLineIndex": 0, "candidate": Candidate},
		}
		return []map[string]any{ack, watched, trickle}
	case "start":
		if err := c.applyAnswer(jsep); err != nil {
			return []map[string]any{ack, event(map[string]any{"streaming": "event", "error_code": 500, "error": err.Error()})}
		}
		return []map[string]any{ack, event(map[string]any{"streaming": "event", "result": map[string]any{"status": "starting"}})}
	default:
		return []map[string]any{event(map[string]any{"streaming": "event", "error_code": 452, "error": "Unknown request"})}
	}
}

// offer creates the peer connection for a watch: one H.264 track, all ICE
// candidates gathered into the SDP.
func (c *connection) offer() (string, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", err
	}
	c.close()
	c.pc = pc
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"},
		"video", "janustest")
	if err != nil {
		return "", err
	}
	if _, err = pc.AddTrack(track); err != nil {
		return "", err
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	<-gathered
	c.server.mu.Lock()
	c.server.track = track
	c.server.mu.Unlock()
	return pc.LocalDescription().SDP, nil
}

func (c *connection) applyAnswer(jsep any) error {
	m, _ := jsep.(map[string]any)
	sdp, _ := m["sdp"].(string)
	if c.pc == nil || m["type"] != "answer" || sdp == "" {
		return errors.New("start without an answer to a watch")
	}
	if err := c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return err
	}
	c.server.answerOnce.Do(func() { close(c.server.answered) })
	return nil
}
