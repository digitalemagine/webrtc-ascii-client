// Package player runs a frame source through the pipeline to the terminal:
// display, pipeline, interactive controls, signals and shutdown. Both
// webrtc-ascii-client and termplay use it.
package player

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
	"github.com/digitalemagine/webrtc-ascii-client/internal/controls"
	"github.com/digitalemagine/webrtc-ascii-client/internal/display"
	"github.com/digitalemagine/webrtc-ascii-client/internal/logging"
	"github.com/digitalemagine/webrtc-ascii-client/internal/pipeline"
)

// Options configure Play.
type Options struct {
	// Title starts the status line, e.g. "termplay v0.1.0".
	Title string
	// Interactive enables the keyboard controls.
	Interactive bool
	// Adaptive and TargetFPS: see pipeline.Options.
	Adaptive  bool
	TargetFPS float64
}

// Source produces frames for the pipeline until it ends or ctx is
// canceled; it should return ctx.Err() (or nil) when canceled.
type Source func(ctx context.Context, p *pipeline.Pipeline) error

// Play shows frames from source until it ends, it fails, or the user quits
// (Ctrl-C, SIGTERM, or the quit key with Options.Interactive). The terminal
// is restored before it returns.
func Play(cfg encode.Config, o Options, statusLog *logging.StatusHandler, source Source) error {
	stdoutFD := int(os.Stdout.Fd())
	var pipe *pipeline.Pipeline
	disp := display.New(os.Stdout, display.Options{
		Terminal: term.IsTerminal(stdoutFD),
		Size: func() (int, int, bool) {
			cols, rows, err := term.GetSize(stdoutFD)
			return cols, rows, err == nil
		},
		Title:   o.Title,
		Dropped: func() int64 { return pipe.Dropped() },
	})
	defer disp.Close()

	pipe, err := pipeline.New(cfg, disp, pipeline.Options{Adaptive: o.Adaptive, TargetFPS: o.TargetFPS})
	if err != nil {
		return err
	}
	statusLog.SetSink(disp.Warn)
	defer statusLog.SetSink(nil)
	if cfg.Mode == encode.Sextant {
		sextantNotice(func() { fmt.Fprintln(os.Stderr, "Note:", encode.SextantFontNotice) })
	}

	// Ctrl-C, SIGTERM and the quit key all cancel ctx.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The controls fit the picture to the terminal when it is resized and,
	// with o.Interactive, read the keys. Without keys they are optional: they
	// can't start when stdout is not a terminal, and then have nothing to do.
	pauseKeys, resumeKeys := func() {}, func() {}
	controlMgr := controls.NewControlManager(cfg)
	ctrlErr := controlMgr.Start(controls.Handlers{
		Reconfigure: func(c encode.Config) error {
			if c.Mode == encode.Sextant {
				// stderr is not visible during play: use the status line.
				sextantNotice(func() { disp.Note(encode.SextantFontNotice) })
			}
			return pipe.Reconfigure(c)
		},
		ToggleHelp: disp.ToggleHelp,
	}, o.Interactive)
	switch {
	case ctrlErr == nil:
		defer controlMgr.Stop()
	case o.Interactive:
		controlMgr.Stop()
		return fmt.Errorf("failed to start interactive controls: %w", ctrlErr)
	default:
		controlMgr.Stop()
		slog.Debug("Not following terminal resizes", "error", ctrlErr)
	}
	if ctrlErr == nil && o.Interactive {
		pauseKeys, resumeKeys = controlMgr.PauseKeyboard, controlMgr.ResumeKeyboard
		go func() {
			select {
			case <-controlMgr.QuitChan():
				stop()
			case <-ctx.Done():
			}
		}()
	}

	go suspendOnCtrlZ(ctx, disp, pauseKeys, resumeKeys)

	pipeDone := make(chan error, 1)
	go func() { pipeDone <- pipe.Run(ctx) }()
	srcDone := make(chan error, 1)
	go func() { srcDone <- source(ctx, pipe) }()

	var srcErr error
	select {
	case srcErr = <-srcDone:
	case err := <-pipeDone:
		stop()
		_ = waitWithTimeout(srcDone) // the pipeline error is the one to report
		return err
	case <-ctx.Done():
		slog.Info("Shutting down")
		// The source closes the peer connection and the signaling session on
		// its way out; give it a moment, but don't hang on an unresponsive server.
		srcErr = waitWithTimeout(srcDone)
	}
	stop()
	pipeErr := <-pipeDone // the display has restored the terminal
	if srcErr != nil && !errors.Is(srcErr, context.Canceled) {
		return srcErr
	}
	return pipeErr
}

// shutdownTimeout bounds how long main waits for the source to clean up
// after Ctrl-C or the quit key.
const shutdownTimeout = 3 * time.Second

func waitWithTimeout(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(shutdownTimeout):
		slog.Warn("Shutdown timed out; exiting anyway")
		return nil
	}
}

// sextantNotice tells the user, once, what the sextant renderer needs: show
// writes encode.SextantFontNotice where the user can see it. It is advice,
// not a warning about the stream, so it is not logged.
func sextantNotice(show func()) { sextantNoticeOnce.Do(show) }

var sextantNoticeOnce sync.Once
