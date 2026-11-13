// Package pipeline connects decoded frames to the display:
//
//	source (track or demo) ─latest─▶ encode ─latest─▶ display (stdout)
//
// Each stage runs in its own goroutine and hands the next one only its
// newest output, so a slow terminal drops frames instead of building up
// delay. The source asks TargetSize for the pixel size to decode to.
package pipeline

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
	"github.com/digitalemagine/webrtc-ascii-client/internal/display"
	"github.com/digitalemagine/webrtc-ascii-client/internal/latest"
)

// Options configure a Pipeline.
type Options struct {
	// Adaptive lowers or raises the enhanced renderer's quality to keep
	// encoding within the frame budget of TargetFPS.
	Adaptive  bool
	TargetFPS float64
}

// Pipeline is safe for concurrent use: sources call Frame, Status and
// TargetSize from their goroutines, controls call Reconfigure.
type Pipeline struct {
	display *display.Display
	opts    Options
	frames  *latest.Slot[*image.RGBA]
	configs *latest.Slot[encode.Config]
	initial encode.Config

	size atomic.Uint64 // decode size: width<<32 | height
}

// New validates cfg and returns a pipeline that shows frames on d.
func New(cfg encode.Config, d *display.Display, opts Options) (*Pipeline, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &Pipeline{
		display: d,
		opts:    opts,
		frames:  latest.NewSlot[*image.RGBA](),
		configs: latest.NewSlot[encode.Config](),
		initial: cfg,
	}
	p.setSize(cfg)
	return p, nil
}

// Frame queues a decoded frame for encoding; a newer frame replaces it.
func (p *Pipeline) Frame(img *image.RGBA) { p.frames.Put(img) }

// Status shows the connection state in the status line.
func (p *Pipeline) Status(s string) { p.display.SetConnectionStatus(s) }

// TargetSize is the pixel size sources should deliver frames in.
func (p *Pipeline) TargetSize() (width, height int) {
	v := p.size.Load()
	return int(v >> 32), int(v & 0xffffffff)
}

// Dropped is how many decoded frames were replaced before being encoded.
func (p *Pipeline) Dropped() int64 { return p.frames.Dropped() }

// Reconfigure switches renderer, color, quality or size. Frames already
// decoded for the old size are skipped.
func (p *Pipeline) Reconfigure(cfg encode.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	p.setSize(cfg)
	p.configs.Put(cfg)
	return nil
}

func (p *Pipeline) setSize(cfg encode.Config) {
	w, h := cfg.PixelSize()
	p.size.Store(uint64(w)<<32 | uint64(h))
}

// Run runs the encode and display stages until ctx is canceled or one of
// them fails, and returns that failure.
func (p *Pipeline) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	stage := func(run func(context.Context) error) {
		wg.Go(func() {
			if err := run(ctx); err != nil {
				errs <- err
				cancel()
			}
		})
	}
	stage(p.encodeLoop)
	stage(p.display.Run)
	wg.Wait()
	close(errs)
	return <-errs // nil if no stage failed
}

func (p *Pipeline) encodeLoop(ctx context.Context) error {
	cfg := p.initial
	enc, err := encode.New(cfg)
	if err != nil {
		return err
	}
	clear := true
	ctrl := newQualityController(cfg, p.opts)
	var lastLen int

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.configs.Ready():
			newCfg, ok := p.configs.TryGet()
			if !ok {
				continue
			}
			if enc, err = encode.New(newCfg); err != nil {
				return fmt.Errorf("pipeline: %w", err) // Reconfigure validated it
			}
			cfg, clear = newCfg, true
			ctrl = newQualityController(cfg, p.opts)
			continue
		case <-p.frames.Ready():
		}
		img, ok := p.frames.TryGet()
		if !ok {
			continue
		}

		start := time.Now()
		data := enc.Encode(make([]byte, 0, lastLen), img)
		if len(data) == 0 {
			continue // decoded for a previous configuration
		}
		lastLen = len(data)
		p.display.Show(display.Frame{Data: data, Rows: cfg.Rows(), Clear: clear, Renderer: describe(cfg)})
		clear = false

		// Adaptive quality changes apply here, keeping the controller (a
		// user's Reconfigure starts a new one).
		if q, change := ctrl.observe(time.Since(start), time.Now()); change {
			cfg.Quality = q
			if enc, err = encode.New(cfg); err != nil {
				return fmt.Errorf("pipeline: %w", err)
			}
			p.setSize(cfg)
			slog.Info("Adaptive quality", "quality", q)
		}
	}
}

// describe names a configuration for the status line.
func describe(c encode.Config) string {
	s := c.Mode
	if encode.HasQuality(c.Mode) {
		s += fmt.Sprintf(" q%d", c.Quality)
	}
	if c.Color && encode.HasColorOption(c.Mode) {
		s += " color"
	}
	return s
}
