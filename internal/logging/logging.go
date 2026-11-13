// Package logging sets up log/slog for the client.
//
// Frames are drawn on stdout, so log output must not go there. Records go to
// two places:
//   - the status panel under the video: warnings and errors only, so the
//     panel's error counter counts problems, not every log line;
//   - stderr, when -V asks for it: 1 shows Info and above, 2 adds Debug.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
)

// StatusSink receives one line per warning or error for the status panel.
type StatusSink func(line string)

// Setup installs the default slog logger (which also captures the standard
// log package) and returns the handler whose status sink can be changed when
// the renderer changes.
func Setup(verbosity int, stderr io.Writer) *StatusHandler {
	status := NewStatusHandler()
	handlers := []slog.Handler{status}
	if verbosity >= 1 {
		level := slog.LevelInfo
		if verbosity >= 2 {
			level = slog.LevelDebug
		}
		handlers = append(handlers, slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	}
	slog.SetDefault(slog.New(slog.NewMultiHandler(handlers...)))
	return status
}

// StatusHandler forwards warnings and errors to a StatusSink as single lines
// ("message key=value ..."). Without a sink, records are dropped.
type StatusHandler struct {
	sink   *atomic.Pointer[StatusSink] // shared by handlers derived with WithAttrs/WithGroup
	prefix string                      // attributes added with WithAttrs, already formatted
	group  string                      // current group, "a.b." form
}

// NewStatusHandler returns a handler without a sink.
func NewStatusHandler() *StatusHandler {
	return &StatusHandler{sink: new(atomic.Pointer[StatusSink])}
}

// SetSink sets where status lines go; nil drops them. Safe to call while logging.
func (h *StatusHandler) SetSink(sink StatusSink) {
	if sink == nil {
		h.sink.Store(nil)
		return
	}
	h.sink.Store(&sink)
}

// Enabled implements slog.Handler: warnings and errors only.
func (h *StatusHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

// Handle implements slog.Handler.
func (h *StatusHandler) Handle(_ context.Context, r slog.Record) error {
	sink := h.sink.Load()
	if sink == nil {
		return nil
	}
	var b strings.Builder
	b.WriteString(r.Message)
	b.WriteString(h.prefix)
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&b, h.group, a)
		return true
	})
	(*sink)(b.String())
	return nil
}

func appendAttr(b *strings.Builder, group string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	b.WriteByte(' ')
	b.WriteString(group)
	b.WriteString(a.Key)
	b.WriteByte('=')
	b.WriteString(a.Value.Resolve().String())
}

// WithAttrs implements slog.Handler.
func (h *StatusHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(h.prefix)
	for _, a := range attrs {
		appendAttr(&b, h.group, a)
	}
	h2 := *h
	h2.prefix = b.String()
	return &h2
}

// WithGroup implements slog.Handler.
func (h *StatusHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := *h
	h2.group = h.group + name + "."
	return &h2
}
