package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// baseAdapter is the WebSocket connection and keepalive shared by the Simple
// and Janus sessions.
type baseAdapter struct {
	// WebSocket and synchronization
	wsConn *websocket.Conn
	wsMu   sync.Mutex

	// Lifecycle
	closeMu        sync.Mutex
	closeRequested bool
	closeOnce      sync.Once

	// Timing. readTimeout is how long a connection may stay silent (no pong
	// or message) before reads fail; keepaliveInterval is the ping period.
	readTimeout       time.Duration
	keepaliveInterval time.Duration

	// Keepalive
	keepaliveTicker *time.Ticker
	keepaliveDone   chan struct{}
	keepaliveOnce   sync.Once

	name string
}

// newBaseAdapter creates a new baseAdapter instance
func newBaseAdapter(name string) baseAdapter {
	return baseAdapter{
		name:              name,
		readTimeout:       120 * time.Second,
		keepaliveInterval: 30 * time.Second,
	}
}

// connectWebSocket establishes the WebSocket connection with standard configuration,
// requesting the given subprotocols (nil for none).
//
// baseAdapter is single-use: after close() this returns an error. Make a new
// session with NewSession to reconnect.
func (b *baseAdapter) connectWebSocket(ctx context.Context, wsURL string, subprotocols []string) error {
	if b.isCloseRequested() {
		return fmt.Errorf("%s: adapter already closed; create a new instance to reconnect", b.name)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
		Subprotocols:     subprotocols,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	// Set connection timeouts
	if err := conn.SetReadDeadline(time.Now().Add(b.readTimeout)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to set read deadline: %w", err)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Enable pong handler for keepalive
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(b.readTimeout))
	})

	b.wsMu.Lock()
	b.wsConn = conn
	b.wsMu.Unlock()

	return nil
}

// sendJSON sends a JSON message over the WebSocket with mutex protection
func (b *baseAdapter) sendJSON(msg any) error {
	b.wsMu.Lock()
	defer b.wsMu.Unlock()

	if b.wsConn == nil {
		return errors.New("websocket connection not established")
	}

	// Set write deadline for each message
	if err := b.wsConn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	return b.wsConn.WriteJSON(msg)
}

// sendWebSocketMessage sends a WebSocket control message with mutex protection
func (b *baseAdapter) sendWebSocketMessage(messageType int, data []byte) error {
	b.wsMu.Lock()
	defer b.wsMu.Unlock()

	if b.wsConn == nil {
		return errors.New("websocket connection not established")
	}

	if err := b.wsConn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	return b.wsConn.WriteMessage(messageType, data)
}

// startKeepalive starts the keepalive goroutine: every keepaliveInterval it
// sends a WebSocket ping, then the application-level ping if appPingFunc is
// not nil.
func (b *baseAdapter) startKeepalive(appPingFunc func() error) {
	b.startKeepaliveWith(func() error {
		if err := b.sendWebSocketMessage(websocket.PingMessage, nil); err != nil {
			return fmt.Errorf("WebSocket ping: %w", err)
		}
		// Don't extend the read deadline here: only the pong handler may,
		// since a pong is the proof that the peer is alive.
		if appPingFunc != nil {
			if err := appPingFunc(); err != nil {
				return fmt.Errorf("keepalive message: %w", err)
			}
		}
		return nil
	})
}

// startKeepaliveWith runs ping every keepaliveInterval until stopKeepalive,
// close, or a failed ping. keepaliveTicker is initialized here (before the
// goroutine starts) so that stopKeepalive can read it without a data race.
func (b *baseAdapter) startKeepaliveWith(ping func() error) {
	b.keepaliveDone = make(chan struct{})
	b.keepaliveTicker = time.NewTicker(b.keepaliveInterval)
	go b.keepaliveLoop(ping)
}

func (b *baseAdapter) keepaliveLoop(ping func() error) {
	defer b.keepaliveTicker.Stop()

	for {
		select {
		case <-b.keepaliveTicker.C:
			if b.isCloseRequested() {
				return
			}
			if err := ping(); err != nil {
				slog.Warn("Keepalive failed", "adapter", b.name, "error", err)
				return
			}
		case <-b.keepaliveDone:
			return
		}
	}
}

// stopKeepalive stops the keepalive goroutine
func (b *baseAdapter) stopKeepalive() {
	b.keepaliveOnce.Do(func() {
		if b.keepaliveDone != nil {
			close(b.keepaliveDone)
		}
		if b.keepaliveTicker != nil {
			b.keepaliveTicker.Stop()
		}
	})
}

// closeTimeout bounds the write of the WebSocket close frame during shutdown.
const closeTimeout = 2 * time.Second

// close cleans up the adapter resources
func (b *baseAdapter) close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		b.setCloseRequested()
		b.stopKeepalive()

		b.wsMu.Lock()
		if b.wsConn != nil {
			// Send the close frame under its own deadline: the one left by
			// the last message has usually passed by now.
			msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			if err := b.wsConn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(closeTimeout)); err != nil {
				slog.Debug("Failed to write WebSocket close frame", "adapter", b.name, "error", err)
			}
			closeErr = b.wsConn.Close()
		}
		b.wsMu.Unlock()
	})
	return closeErr
}

// isCloseRequested returns whether close has been requested
func (b *baseAdapter) isCloseRequested() bool {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	return b.closeRequested
}

// setCloseRequested sets the close requested flag
func (b *baseAdapter) setCloseRequested() {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	b.closeRequested = true
}

// String returns the adapter name
func (b *baseAdapter) String() string {
	return b.name
}
