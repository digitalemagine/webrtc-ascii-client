//go:build unix

package controls

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// KeyboardHandler reads keys from the controlling terminal (/dev/tty) and turns
// them into commands. It puts the terminal into a raw input mode but leaves
// output processing (OPOST) alone, so renderers can keep writing "\n".
type KeyboardHandler struct {
	commands chan Command

	// Set by Start; a handler that was never started has nothing to restore.
	started  bool
	fd       int // /dev/tty
	orig     *unix.Termios
	raw      unix.Termios
	quit     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	paused   atomic.Bool // between Pause and Resume: leave the terminal alone
}

// NewKeyboardHandler creates a new keyboard handler
func NewKeyboardHandler() (*KeyboardHandler, error) {
	return &KeyboardHandler{
		commands: make(chan Command, 10),
	}, nil
}

// Start switches the terminal to raw input and begins reading keys in a goroutine.
func (k *KeyboardHandler) Start() error {
	fd, err := unix.Open("/dev/tty", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/tty: %w", err)
	}

	orig, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("failed to read terminal attributes: %w", err)
	}

	raw := *orig
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	// ISIG stays on: Ctrl-C raises SIGINT, which main already handles.
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	// Return from read after at most 100 ms without input. The timeout lets
	// readLoop notice Stop and tells a lone Esc apart from an escape sequence.
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1

	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &raw); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("failed to set terminal attributes: %w", err)
	}

	k.started = true
	k.fd = fd
	k.orig = orig
	k.raw = raw
	k.quit = make(chan struct{})
	k.done = make(chan struct{})
	go k.readLoop()
	return nil
}

// Stop ends the read loop and restores the terminal. It is idempotent and
// safe to call without Start.
func (k *KeyboardHandler) Stop() error {
	var err error
	k.stopOnce.Do(func() {
		if !k.started {
			return
		}
		close(k.quit)
		<-k.done
		err = unix.IoctlSetTermios(k.fd, ioctlSetTermios, k.orig)
		err = errors.Join(err, unix.Close(k.fd))
	})
	return err
}

// Commands returns the command channel
func (k *KeyboardHandler) Commands() <-chan Command {
	return k.commands
}

// Pause gives the terminal its original settings back until Resume, for the
// time the process is stopped with Ctrl-Z.
func (k *KeyboardHandler) Pause() {
	if !k.started {
		return
	}
	k.paused.Store(true)
	_ = unix.IoctlSetTermios(k.fd, ioctlSetTermios, k.orig)
}

// Resume returns to raw input after Pause.
func (k *KeyboardHandler) Resume() {
	if !k.started {
		return
	}
	_ = unix.IoctlSetTermios(k.fd, ioctlSetTermios, &k.raw)
	k.paused.Store(false)
}

// readLoop reads from the terminal until Stop is called.
func (k *KeyboardHandler) readLoop() {
	defer close(k.done)

	var p keyParser
	buf := make([]byte, 64)
	for {
		select {
		case <-k.quit:
			return
		default:
		}

		// Wait with select rather than rely on VTIME alone: if another process
		// resets the terminal to line mode, a read would block until Enter.
		// (Not poll: macOS doesn't support it on terminal devices.)
		var rset unix.FdSet
		rset.Set(k.fd)
		tv := unix.NsecToTimeval(int64(100 * time.Millisecond))
		ready, err := unix.Select(k.fd+1, &rset, nil, nil, &tv)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return
		}
		n := 0
		if ready > 0 {
			n, err = unix.Read(k.fd, buf)
			if err != nil && !errors.Is(err, unix.EINTR) && !errors.Is(err, unix.EAGAIN) {
				return
			}
		} else {
			k.keepRaw()
		}

		var cmds []Command
		if n > 0 {
			cmds = p.feed(buf[:n])
		} else {
			cmds = p.flush()
		}
		for _, cmd := range cmds {
			select {
			case k.commands <- cmd:
			case <-k.quit:
				return
			}
		}
	}
}

// keepRaw puts the terminal back into raw input mode if another process
// changed it. ffmpeg, the usual producer in "ffmpeg … | termplay -i", saves
// the terminal settings when it starts and restores them when it exits,
// which turns line mode back on under us. Keys typed meanwhile are kept.
func (k *KeyboardHandler) keepRaw() {
	if k.paused.Load() {
		return
	}
	cur, err := unix.IoctlGetTermios(k.fd, ioctlGetTermios)
	if err != nil {
		return
	}
	if cur.Lflag == k.raw.Lflag && cur.Iflag == k.raw.Iflag &&
		cur.Cc[unix.VMIN] == k.raw.Cc[unix.VMIN] && cur.Cc[unix.VTIME] == k.raw.Cc[unix.VTIME] {
		return
	}
	slog.Debug("terminal settings changed by another process; restoring raw input")
	_ = unix.IoctlSetTermios(k.fd, ioctlSetTermios, &k.raw)
}
