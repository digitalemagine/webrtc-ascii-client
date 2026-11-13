//go:build unix

package controls

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize delivers terminal resize signals (SIGWINCH) on c.
func notifyResize(c chan<- os.Signal) { signal.Notify(c, syscall.SIGWINCH) }
