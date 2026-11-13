package controls

import (
	"os"
	"os/signal"
	"sync"

	"golang.org/x/term"
)

// TerminalSize represents terminal dimensions
type TerminalSize struct {
	Width  int
	Height int
}

// SizePreset represents predefined terminal sizes
type SizePreset int

const (
	SizeSmall      SizePreset = iota // 80×24
	SizeMedium                       // 160×48
	SizeLarge                        // 240×72
	SizeFullscreen                   // Full terminal size
)

// SizePresets maps preset names to dimensions
var SizePresets = map[SizePreset]TerminalSize{
	SizeSmall:  {Width: 80, Height: 24},
	SizeMedium: {Width: 160, Height: 48},
	SizeLarge:  {Width: 240, Height: 72},
}

// TerminalSizeMonitor monitors terminal size changes
type TerminalSizeMonitor struct {
	sizeChanges chan TerminalSize
	sigChan     chan os.Signal
	currentSize TerminalSize
	stopped     bool
	stopMu      sync.Mutex
}

// NewTerminalSizeMonitor creates a new terminal size monitor
func NewTerminalSizeMonitor() *TerminalSizeMonitor {
	return &TerminalSizeMonitor{
		sizeChanges: make(chan TerminalSize, 1),
		sigChan:     make(chan os.Signal, 1),
	}
}

// Start begins monitoring terminal size changes
func (t *TerminalSizeMonitor) Start() error {
	// Get initial size
	size, err := t.GetCurrentSize()
	if err != nil {
		return err
	}
	t.currentSize = size

	// Listen for terminal resizes (SIGWINCH on Unix)
	notifyResize(t.sigChan)

	// Start monitoring goroutine
	go t.monitorLoop()

	return nil
}

// Stop stops monitoring with idempotent check
func (t *TerminalSizeMonitor) Stop() {
	t.stopMu.Lock()
	if t.stopped {
		t.stopMu.Unlock()
		return
	}
	t.stopped = true
	t.stopMu.Unlock()

	signal.Stop(t.sigChan)
	close(t.sigChan)
}

// SizeChanges returns the size change channel
func (t *TerminalSizeMonitor) SizeChanges() <-chan TerminalSize {
	return t.sizeChanges
}

// GetCurrentSize returns the current terminal size
func (t *TerminalSizeMonitor) GetCurrentSize() (TerminalSize, error) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return TerminalSize{}, err
	}
	return TerminalSize{Width: width, Height: height}, nil
}

// monitorLoop watches for terminal resize events
func (t *TerminalSizeMonitor) monitorLoop() {
	for range t.sigChan {
		size, err := t.GetCurrentSize()
		if err != nil {
			continue
		}

		// Only send if size actually changed
		if size.Width != t.currentSize.Width || size.Height != t.currentSize.Height {
			t.currentSize = size
			select {
			case t.sizeChanges <- size:
			default:
				// Drop update if channel is full
			}
		}
	}
}

// NextPreset returns the next size preset in the cycle
func NextPreset(current SizePreset) SizePreset {
	switch current {
	case SizeSmall:
		return SizeMedium
	case SizeMedium:
		return SizeLarge
	case SizeLarge:
		return SizeFullscreen
	case SizeFullscreen:
		return SizeSmall
	default:
		return SizeSmall
	}
}
