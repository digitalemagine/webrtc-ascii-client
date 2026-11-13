package controls

import (
	"os"
	"testing"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
)

func newTestControlManager() *ControlManager {
	c := NewControlManager(encode.Config{Mode: encode.Classic, Width: 80, Height: 24, Quality: 2})

	c.keyboard = &KeyboardHandler{commands: make(chan Command, 4)}
	c.sizeMonitor = &TerminalSizeMonitor{
		sizeChanges: make(chan TerminalSize, 4),
		sigChan:     make(chan os.Signal, 1),
	}
	return c
}

func TestHandleCommandQuitIsIdempotent(t *testing.T) {
	t.Parallel()

	c := newTestControlManager()
	c.handleCommand(Command{Type: CmdQuit})
	c.handleCommand(Command{Type: CmdQuit})

	select {
	case <-c.QuitChan():
		// closed as expected
	default:
		t.Fatal("quit channel should be closed")
	}
}

func TestStopWithoutStartDoesNotPanic(t *testing.T) {
	t.Parallel()

	c := NewControlManager(encode.Config{Mode: encode.Classic, Width: 80, Height: 24, Quality: 2})
	c.Stop()
	c.Stop()
}

func TestStopSignalsProcessCommandsAndIsIdempotent(t *testing.T) {
	t.Parallel()

	c := newTestControlManager()
	c.wg.Add(1)
	go c.processCommands()

	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Stop did not return in time")
	}

	doneAgain := make(chan struct{})
	go func() {
		c.Stop()
		close(doneAgain)
	}()

	select {
	case <-doneAgain:
	case <-time.After(1 * time.Second):
		t.Fatal("second Stop did not return in time")
	}

	select {
	case <-c.QuitChan():
		// closed as expected
	default:
		t.Fatal("quit channel should be closed after Stop")
	}
}

// Keys change the configuration; sizes stay in cells and are converted to
// pixels for the image renderers; options a renderer lacks are ignored.
func TestHandleCommandReconfigures(t *testing.T) {
	t.Parallel()

	c := newTestControlManager()
	var got []encode.Config
	c.handlers.Reconfigure = func(cfg encode.Config) error { got = append(got, cfg); return nil }
	helps := 0
	c.handlers.ToggleHelp = func() { helps++ }

	c.handleCommand(Command{Type: CmdToggleColor})               // classic has no color: ignored
	c.handleCommand(Command{Type: CmdSwitchRenderer, Value: 1})  // enhanced
	c.handleCommand(Command{Type: CmdIncreaseQuality})           // q3
	c.handleCommand(Command{Type: CmdToggleColor})               // color on
	c.handleCommand(Command{Type: CmdSwitchRenderer, Value: 5})  // sixel: pixels
	c.handleCommand(Command{Type: CmdSwitchRenderer, Value: 2})  // blocks: back to cells
	c.handleCommand(Command{Type: CmdSwitchRenderer, Value: 99}) // out of range: ignored
	c.handleCommand(Command{Type: CmdShowHelp})

	want := []encode.Config{
		{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 2},
		{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 3},
		{Mode: encode.Enhanced, Width: 80, Height: 24, Quality: 3, Color: true},
		{Mode: encode.Sixel, Width: 800, Height: 480, Quality: 3, Color: true},
		{Mode: encode.Blocks, Width: 80, Height: 24, Quality: 3, Color: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d reconfigurations %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reconfiguration %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if helps != 1 {
		t.Errorf("help toggled %d times, want 1", helps)
	}
}

// An image-mode size from the command line (pixels) becomes cells.
func TestNewControlManagerImageSize(t *testing.T) {
	t.Parallel()

	c := NewControlManager(encode.Config{Mode: encode.ITerm2, Width: 1280, Height: 720})
	if c.cols != 128 || c.rows != 36 {
		t.Fatalf("cells = %dx%d, want 128x36", c.cols, c.rows)
	}
}

// recordReconfigures collects the sizes the manager applies.
func recordReconfigures(c *ControlManager) *[]TerminalSize {
	var got []TerminalSize
	c.handlers.Reconfigure = func(cfg encode.Config) error {
		got = append(got, TerminalSize{Width: cfg.Width, Height: cfg.Height})
		return nil
	}
	return &got
}

func checkSizes(t *testing.T, got, want []TerminalSize) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("reconfigured to %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reconfigured to %v, want %v", got, want)
		}
	}
}

// The picture keeps its size while the terminal has room, shrinks to fit
// above the status line when it hasn't, and grows back afterwards.
func TestTerminalResizedFitsWantedSize(t *testing.T) {
	t.Parallel()

	c := newTestControlManager() // 80×24
	got := recordReconfigures(c)

	c.terminalResized(TerminalSize{Width: 200, Height: 60}) // room: unchanged
	c.terminalResized(TerminalSize{Width: 60, Height: 20})  // 60×18
	c.terminalResized(TerminalSize{Width: 70, Height: 40})  // 70×24
	c.terminalResized(TerminalSize{Width: 120, Height: 50}) // back to 80×24
	c.terminalResized(TerminalSize{Width: 130, Height: 55}) // unchanged
	c.terminalResized(TerminalSize{Width: 1, Height: 2})    // no room: unknown, kept as is

	checkSizes(t, *got, []TerminalSize{{60, 18}, {70, 24}, {80, 24}})
}

// A size from the command line larger than the terminal is kept until the
// terminal is resized; image-renderer sizes are converted from cells.
func TestTerminalResizedShrinksExplicitImageSize(t *testing.T) {
	t.Parallel()

	c := NewControlManager(encode.Config{Mode: encode.Sixel, Width: 1600, Height: 900})
	got := recordReconfigures(c)

	c.terminalResized(TerminalSize{Width: 100, Height: 40})  // 100×38 cells
	c.terminalResized(TerminalSize{Width: 300, Height: 100}) // back to 160×45 cells

	checkSizes(t, *got, []TerminalSize{{1000, 760}, {1600, 900}})
}

// The size presets are limited to the terminal, and the full-terminal preset
// follows resizes.
func TestToggleSizeFitsTerminal(t *testing.T) {
	t.Parallel()

	c := newTestControlManager() // 80×24, preset small
	c.term = TerminalSize{Width: 100, Height: 30}
	got := recordReconfigures(c)

	c.handleCommand(Command{Type: CmdToggleSize})           // medium: 100×28
	c.handleCommand(Command{Type: CmdToggleSize})           // large: same, no change
	c.handleCommand(Command{Type: CmdToggleSize})           // full terminal: same, no change
	c.terminalResized(TerminalSize{Width: 120, Height: 40}) // full terminal follows: 120×38
	c.handleCommand(Command{Type: CmdToggleSize})           // small: 80×24

	checkSizes(t, *got, []TerminalSize{{100, 28}, {120, 38}, {80, 24}})
}

// Without the keyboard (no -i), the manager still follows terminal resizes.
func TestProcessCommandsWithoutKeyboardFollowsResizes(t *testing.T) {
	t.Parallel()

	c := newTestControlManager()
	c.keyboard = nil
	sizes := make(chan TerminalSize, 1)
	c.handlers.Reconfigure = func(cfg encode.Config) error {
		sizes <- TerminalSize{Width: cfg.Width, Height: cfg.Height}
		return nil
	}
	c.wg.Add(1)
	go c.processCommands()
	defer c.Stop()

	c.sizeMonitor.sizeChanges <- TerminalSize{Width: 50, Height: 20}
	select {
	case got := <-sizes:
		if want := (TerminalSize{Width: 50, Height: 18}); got != want {
			t.Fatalf("reconfigured to %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("resize not applied")
	}
}
