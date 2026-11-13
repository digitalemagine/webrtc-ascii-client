//go:build !unix

package controls

import "os"

// notifyResize does nothing: there is no resize signal outside Unix.
func notifyResize(chan<- os.Signal) {}
