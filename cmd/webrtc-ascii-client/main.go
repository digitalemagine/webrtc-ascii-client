// Command webrtc-ascii-client receives a WebRTC video stream and shows it in
// the terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
	"github.com/digitalemagine/webrtc-ascii-client/internal/adapters"
	"github.com/digitalemagine/webrtc-ascii-client/internal/client"
	"github.com/digitalemagine/webrtc-ascii-client/internal/cliflags"
	"github.com/digitalemagine/webrtc-ascii-client/internal/decoder"
	"github.com/digitalemagine/webrtc-ascii-client/internal/demo"
	"github.com/digitalemagine/webrtc-ascii-client/internal/logging"
	"github.com/digitalemagine/webrtc-ascii-client/internal/pipeline"
	"github.com/digitalemagine/webrtc-ascii-client/internal/player"
	"github.com/digitalemagine/webrtc-ascii-client/internal/version"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// options are the command-line settings.
type options struct {
	width, height int
	ratio         string
	demo          bool
	adapter       string
	listAdapters  bool
	showVersion   bool
	showDetailed  bool
	render        string
	color         bool
	quality       int
	verbosity     int
	adaptive      bool
	targetFPS     float64
	interactive   bool
	stun          client.STUNFlag
}

func parseFlags() *options {
	o := &options{}
	intFlag := func(p *int, value int, usage string, names ...string) {
		for _, n := range names {
			flag.IntVar(p, n, value, usage)
		}
	}
	strFlag := func(p *string, value, usage string, names ...string) {
		for _, n := range names {
			flag.StringVar(p, n, value, usage)
		}
	}
	boolFlag := func(p *bool, usage string, names ...string) {
		for _, n := range names {
			flag.BoolVar(p, n, false, usage)
		}
	}
	intFlag(&o.width, 0, "Output width: characters, or pixels for sixel/iterm2 (default 80 chars / 1280 px, less in a smaller terminal)", "width", "w")
	intFlag(&o.height, 0, "Output height: characters, or pixels for sixel/iterm2 (default 24 chars / 720 px, less in a smaller terminal)", "height", "H")
	flag.StringVar(&o.ratio, "ratio", "", "Aspect ratio such as 16:9; computes the missing dimension")
	boolFlag(&o.demo, "Run the demo: a generated test pattern, no server needed", "demo")
	strFlag(&o.adapter, "auto", "Signaling adapter: auto, "+strings.Join(adapters.Kinds(), ", "), "adapter", "a")
	boolFlag(&o.listAdapters, "List the signaling adapters", "list-adapters")
	boolFlag(&o.showVersion, "Show the version", "version", "v")
	boolFlag(&o.showDetailed, "Show detailed version information", "version-detailed")
	strFlag(&o.render, encode.Classic, "Renderer: "+strings.Join(encode.Modes, ", "), "render", "r")
	boolFlag(&o.color, "Color, for enhanced, blocks, braille and sextant", "color", "c")
	intFlag(&o.quality, 2, "Supersampling for enhanced: 1 fast, 2 normal, 3 high, 4 ultra", "quality", "q")
	intFlag(&o.verbosity, 0, "Log to stderr: 0 none (warnings and errors only in the status line), 1 info, 2 debug", "verbosity", "V")
	boolFlag(&o.adaptive, "Lower or raise enhanced quality to keep up with -target-fps", "adaptive")
	flag.Float64Var(&o.targetFPS, "target-fps", 30, "Frame rate -adaptive aims for")
	boolFlag(&o.interactive, "Keyboard controls (press h for the keys)", "interactive", "i")
	flag.Var(&o.stun, "stun", o.stun.Usage())
	flag.Usage = usage
	flag.Parse()
	return o
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `WebRTC ASCII Video Client %s

Usage:
  webrtc-ascii-client [options] <server_url> [<session_id>]
  webrtc-ascii-client [options] -demo
  webrtc-ascii-client -list-adapters | -version | -version-detailed

Options must come before the server URL. Simple and Janus need a session ID
(room or mountpoint); WHEP identifies the stream by its URL.

Examples:
  webrtc-ascii-client ws://localhost:8080/ws myroom             # Simple WebSocket signaling
  webrtc-ascii-client -adapter janus ws://localhost:8188 1      # Janus streaming plugin, mountpoint 1
  webrtc-ascii-client http://localhost:8889/mystream/whep        # WHEP, e.g. MediaMTX
  webrtc-ascii-client -render sextant -color -i ws://localhost:8080/ws myroom
  webrtc-ascii-client -w 160 -ratio 16:9 ws://localhost:8080/ws myroom
  webrtc-ascii-client -render iterm2 -ratio 16:9 ws://localhost:8080/ws myroom
  webrtc-ascii-client -demo -i
  webrtc-ascii-client -V 2 ws://localhost:8080/ws myroom 2>debug.log

Options:
`, version.Get().Display())
	cliflags.PrintDefaults(out, flag.CommandLine)
}

func run() error {
	o := parseFlags()
	statusLog := logging.Setup(o.verbosity, os.Stderr)

	switch {
	case o.showVersion:
		fmt.Println(version.Get().String())
		return nil
	case o.showDetailed:
		fmt.Println(version.Get().Detailed())
		return nil
	case o.listAdapters:
		listAdapters()
		return nil
	}

	cfg, err := rendererConfig(o)
	if err != nil {
		return err
	}

	// The source of frames: the demo pattern, or a WebRTC connection.
	var source player.Source
	if o.demo {
		slog.Info("Running the demo")
		source = func(ctx context.Context, p *pipeline.Pipeline) error {
			p.Status("Demo")
			demo.Run(ctx, p)
			return nil
		}
	} else {
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
		slog.Info("Starting", "url", target.URL, "session", target.SessionID, "adapter", target.Kind,
			"auto_detected", o.adapter == "auto", "renderer", cfg.Mode, "width", cfg.Width, "height", cfg.Height)
		source = func(ctx context.Context, p *pipeline.Pipeline) error {
			c.SetOutput(decodingOutput{p, o.verbosity})
			if err := c.Connect(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("connection failed: %w", err)
			}
			return nil
		}
	}

	return player.Play(cfg, player.Options{
		Title:       "webrtc-ascii-client " + version.Get().Display(),
		Interactive: o.interactive,
		Adaptive:    o.adaptive,
		TargetFPS:   o.targetFPS,
	}, statusLog, source)
}

// decodingOutput decodes the client's video tracks into the pipeline.
type decodingOutput struct {
	*pipeline.Pipeline
	verbosity int
}

func (o decodingOutput) NewTrack() (client.Track, error) {
	return decoder.NewH264Stream(o.Pipeline, o.verbosity)
}

func listAdapters() {
	fmt.Println("Supported signaling adapters:")
	for _, a := range adapters.Kinds() {
		fmt.Printf("  %s\n", a)
	}
	fmt.Println("\nAdapter descriptions:")
	fmt.Println("  auto   - Detect from the URL: http(s) is WHEP, a path with \"janus\" or port 8188 is Janus, else Simple")
	fmt.Println("  simple - Simple WebSocket signaling (see docs/SIGNALING.md)")
	fmt.Println("  janus  - Janus WebRTC Gateway, streaming plugin")
	fmt.Println("  whep   - WHEP over HTTP, e.g. MediaMTX")
}

// rendererConfig builds the renderer configuration from the flags.
func rendererConfig(o *options) (encode.Config, error) {
	// Zero when stdout is not a terminal: the defaults then stay as they are.
	termCols, termRows, _ := term.GetSize(int(os.Stdout.Fd()))
	width, height, err := calculateDimensions(o.width, o.height, o.ratio, o.render, termCols, termRows)
	if err != nil {
		return encode.Config{}, err
	}
	cfg := encode.Config{Mode: o.render, Color: o.color, Quality: o.quality, Width: width, Height: height}
	return cfg, cfg.Validate()
}

// Approximate terminal cell size in pixels, for the image renderers, and the
// rows the display's status line takes below the picture.
const (
	cellWidthPx  = 10
	cellHeightPx = 20
	statusRows   = 2
)

// getDefaultDimensions returns the size used for a dimension that isn't
// given: 80×24 characters for text renderers, 1280×720 pixels for the image
// renderers, reduced to what a termCols×termRows terminal can show above the
// status line (a picture that doesn't fit scrolls). termCols and termRows
// are 0 when stdout is not a terminal.
func getDefaultDimensions(renderMode string, termCols, termRows int) (width, height int) {
	width, height = 80, 24
	cellW, cellH := 1, 1
	if encode.IsImageMode(renderMode) {
		width, height = 1280, 720
		cellW, cellH = cellWidthPx, cellHeightPx
	}
	if termCols > 0 && termRows > statusRows {
		width = min(width, termCols*cellW)
		height = min(height, (termRows-statusRows)*cellH)
	}
	return width, height
}

// parseRatio parses aspect ratio string like "16:9" into width and height components.
func parseRatio(ratio string) (width, height int, err error) {
	parts := strings.Split(ratio, ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid format, expected W:H")
	}

	w, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || w <= 0 {
		return 0, 0, errors.New("invalid width component")
	}

	h, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || h <= 0 {
		return 0, 0, errors.New("invalid height component")
	}

	return w, h, nil
}

// calculateDimensions handles dimension calculation with ratio and renderer-specific defaults.
// termCols and termRows are the terminal size, or 0 if stdout is not one.
func calculateDimensions(width, height int, ratio, renderMode string, termCols, termRows int) (int, int, error) {
	defaultWidth, defaultHeight := getDefaultDimensions(renderMode, termCols, termRows)
	// The tallest picture the terminal shows above the status line; 0 if unknown.
	maxHeight := 0
	if termRows > statusRows {
		maxHeight = termRows - statusRows
		if encode.IsImageMode(renderMode) {
			maxHeight *= cellHeightPx
		}
	}

	// If no dimensions specified, use defaults
	if width == 0 && height == 0 && ratio == "" {
		return defaultWidth, defaultHeight, nil
	}

	// If ratio specified, calculate dimensions
	if ratio != "" {
		ratioW, ratioH, err := parseRatio(ratio)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid ratio format %q (use format like 16:9, 4:3, 21:9): %w", ratio, err)
		}

		if encode.IsImageMode(renderMode) {
			// Graphic renderers: direct pixel aspect ratio
			switch {
			case width > 0 && height == 0:
				height = (width * ratioH) / ratioW
			case height > 0 && width == 0:
				width = (height * ratioW) / ratioH
			case width == 0 && height == 0:
				// Use default width, calculate height; if that is taller
				// than the terminal, start from the height instead
				width = defaultWidth
				height = (width * ratioH) / ratioW
				if maxHeight > 0 && height > maxHeight {
					height = maxHeight
					width = (height * ratioW) / ratioH
				}
			}
			// If both set, use as-is
		} else {
			// Text-based renderers: adjust for character aspect ratio
			// To display 16:9 content correctly, we need fewer rows because characters are tall
			switch {
			case width > 0 && height == 0:
				// Calculate height considering character aspect (characters are 2x taller)
				height = (width * ratioH * 1) / (ratioW * 2)
			case height > 0 && width == 0:
				// Calculate width considering character aspect
				width = (height * ratioW * 2) / (ratioH * 1)
			case width == 0 && height == 0:
				// Use default width, calculate height with aspect correction
				width = defaultWidth
				height = (width * ratioH * 1) / (ratioW * 2)
				// Taller than the terminal: start from the height instead
				if maxHeight > 0 && height > maxHeight {
					height = maxHeight
					width = (height * ratioW * 2) / (ratioH * 1)
				}
			}
			// If both set, use as-is
		}
	} else {
		// No ratio specified, apply defaults for unset dimensions
		if width == 0 {
			width = defaultWidth
		}
		if height == 0 {
			height = defaultHeight
		}
	}

	return width, height, nil
}
