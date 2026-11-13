package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/pion/webrtc/v4"
)

// createSession sends a Janus "create" request and stores the returned session ID.
func (a *JanusAdapter) createSession(ctx context.Context) error {
	transaction := a.generateTransaction()
	respCh := a.registerTransaction(transaction)
	defer a.removeTransaction(transaction)

	msg := JanusMessage{
		Janus:       "create",
		Transaction: transaction,
	}

	slog.Debug("Janus: create session", "transaction", transaction)

	if err := a.sendJSON(msg); err != nil {
		return err
	}

	select {
	case resp := <-respCh:
		slog.Debug("Janus: create session reply", "janus", resp.Janus)
		if resp.Janus != "success" {
			if resp.Error != nil {
				return fmt.Errorf("failed to create session: %s (code %d)", resp.Error.Reason, resp.Error.Code)
			}
			return fmt.Errorf("failed to create session: %s", resp.Janus)
		}

		switch {
		case resp.SessionID != nil:
			a.sessionID.Store(*resp.SessionID)
			slog.Info("Janus: session created", "session", *resp.SessionID)
		case resp.Data != nil:
			a.sessionID.Store(resp.Data.ID)
			slog.Info("Janus: session created", "session", resp.Data.ID)
		default:
			return errors.New("no session ID returned in response")
		}

		return nil
	case err := <-a.lost:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New("timeout waiting for create session response")
	}
}

// attachPlugin sends a Janus "attach" request for the streaming plugin and stores the handle ID.
func (a *JanusAdapter) attachPlugin(ctx context.Context) error {
	transaction := a.generateTransaction()
	respCh := a.registerTransaction(transaction)
	defer a.removeTransaction(transaction)

	msg := JanusMessage{
		Janus:       "attach",
		Transaction: transaction,
		SessionID:   new(a.sessionID.Load()),
		Plugin:      "janus.plugin.streaming",
	}

	slog.Debug("Janus: attach streaming plugin", "transaction", transaction)

	if err := a.sendJSON(msg); err != nil {
		return err
	}

	select {
	case resp := <-respCh:
		slog.Debug("Janus: attach reply", "janus", resp.Janus)
		if resp.Janus != "success" {
			if resp.Error != nil {
				return fmt.Errorf("failed to attach plugin: %s (code %d)", resp.Error.Reason, resp.Error.Code)
			}
			return fmt.Errorf("failed to attach plugin: %s", resp.Janus)
		}

		switch {
		case resp.HandleID != nil:
			a.handleID.Store(*resp.HandleID)
			slog.Info("Janus: streaming plugin attached", "handle", *resp.HandleID)
		case resp.Data != nil:
			a.handleID.Store(resp.Data.ID)
			slog.Info("Janus: streaming plugin attached", "handle", resp.Data.ID)
		default:
			return errors.New("no handle ID returned in response")
		}

		return nil
	case err := <-a.lost:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New("timeout waiting for attach plugin response")
	}
}

// listStreams queries the available streams for diagnostic logging.
// A failure is not fatal: the caller only logs it.
func (a *JanusAdapter) listStreams(ctx context.Context) error {
	transaction := a.generateTransaction()
	respCh := a.registerTransaction(transaction)
	defer a.removeTransaction(transaction)

	msg := JanusMessage{
		Janus:       "message",
		Transaction: transaction,
		SessionID:   new(a.sessionID.Load()),
		HandleID:    new(a.handleID.Load()),
		Body: JanusBody{
			Request: "list",
		},
	}

	slog.Debug("Janus: list streams")

	if err := a.sendJSON(msg); err != nil {
		return fmt.Errorf("failed to send list streams message: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return fmt.Errorf("failed to list streams: %s", resp.Error.Reason)
		}
		if err := resp.pluginError(); err != nil {
			return fmt.Errorf("failed to list streams: %w", err)
		}
		if resp.PluginData != nil {
			slog.Info("Janus: available streams", "streams", resp.PluginData.Data.List)
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		slog.Debug("Janus: no reply to list streams")
	}

	return nil
}

// watchStream sends a "watch" request for the stream and returns the SDP
// offer from the plugin's reply. Janus first acks the request, then sends an
// "event" whose plugindata holds either an error (e.g. 455, no such stream)
// or the result, together with the offer.
func (a *JanusAdapter) watchStream(ctx context.Context) (webrtc.SessionDescription, error) {
	transaction := a.generateTransaction()
	respCh := a.registerTransaction(transaction)
	defer a.removeTransaction(transaction)

	streamID := a.streamID

	// Try to convert stream ID to integer, fall back to string if it fails
	var streamIDValue any
	if intID, err := strconv.Atoi(streamID); err == nil {
		streamIDValue = intID
	} else {
		streamIDValue = streamID
	}

	msg := JanusMessage{
		Janus:       "message",
		Transaction: transaction,
		SessionID:   new(a.sessionID.Load()),
		HandleID:    new(a.handleID.Load()),
		Body: JanusBody{
			Request: "watch",
			ID:      streamIDValue,
		},
	}

	slog.Debug("Janus: watch", "stream", streamIDValue, "transaction", transaction)

	if err := a.sendJSON(msg); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("failed to send watch message: %w", err)
	}

	// Wait for the plugin's reply (handleMessages skips the ack)
	select {
	case resp := <-respCh:
		slog.Debug("Janus: watch reply", "janus", resp.Janus, "jsep", resp.JSEP != nil)
		if resp.Janus == "error" {
			if resp.Error != nil {
				return webrtc.SessionDescription{}, fmt.Errorf("janus watch error: %s (code %d)", resp.Error.Reason, resp.Error.Code)
			}
			return webrtc.SessionDescription{}, errors.New("janus watch failed with unknown error")
		}
		if err := resp.pluginError(); err != nil {
			return webrtc.SessionDescription{}, fmt.Errorf("janus watch of stream %s failed: %w", streamID, err)
		}
		if resp.JSEP == nil || resp.JSEP.Type != webrtc.SDPTypeOffer {
			return webrtc.SessionDescription{}, fmt.Errorf("janus watch of stream %s: reply has no offer", streamID)
		}
		slog.Info("Janus: watching stream", "stream", streamID)
		return *resp.JSEP, nil
	case err := <-a.lost:
		return webrtc.SessionDescription{}, err
	case <-ctx.Done():
		return webrtc.SessionDescription{}, ctx.Err()
	case <-time.After(10 * time.Second):
		return webrtc.SessionDescription{}, errors.New("timeout waiting for watch response")
	}
}
