package adapters

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	// whepGatherTimeout bounds the wait for ICE gathering before the offer
	// is sent. WHEP allows trickling later with PATCH; sending the complete
	// offer once is simpler and servers accept it.
	whepGatherTimeout = 2 * time.Second
	// whepRequestTimeout bounds each HTTP request.
	whepRequestTimeout = 10 * time.Second
)

// WHEPSession is a WHEP client (IETF draft-ietf-wish-whep), as served by
// MediaMTX: it offers to receive video, POSTs the offer to the endpoint URL,
// applies the answer, and DELETEs the session resource on exit.
type WHEPSession struct {
	endpoint string
	client   *http.Client
}

// NewWHEPSession returns a WHEP session for the endpoint URL, e.g.
// http://localhost:8889/mystream/whep for MediaMTX.
func NewWHEPSession(endpoint string) *WHEPSession {
	return &WHEPSession{endpoint: endpoint, client: &http.Client{Timeout: whepRequestTimeout}}
}

// String names the session kind.
func (s *WHEPSession) String() string { return "WHEP" }

// Run implements Session.
func (s *WHEPSession) Run(ctx context.Context, pc *webrtc.PeerConnection) error {
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return fmt.Errorf("add video transceiver: %w", err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set local description: %w", err)
	}
	select {
	case <-gathered:
	case <-time.After(whepGatherTimeout):
		slog.Debug("WHEP: ICE gathering not complete; sending the candidates found so far")
	case <-ctx.Done():
		return ctx.Err()
	}

	slog.Info("WHEP: sending offer", "url", s.endpoint)
	answer, resource, err := s.post(ctx, pc.LocalDescription().SDP)
	if err != nil {
		return err
	}
	if resource != "" {
		// Delete even after cancellation: that is when it matters.
		defer s.delete(context.WithoutCancel(ctx), resource)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}
	slog.Info("WHEP: answer applied")

	<-ctx.Done()
	return ctx.Err()
}

// post sends the offer and returns the answer SDP and the session resource URL.
func (s *WHEPSession) post(ctx context.Context, offer string) (answer, resource string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, strings.NewReader(offer))
	if err != nil {
		return "", "", fmt.Errorf("WHEP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/sdp")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("WHEP request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("WHEP response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("server answered %s: %s", resp.Status, strings.TrimSpace(string(firstLine(body))))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/sdp") {
		return "", "", fmt.Errorf("server answered with content type %q, want application/sdp", ct)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		// Relative to the URL that answered, which differs from the
		// endpoint after a redirect.
		if ref, err := url.Parse(loc); err == nil {
			resource = resp.Request.URL.ResolveReference(ref).String()
		}
	} else {
		slog.Debug("WHEP: no Location header; the session can't be deleted on exit")
	}
	return string(body), resource, nil
}

// delete ends the session on the server. It runs during shutdown, so it has
// its own short deadline.
func (s *WHEPSession) delete(ctx context.Context, resource string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, resource, nil)
	if err != nil {
		slog.Warn("WHEP: failed to end session", "error", err)
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		slog.Warn("WHEP: failed to end session", "error", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		slog.Warn("WHEP: failed to end session", "status", resp.Status)
		return
	}
	slog.Info("WHEP: session ended")
}

func firstLine(b []byte) []byte {
	line, _, _ := bytes.Cut(b, []byte{'\n'})
	return line
}
