//go:build !unix

package controls

import "errors"

// KeyboardHandler is not implemented outside Unix: interactive mode needs a
// Unix terminal.
type KeyboardHandler struct {
	commands chan Command
}

// NewKeyboardHandler returns a handler whose Start fails.
func NewKeyboardHandler() (*KeyboardHandler, error) {
	return &KeyboardHandler{commands: make(chan Command)}, nil
}

// Start reports that interactive mode is not supported.
func (k *KeyboardHandler) Start() error {
	return errors.New("interactive mode is not supported on this platform")
}

// Stop does nothing.
func (k *KeyboardHandler) Stop() error { return nil }

// Commands returns a channel that never delivers.
func (k *KeyboardHandler) Commands() <-chan Command { return k.commands }

// Pause does nothing.
func (k *KeyboardHandler) Pause() {}

// Resume does nothing.
func (k *KeyboardHandler) Resume() {}
