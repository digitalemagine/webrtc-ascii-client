package client

import (
	"errors"
	"fmt"
	"strings"

	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters"
)

// Command-line helpers shared by the commands that connect.

// Target is the signaling server and session given on the command line.
type Target struct {
	Kind      string // one of adapters.Kinds()
	URL       string
	SessionID string // room or mountpoint; empty for WHEP
}

// ParseTarget reads the arguments <server_url> [<session_id>]. kind is the
// -adapter value; "auto" detects it from the URL. More arguments are an
// error: the flag package stops at the URL, so they are usually misplaced
// options.
func ParseTarget(kind string, args []string) (Target, error) {
	switch {
	case len(args) < 1:
		return Target{}, errors.New("server URL is required")
	case len(args) > 2:
		return Target{}, fmt.Errorf("unexpected arguments %q (options must come before the server URL)", args[2:])
	}
	t := Target{Kind: kind, URL: args[0]}
	if t.Kind == "auto" {
		t.Kind = adapters.DetectAdapterType(t.URL)
	}
	if len(args) > 1 {
		t.SessionID = args[1]
	} else if adapters.NeedsSessionID(t.Kind) {
		return Target{}, fmt.Errorf("the %s adapter needs a session ID after the URL", t.Kind)
	}
	return t, nil
}

// STUNFlag collects repeated -stun values. An empty value contributes no
// server, so -stun "" alone means "no STUN".
type STUNFlag struct {
	urls []string
	set  bool
}

// Usage is the help text for the -stun flag.
func (f *STUNFlag) Usage() string {
	return "STUN server `URL`, e.g. stun:stun.example.org:3478 (repeatable; -stun \"\" disables STUN; default " +
		strings.Join(DefaultSTUNServers, ", ") + ")"
}

func (f *STUNFlag) String() string { return strings.Join(f.urls, ",") }

// Set implements flag.Value.
func (f *STUNFlag) Set(v string) error {
	f.set = true
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "stun:") && !strings.HasPrefix(v, "stuns:") {
		return fmt.Errorf("%q is not a STUN URL (stun:host[:port])", v)
	}
	f.urls = append(f.urls, v)
	return nil
}

// Apply sets the client's STUN servers if the flag was given.
func (f *STUNFlag) Apply(c *WebRTCClient) {
	if f.set {
		c.SetSTUNServers(f.urls)
	}
}
