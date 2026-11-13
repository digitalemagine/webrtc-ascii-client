package controls

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
)

// Approximate size of a terminal cell in pixels, for the image renderers
// (sixel, iterm2), whose size is in pixels.
const (
	cellWidthPx  = 10
	cellHeightPx = 20
)

// statusRows is the height of the display's status line, kept free below
// the video.
const statusRows = 2

// Handlers are called from the manager's goroutine.
type Handlers struct {
	// Reconfigure applies a new renderer configuration.
	Reconfigure func(encode.Config) error
	// ToggleHelp shows or hides the key help.
	ToggleHelp func()
}

// ControlManager turns keys and terminal resizes into configuration changes.
// After Start, its state is only touched by its own goroutine.
//
// The picture keeps the size asked for (on the command line or with a size
// preset) when the terminal has room for it, and shrinks to fit the terminal
// above the status line when it hasn't. In the full-terminal preset it
// follows the terminal size.
type ControlManager struct {
	keyboard    *KeyboardHandler
	sizeMonitor *TerminalSizeMonitor
	handlers    Handlers

	// Owned by processCommands after Start.
	config     encode.Config
	cols, rows int          // output size in terminal cells, whatever the renderer
	want       TerminalSize // the size asked for, in cells
	term       TerminalSize // the terminal size; zero if unknown
	preset     SizePreset

	quitChan chan struct{}
	quitOnce sync.Once

	// Shutdown coordination
	stopChan chan struct{}
	stopped  bool
	stopMu   sync.Mutex
	wg       sync.WaitGroup
}

// NewControlManager starts from the configuration chosen on the command line.
func NewControlManager(initial encode.Config) *ControlManager {
	c := &ControlManager{
		config:   initial,
		quitChan: make(chan struct{}),
		stopChan: make(chan struct{}),
	}
	c.cols, c.rows = initial.Width, initial.Height
	if encode.IsImageMode(initial.Mode) {
		c.cols, c.rows = initial.Width/cellWidthPx, initial.Height/cellHeightPx
	}
	c.want = TerminalSize{Width: c.cols, Height: c.rows}
	for p, size := range SizePresets {
		if c.cols == size.Width && c.rows == size.Height {
			c.preset = p
		}
	}
	return c
}

// Start begins watching the terminal size and, with keys, reading the
// keyboard. It fails if stdout is not a terminal. A size given on the command
// line is kept until the first terminal resize, even if it doesn't fit.
func (c *ControlManager) Start(h Handlers, keys bool) error {
	c.handlers = h

	if keys {
		keyboard, err := NewKeyboardHandler()
		if err != nil {
			return fmt.Errorf("failed to create keyboard handler: %w", err)
		}
		c.keyboard = keyboard
		if err := keyboard.Start(); err != nil {
			return fmt.Errorf("failed to start keyboard handler: %w", err)
		}
	}

	c.sizeMonitor = NewTerminalSizeMonitor()
	if err := c.sizeMonitor.Start(); err != nil {
		return fmt.Errorf("failed to start size monitor: %w", err)
	}
	if size, err := c.sizeMonitor.GetCurrentSize(); err == nil {
		c.term = size
	}

	c.wg.Add(1)
	go c.processCommands()
	return nil
}

// Stop stops the control system gracefully.
// Safe to call multiple times and safe to call without Start() (e.g., after partial initialization failure).
func (c *ControlManager) Stop() {
	c.stopMu.Lock()
	if c.stopped {
		c.stopMu.Unlock()
		return
	}
	c.stopped = true
	c.stopMu.Unlock()

	// 1. Signal processCommands to stop
	close(c.stopChan)

	// 2. Stop input sources (may be nil if Start() was never called or failed partway)
	if c.keyboard != nil {
		_ = c.keyboard.Stop()
	}
	if c.sizeMonitor != nil {
		c.sizeMonitor.Stop()
	}

	// 3. Wait for processCommands to exit
	c.wg.Wait()

	// 4. Signal quit. The producers own keyboard.commands and
	// sizeMonitor.sizeChanges; they are not closed here.
	c.quitOnce.Do(func() { close(c.quitChan) })
}

// QuitChan is closed when the user quits or Stop is called.
func (c *ControlManager) QuitChan() <-chan struct{} {
	return c.quitChan
}

// PauseKeyboard restores the terminal's input settings until ResumeKeyboard,
// for the time the process is stopped with Ctrl-Z.
func (c *ControlManager) PauseKeyboard() {
	if c.keyboard != nil {
		c.keyboard.Pause()
	}
}

// ResumeKeyboard returns to raw input after PauseKeyboard.
func (c *ControlManager) ResumeKeyboard() {
	if c.keyboard != nil {
		c.keyboard.Resume()
	}
}

func (c *ControlManager) processCommands() {
	defer c.wg.Done()
	var keys <-chan Command // nil without a keyboard: never ready
	if c.keyboard != nil {
		keys = c.keyboard.Commands()
	}
	for {
		select {
		case <-c.stopChan:
			return
		case cmd := <-keys:
			c.handleCommand(cmd)
		case size := <-c.sizeMonitor.SizeChanges():
			c.terminalResized(size)
		}
	}
}

// terminalResized fits the picture to the new terminal size.
func (c *ControlManager) terminalResized(size TerminalSize) {
	c.term = size
	c.fit()
}

// handleCommand processes a single command
func (c *ControlManager) handleCommand(cmd Command) {
	switch cmd.Type {
	case CmdQuit:
		c.quitOnce.Do(func() { close(c.quitChan) })

	case CmdToggleColor:
		if encode.HasColorOption(c.config.Mode) {
			c.config.Color = !c.config.Color
			c.apply()
		}

	case CmdIncreaseQuality, CmdDecreaseQuality:
		q := c.config.Quality + 1
		if cmd.Type == CmdDecreaseQuality {
			q = c.config.Quality - 1
		}
		if encode.HasQuality(c.config.Mode) && q >= 1 && q <= 4 {
			c.config.Quality = q
			c.apply()
		}

	case CmdSwitchRenderer:
		if idx, ok := cmd.Value.(int); ok && idx >= 0 && idx < len(encode.Modes) {
			c.config.Mode = encode.Modes[idx]
			if c.config.Quality == 0 {
				c.config.Quality = 2
			}
			c.resize(c.cols, c.rows)
		}

	case CmdToggleSize:
		c.preset = NextPreset(c.preset)
		if size, ok := SizePresets[c.preset]; ok {
			c.want = size
		}
		c.fit()

	case CmdShowHelp:
		if c.handlers.ToggleHelp != nil {
			c.handlers.ToggleHelp()
		}
	}
}

// fit sizes the picture for the terminal: the wanted size (the whole
// terminal in the full-terminal preset), limited to the room above the
// status line. While the terminal size is unknown, the wanted size is used
// as it is. Nothing is reconfigured if the size stays the same.
func (c *ControlManager) fit() {
	cols, rows := c.want.Width, c.want.Height
	if c.term.Width > 0 && c.term.Height > statusRows {
		roomCols, roomRows := c.term.Width, c.term.Height-statusRows
		if c.preset == SizeFullscreen {
			cols, rows = roomCols, roomRows
		}
		cols, rows = min(cols, roomCols), min(rows, roomRows)
	}
	if cols != c.cols || rows != c.rows {
		c.resize(cols, rows)
	}
}

// resize sets the output size in cells and applies it in the units of the
// current renderer.
func (c *ControlManager) resize(cols, rows int) {
	c.cols, c.rows = max(cols, 1), max(rows, 1)
	c.config.Width, c.config.Height = c.cols, c.rows
	if encode.IsImageMode(c.config.Mode) {
		c.config.Width, c.config.Height = c.cols*cellWidthPx, c.rows*cellHeightPx
	}
	c.apply()
}

func (c *ControlManager) apply() {
	if c.handlers.Reconfigure == nil {
		return
	}
	if err := c.handlers.Reconfigure(c.config); err != nil {
		slog.Warn("Failed to reconfigure renderer", "error", err)
		return
	}
	slog.Info("Renderer reconfigured", "renderer", c.config.Mode, "width", c.config.Width,
		"height", c.config.Height, "quality", c.config.Quality, "color", c.config.Color)
}
