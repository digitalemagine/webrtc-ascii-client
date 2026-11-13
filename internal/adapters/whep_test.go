package adapters

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// whepServer is a WHEP endpoint with a pion peer connection, like MediaMTX:
// POST /stream/whep with an SDP offer gets 201, an SDP answer and a relative
// Location; DELETE on that location ends the session.
type whepServer struct {
	url      string
	mu       sync.Mutex
	offers   []string
	deletes  []string
	answered chan struct{}
}

func newWHEPServer(t *testing.T) *whepServer {
	t.Helper()
	s := &whepServer{answered: make(chan struct{})}
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("POST /stream/whep", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/sdp" {
			http.Error(w, "want application/sdp, got "+ct, http.StatusUnsupportedMediaType)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.offers = append(s.offers, string(body))
		s.mu.Unlock()

		pc := newTestPeerConnection(t)
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "whep")
		if err != nil {
			t.Error(err)
			return
		}
		if _, err = pc.AddTrack(track); err != nil {
			t.Error(err)
			return
		}
		if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(body)}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			t.Error(err)
			return
		}
		gathered := webrtc.GatheringCompletePromise(pc)
		if err := pc.SetLocalDescription(answer); err != nil {
			t.Error(err)
			return
		}
		<-gathered
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "session/abc")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, pc.LocalDescription().SDP)
		once.Do(func() { close(s.answered) })
	})
	mux.HandleFunc("DELETE /stream/session/abc", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.deletes = append(s.deletes, r.URL.Path)
		s.mu.Unlock()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.url = srv.URL + "/stream/whep"
	return s
}

func (s *whepServer) deleted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deletes)
}

// The client offers to receive video, applies the server's answer and
// deletes the session (relative Location resolved) when canceled.
func TestWHEPRun(t *testing.T) {
	t.Parallel()

	srv := newWHEPServer(t)
	s, err := NewSession(KindWHEP, srv.url, "")
	if err != nil {
		t.Fatal(err)
	}
	pc := newTestPeerConnection(t)
	stop, done := runSession(t, s, pc)

	select {
	case <-srv.answered:
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no offer reached the server")
	}
	waitFor(t, "the answer to be applied", func() bool { return pc.RemoteDescription() != nil })
	srv.mu.Lock()
	offer := srv.offers[0]
	srv.mu.Unlock()
	if !strings.Contains(offer, "m=video") || !strings.Contains(offer, "a=recvonly") {
		t.Errorf("offer is not a recvonly video offer:\n%s", offer)
	}
	if !strings.Contains(offer, "a=candidate:") {
		t.Error("offer has no ICE candidates (gathering not awaited)")
	}

	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if n := srv.deleted(); n != 1 {
		t.Fatalf("%d DELETE requests, want 1", n)
	}
}

// A server error (e.g. MediaMTX's 404 for an unknown stream) ends Run with
// the status and message.
func TestWHEPServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "stream 'nope' not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, done := runSession(t, NewWHEPSession(srv.URL+"/nope/whep"), newTestPeerConnection(t))
	select {
	case err := <-done:
		for _, want := range []string{"404", "stream 'nope' not found"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("error %v does not contain %q", err, want)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not fail")
	}
}

func TestDetectAdapterType(t *testing.T) {
	t.Parallel()

	for url, want := range map[string]string{
		"http://localhost:8889/mystream/whep": KindWHEP,
		"https://example.org/live/whep":       KindWHEP,
		"ws://localhost:8188":                 KindJanus,
		"wss://example.org/janus":             KindJanus,
		"ws://localhost:8080/ws":              KindSimple,
		"::bad url":                           KindSimple,
	} {
		if got := DetectAdapterType(url); got != want {
			t.Errorf("DetectAdapterType(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestNewSessionRejects(t *testing.T) {
	t.Parallel()

	if _, err := NewSession(KindWHEP, "ws://localhost:8889/x/whep", ""); err == nil {
		t.Error("WHEP accepted a ws:// URL")
	}
	if _, err := NewSession("mediamtx", "http://localhost:8889/x/whep", ""); err == nil {
		t.Error("unknown kind accepted")
	}
}
