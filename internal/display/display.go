// Package display owns the terminal output: it is the only writer to stdout.
//
// It draws each frame from the top-left corner (no full clear, so no
// flicker) inside a synchronized update, followed by a two-line status line,
// and uses the alternate screen so the user's scrollback is left alone.
package display

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/digitalemagine/webrtc-ascii-client/internal/latest"
)

// Terminal control sequences.
const (
	enterAltScreen = "\033[?1049h\033[?25l" // alternate screen, hide cursor
	leaveAltScreen = "\033[0m\033[?25h\033[?1049l"
	beginSync      = "\033[?2026h" // synchronized update: the terminal shows the frame at once
	endSync        = "\033[?2026l"
	home           = "\033[H"
	clearScreen    = "\033[2J"
	clearToEnd     = "\033[J"
	sgrReset       = "\033[0m"
)

// statusRows is the height of the status line.
const statusRows = 2

// statusInterval is how often the status line is redrawn when no frames arrive.
const statusInterval = 250 * time.Millisecond

// Frame is one encoded frame.
type Frame struct {
	Data []byte
	// Rows is how many terminal rows Data occupies (0 if unknown).
	Rows int
	// Clear asks for the screen to be cleared first (the renderer changed).
	Clear bool
	// Renderer describes the renderer for the status line, e.g. "enhanced q2 color".
	Renderer string
}

// Options configure a Display.
type Options struct {
	// Terminal enables the alternate screen and hidden cursor. Set it when
	// the output is a terminal.
	Terminal bool
	// Size returns the terminal size in cells; ok is false if unknown.
	Size func() (cols, rows int, ok bool)
	// Title is the first item of the status line (application and version).
	Title string
	// Dropped reports frames dropped before reaching the display (optional).
	Dropped func() int64
}

// Display draws frames and the status line. Show, SetConnectionStatus,
// Warn and ToggleHelp may be called from any goroutine; Run draws.
type Display struct {
	out    io.Writer
	opts   Options
	frames *latest.Slot[Frame]

	mu         sync.Mutex
	connection string
	lastWarn   string
	warnings   int
	help       bool
	dirty      bool
	frameTimes []time.Time // display times within the last second, for FPS
	shown      int64

	suspend chan func() // Suspend's requests to Run

	closeOnce sync.Once
	entered   bool
}

// New returns a display writing to out.
func New(out io.Writer, opts Options) *Display {
	if opts.Size == nil {
		opts.Size = func() (int, int, bool) { return 0, 0, false }
	}
	return &Display{out: out, opts: opts, frames: latest.NewSlot[Frame](), connection: "Starting", dirty: true,
		suspend: make(chan func())}
}

// Show queues a frame; if the display is busy, a newer frame replaces it.
func (d *Display) Show(f Frame) { d.frames.Put(f) }

// SetConnectionStatus sets the connection state shown in the status line.
func (d *Display) SetConnectionStatus(s string) {
	d.mu.Lock()
	d.connection, d.dirty = s, true
	d.mu.Unlock()
}

// Warn shows a warning in the status line and counts it.
func (d *Display) Warn(line string) {
	d.mu.Lock()
	d.lastWarn, d.dirty = line, true
	d.warnings++
	d.mu.Unlock()
}

// Note shows a line where warnings go, without counting it as one.
func (d *Display) Note(line string) {
	d.mu.Lock()
	d.lastWarn, d.dirty = line, true
	d.mu.Unlock()
}

// Suspend hands the terminal back for the time stop takes: Run leaves the
// alternate screen, calls stop, then takes the screen again and redraws.
// stop is expected to stop the process (Ctrl-Z) and return when it
// continues. Suspend returns when that is done, or when ctx is canceled
// before Run gets to it.
func (d *Display) Suspend(ctx context.Context, stop func()) {
	done := make(chan struct{})
	select {
	case d.suspend <- func() { stop(); close(done) }:
		<-done
	case <-ctx.Done():
	}
}

// ToggleHelp switches the second status row between connection state and
// the interactive keys.
func (d *Display) ToggleHelp() {
	d.mu.Lock()
	d.help, d.dirty = !d.help, true
	d.mu.Unlock()
}

// Run draws until ctx is canceled, then restores the terminal.
func (d *Display) Run(ctx context.Context) error {
	if d.opts.Terminal {
		d.entered = true
		if _, err := io.WriteString(d.out, enterAltScreen); err != nil {
			return fmt.Errorf("display: %w", err)
		}
	}
	defer d.Close()

	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()
	var last Frame
	var buf bytes.Buffer
	for {
		var clear bool
		select {
		case <-ctx.Done():
			return nil
		case <-d.frames.Ready():
			f, ok := d.frames.TryGet()
			if !ok {
				continue
			}
			clear = f.Clear
			last = f
			d.recordFrame(time.Now())
		case <-ticker.C:
			if !d.takeDirty() {
				continue
			}
		case stop := <-d.suspend:
			if d.entered {
				_, _ = io.WriteString(d.out, leaveAltScreen)
			}
			stop()
			if d.entered {
				if _, err := io.WriteString(d.out, enterAltScreen); err != nil {
					return fmt.Errorf("display: %w", err)
				}
			}
			clear = true
		}
		buf.Reset()
		d.compose(&buf, last, clear, time.Now())
		if _, err := d.out.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("display: %w", err)
		}
	}
}

// Close restores the terminal. It is idempotent; Run calls it on return,
// and main defers it in case of an early exit.
func (d *Display) Close() {
	d.closeOnce.Do(func() {
		if d.entered {
			_, _ = io.WriteString(d.out, leaveAltScreen)
		}
	})
}

func (d *Display) takeDirty() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	dirty := d.dirty
	d.dirty = false
	return dirty
}

func (d *Display) recordFrame(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.shown++
	d.frameTimes = append(d.frameTimes, now)
	cut := 0
	for cut < len(d.frameTimes) && now.Sub(d.frameTimes[cut]) > time.Second {
		cut++
	}
	d.frameTimes = d.frameTimes[cut:]
	d.dirty = false
}

// compose writes one screen update: the frame from the top-left corner, then
// the status line, then clears whatever an earlier, larger frame left below.
func (d *Display) compose(buf *bytes.Buffer, f Frame, clear bool, now time.Time) {
	cols, rows, sized := d.opts.Size()
	// Drawing from the corner without clearing only works if nothing
	// scrolls: the frame and the status line must fit.
	if sized && f.Rows > 0 && f.Rows+statusRows > rows {
		clear = true
	}
	if cols <= 0 {
		cols = 80
	}

	buf.WriteString(beginSync)
	if clear {
		buf.WriteString(clearScreen)
	}
	buf.WriteString(home)
	buf.Write(f.Data)
	buf.WriteString(sgrReset)
	if len(f.Data) > 0 && f.Data[len(f.Data)-1] != '\n' {
		buf.WriteByte('\n') // e.g. after Sixel data
	}
	first, second := d.statusLines(f.Renderer, now)
	buf.WriteString(fit(first, cols))
	buf.WriteByte('\n')
	buf.WriteString(fit(second, cols)) // no newline: it would scroll at the bottom
	buf.WriteString(clearToEnd)
	buf.WriteString(endSync)
}

const helpLine = "q quit · h help · c color · +/- quality · f size · " +
	"1-7 classic enhanced blocks braille sextant sixel iterm2"

func (d *Display) statusLines(renderer string, now time.Time) (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fps := "--"
	if n := len(d.frameTimes); n >= 2 {
		if span := d.frameTimes[n-1].Sub(d.frameTimes[0]).Seconds(); span > 0 && now.Sub(d.frameTimes[n-1]) < time.Second {
			fps = fmt.Sprintf("%.1f", float64(n-1)/span)
		}
	}
	var dropped int64
	if d.opts.Dropped != nil {
		dropped = d.opts.Dropped()
	}
	dropped += d.frames.Dropped()

	parts := []string{d.opts.Title}
	if renderer != "" {
		parts = append(parts, renderer)
	}
	parts = append(parts,
		"FPS "+fps,
		fmt.Sprintf("Frames %d", d.shown),
		fmt.Sprintf("Dropped %d", dropped),
		fmt.Sprintf("Warnings %d", d.warnings),
	)
	first := strings.Join(parts, " | ")

	second := d.connection
	if d.help {
		second = helpLine
	} else if d.lastWarn != "" {
		second += " | " + d.lastWarn
	}
	return first, second
}

// fit pads or truncates s to exactly width columns (counting runes).
func fit(s string, width int) string {
	s = strings.Map(func(r rune) rune { // keep control characters out of the status line
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return string([]rune(s)[:width])
}
