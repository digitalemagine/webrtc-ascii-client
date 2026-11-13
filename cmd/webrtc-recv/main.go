// Command webrtc-recv receives a WebRTC video track and writes it as an H.264
// Annex-B byte stream, to stdout or a file, for ffmpeg, ffplay, GStreamer or
// a recording:
//
//	webrtc-recv http://localhost:8889/cam/whep > cam.h264
//	webrtc-recv -adapter janus ws://localhost:8188 1 | ffplay -f h264 -fflags nobuffer -
//
// It speaks the same signaling as webrtc-ascii-client (Simple, Janus, WHEP)
// and needs no C libraries.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters"
	"github.com/digitalemagine/webrtc-ascii-client/internal/client"
	"github.com/digitalemagine/webrtc-ascii-client/internal/cliflags"
	"github.com/digitalemagine/webrtc-ascii-client/internal/version"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "webrtc-recv:", err)
		os.Exit(1)
	}
}

type options struct {
	adapter     string
	output      string
	verbosity   int
	stun        client.STUNFlag
	showVersion bool
}

func parseFlags() *options {
	o := &options{}
	for _, n := range []string{"adapter", "a"} {
		flag.StringVar(&o.adapter, n, "auto", "Signaling adapter: auto, "+strings.Join(adapters.Kinds(), ", "))
	}
	for _, n := range []string{"o", "output"} {
		flag.StringVar(&o.output, n, "", "Write to this file instead of stdout")
	}
	for _, n := range []string{"verbosity", "V"} {
		flag.IntVar(&o.verbosity, n, 0, "Log to stderr: 0 warnings and errors, 1 info, 2 debug")
	}
	flag.Var(&o.stun, "stun", o.stun.Usage())
	for _, n := range []string{"version", "v"} {
		flag.BoolVar(&o.showVersion, n, false, "Show the version")
	}
	flag.Usage = usage
	flag.Parse()
	return o
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `webrtc-recv %s: receive WebRTC video as H.264 (Annex-B)

Usage:
  webrtc-recv [options] <server_url> [<session_id>] > out.h264

Options must come before the server URL. Simple and Janus need a session ID
(room or mountpoint); WHEP identifies the stream by its URL. The output
starts at the first keyframe. Logs go to stderr.

Examples:
  webrtc-recv http://localhost:8889/mystream/whep > stream.h264
  webrtc-recv -adapter janus ws://localhost:8188 1 | ffplay -f h264 -fflags nobuffer -
  webrtc-recv ws://localhost:8080/ws myroom | ffmpeg -f h264 -i - -c copy out.mp4

Options:
`, version.Get().Display())
	cliflags.PrintDefaults(out, flag.CommandLine)
}

func run() error {
	o := parseFlags()
	setupLogging(o.verbosity)
	if o.showVersion {
		fmt.Println("webrtc-recv " + version.Get().Display())
		return nil
	}

	target, err := client.ParseTarget(o.adapter, flag.Args())
	if err != nil {
		flag.Usage()
		return err
	}
	c, err := client.NewWebRTCClient(target.Kind, target.URL, target.SessionID)
	if err != nil {
		return err
	}
	o.stun.Apply(c)

	out, err := openOutput(o.output)
	if err != nil {
		return err
	}

	// A reader that goes away makes writes fail with EPIPE, so that the
	// signaling session is cleaned up, instead of SIGPIPE killing the process.
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("Starting", "url", target.URL, "session", target.SessionID, "adapter", target.Kind)
	err = receive(ctx, c, out)
	if cerr := out.Close(); err == nil && cerr != nil && out != os.Stdout {
		err = cerr
	}
	return err
}

// openOutput returns stdout, or the file to create for -o ("-" is stdout).
// Binary video on a terminal is of no use, so stdout must be redirected.
func openOutput(path string) (io.WriteCloser, error) {
	if path != "" && path != "-" {
		return os.Create(path)
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, errors.New("stdout is a terminal: redirect it (> file, | ffplay -) or use -o")
	}
	return os.Stdout, nil
}

// setupLogging sends logs to stderr: warnings and errors, and with -V 1 or
// -V 2 also info and debug. stdout carries the stream only.
func setupLogging(verbosity int) {
	level := slog.LevelWarn
	switch {
	case verbosity >= 2:
		level = slog.LevelDebug
	case verbosity == 1:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}
