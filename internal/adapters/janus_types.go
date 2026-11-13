package adapters

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

// JanusMessage is the top-level envelope for all Janus WebSocket messages.
type JanusMessage struct {
	Janus       string                     `json:"janus"`
	Transaction string                     `json:"transaction"`
	SessionID   *int64                     `json:"session_id,omitempty"`
	HandleID    *int64                     `json:"handle_id,omitempty"`
	Plugin      string                     `json:"plugin,omitempty"`
	Body        any                        `json:"body,omitempty"`
	JSEP        *webrtc.SessionDescription `json:"jsep,omitempty"`
	Candidate   *JanusCandidate            `json:"candidate,omitempty"`
	Error       *JanusError                `json:"error,omitempty"`
	Reason      string                     `json:"reason,omitempty"` // hangup
	Data        *JanusData                 `json:"data,omitempty"`
	PluginData  *JanusPluginData           `json:"plugindata,omitempty"`
}

// JanusPluginData carries a plugin's reply to a "message" request.
type JanusPluginData struct {
	Plugin string              `json:"plugin"`
	Data   JanusStreamingReply `json:"data"`
}

// JanusStreamingReply is the streaming plugin's reply. Errors are reported
// here, in an "event" or "success" message, not in the top-level error field.
type JanusStreamingReply struct {
	Streaming string `json:"streaming"`
	ErrorCode int    `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
	List      []any  `json:"list,omitempty"`
}

// pluginError returns the plugin error carried by msg, or nil.
func (msg *JanusMessage) pluginError() error {
	if msg.PluginData == nil || msg.PluginData.Data.ErrorCode == 0 && msg.PluginData.Data.Error == "" {
		return nil
	}
	d := msg.PluginData.Data
	return fmt.Errorf("%s (code %d)", d.Error, d.ErrorCode)
}

// JanusData carries the numeric ID returned by create/attach responses.
type JanusData struct {
	ID int64 `json:"id"`
}

// JanusError carries the error code and reason from a Janus error response.
type JanusError struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// JanusCandidate is a trickle ICE candidate in Janus format, or the end of
// candidates ({"completed": true}).
type JanusCandidate struct {
	Candidate     string  `json:"candidate,omitempty"`
	SdpMid        *string `json:"sdpMid,omitempty"`
	SdpMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
	Completed     bool    `json:"completed,omitempty"`
}

// JanusBody is the body field for streaming plugin requests (watch, list, start).
type JanusBody struct {
	Request string `json:"request"`
	ID      any    `json:"id,omitempty"`
}
