//go:build unix

package player

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/digitalemagine/webrtc-ascii-client/internal/display"
)

// suspendOnCtrlZ handles Ctrl-Z (SIGTSTP) until ctx is canceled: it gives
// the terminal back (main screen, cursor, original input settings), stops
// the process, and takes the terminal again when the process continues.
func suspendOnCtrlZ(ctx context.Context, disp *display.Display, pauseKeys, resumeKeys func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTSTP)
	defer signal.Stop(sig)
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			disp.Suspend(ctx, func() {
				pauseKeys()
				// SIGSTOP can't be caught: this returns when the process
				// is continued (fg, SIGCONT).
				_ = syscall.Kill(syscall.Getpid(), syscall.SIGSTOP)
				resumeKeys()
			})
		}
	}
}
