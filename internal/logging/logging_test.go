package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type lines struct {
	mu sync.Mutex
	l  []string
}

func (l *lines) add(s string) { l.mu.Lock(); l.l = append(l.l, s); l.mu.Unlock() }

// Only warnings and errors reach the status panel and its warning counter.
func TestStatusHandlerWarnAndAbove(t *testing.T) {
	var got lines
	h := NewStatusHandler()
	h.SetSink(got.add)
	log := slog.New(h).With("adapter", "janus").WithGroup("g")

	log.Debug("debug")
	log.Info("info")
	log.Warn("lost", "error", "eof")
	log.Error("failed", "code", 455)

	want := []string{"lost adapter=janus g.error=eof", "failed adapter=janus g.code=455"}
	if strings.Join(got.l, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got.l, want)
	}

	h.SetSink(nil)
	log.Error("dropped")
	if len(got.l) != 2 {
		t.Fatal("record delivered after SetSink(nil)")
	}
}

func TestSetupVerbosity(t *testing.T) {
	for v, want := range map[int][]string{
		0: nil,
		1: {"info", "warn"},
		2: {"debug", "info", "warn"},
	} {
		var stderr bytes.Buffer
		Setup(v, &stderr)
		slog.Debug("debug")
		slog.Info("info")
		slog.Warn("warn")
		for _, msg := range []string{"debug", "info", "warn"} {
			has := strings.Contains(stderr.String(), "msg="+msg)
			wanted := strings.Contains(strings.Join(want, ","), msg)
			if has != wanted {
				t.Errorf("-V %d: %q on stderr = %v, want %v", v, msg, has, wanted)
			}
		}
	}
}
