//go:build !unix

package player

import (
	"context"

	"github.com/digitalemagine/webrtc-ascii-client/internal/display"
)

// suspendOnCtrlZ does nothing: there is no job control to cooperate with.
func suspendOnCtrlZ(context.Context, *display.Display, func(), func()) {}
