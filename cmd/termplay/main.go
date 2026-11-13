// Command termplay plays YUV4MPEG2 (Y4M) video in the terminal, with the
// same renderers as webrtc-ascii-client. ffmpeg turns any source into Y4M:
//
//	ffmpeg -v error -nostdin -re -i talk.mp4 -vf scale=160:96 -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r sextant -c
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
	"github.com/digitalemagine/webrtc-ascii-client/internal/cliflags"
	"github.com/digitalemagine/webrtc-ascii-client/internal/logging"
	"github.com/digitalemagine/webrtc-ascii-client/internal/pipeline"
	"github.com/digitalemagine/webrtc-ascii-client/internal/player"
	"github.com/digitalemagine/webrtc-ascii-client/internal/resize"
	"github.com/digitalemagine/webrtc-ascii-client/internal/version"
	"github.com/digitalemagine/webrtc-ascii-client/internal/y4m"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "termplay:", err)
		os.Exit(1)
	}
}

type options struct {
	render        string
	color         bool
	quality       int
	width, height int
	adaptive      bool
	targetFPS     float64
	interactive   bool
	verbosity     int
	pace          bool
	hold          bool
	printSize     bool
	showVersion   bool
}

func parseFlags() *options {
	o := &options{}
	both := func(set func(name string), names ...string) {
		for _, n := range names {
			set(n)
		}
	}
	both(func(n string) {
		flag.StringVar(&o.render, n, encode.Classic, "Renderer: "+strings.Join(encode.Modes, ", "))
	}, "render", "r")
	both(func(n string) { flag.BoolVar(&o.color, n, false, "Color, for enhanced, blocks, braille and sextant") }, "color", "c")
	both(func(n string) {
		flag.IntVar(&o.quality, n, 2, "Supersampling for enhanced: 1 fast, 2 normal, 3 high, 4 ultra")
	}, "quality", "q")
	both(func(n string) {
		flag.IntVar(&o.width, n, 0, "Output width: characters, or pixels for sixel/iterm2 (default: fit the terminal)")
	}, "width", "w")
	both(func(n string) {
		flag.IntVar(&o.height, n, 0, "Output height: characters, or pixels for sixel/iterm2 (default: from the aspect ratio)")
	}, "height", "H")
	flag.BoolVar(&o.adaptive, "adaptive", false, "Lower or raise enhanced quality to keep up with -target-fps")
	flag.Float64Var(&o.targetFPS, "target-fps", 30, "Frame rate -adaptive aims for")
	both(func(n string) { flag.BoolVar(&o.interactive, n, false, "Keyboard controls (press h for the keys)") }, "interactive", "i")
	both(func(n string) {
		flag.IntVar(&o.verbosity, n, 0, "Log to stderr: 0 none (warnings and errors only in the status line), 1 info, 2 debug")
	}, "verbosity", "V")
	flag.BoolVar(&o.pace, "pace", true, "Show frames at the stream's frame rate (-pace=false: as fast as they arrive)")
	flag.BoolVar(&o.hold, "hold", false, "Keep the last frame on screen at the end of input, until Ctrl-C (or q with -i)")
	flag.BoolVar(&o.printSize, "print-size", false, "Print the pixel size to scale to (W:H, for ffmpeg -vf scale=W:H) for -r, -w and -H, and exit")
	both(func(n string) { flag.BoolVar(&o.showVersion, n, false, "Show the version") }, "version", "v")
	flag.Usage = usage
	flag.Parse()
	return o
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `termplay %s - play YUV4MPEG2 video in the terminal

Usage:
  <producer> | termplay [options]
  termplay [options] file.y4m
  termplay -print-size -r <renderer> -w <width> -H <height>

ffmpeg writes YUV4MPEG2 from any source with -pix_fmt yuv420p -f yuv4mpegpipe; -v error -nostdin
keep it from drawing over the picture and reading keys. termplay scales frames itself; scaling
in ffmpeg to the size -print-size gives keeps the pipe small.

Examples:
  ffmpeg -v error -nostdin -re -i talk.mp4 -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r sextant -c
  ffmpeg -v error -nostdin -i talk.mp4 -vf scale=$(termplay -print-size -r braille -w 80 -H 24) -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r braille -w 80 -H 24
  ffmpeg -v error -nostdin -f avfoundation -framerate 30 -i 0 -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r blocks -c -i
  ffmpeg -v error -nostdin -i photo.jpg -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r iterm2 -hold

Options:
`, version.Get().Display())
	cliflags.PrintDefaults(out, flag.CommandLine)
}

func run() error {
	o := parseFlags()
	statusLog := logging.Setup(o.verbosity, os.Stderr)

	if o.showVersion {
		fmt.Println("termplay " + version.Get().Display())
		return nil
	}
	if o.printSize {
		if o.width <= 0 || o.height <= 0 {
			return errors.New("-print-size needs -w and -H")
		}
		cfg := encode.Config{Mode: o.render, Color: o.color, Quality: o.quality, Width: o.width, Height: o.height}
		if err := cfg.Validate(); err != nil {
			return err
		}
		w, h := cfg.PixelSize()
		fmt.Printf("%d:%d\n", w, h)
		return nil
	}

	in, name, err := openInput(flag.Args())
	if err != nil {
		return err
	}
	defer in.Close()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		flag.Usage()
		return errors.New("no input: pipe YUV4MPEG2 video in, e.g. ffmpeg -i video.mp4 -pix_fmt yuv420p -f yuv4mpegpipe - | termplay")
	}

	reader, err := y4m.NewReader(in)
	if err != nil {
		if strings.Contains(err.Error(), "empty input") {
			err = fmt.Errorf("%w (did the producer fail? ffmpeg needs -pix_fmt yuv420p for RGB sources)", err)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	header := reader.Header()

	termCols, termRows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termCols, termRows = 0, 0
	}
	width, height := fitSize(o.render, header.Width, header.Height, o.width, o.height, termCols, termRows)
	cfg := encode.Config{Mode: o.render, Color: o.color, Quality: o.quality, Width: width, Height: height}
	if err := cfg.Validate(); err != nil {
		return err
	}
	slog.Info("Playing", "input", name, "size", fmt.Sprintf("%dx%d", header.Width, header.Height),
		"fps", header.FPS(), "colorspace", header.Colorspace, "full_range", header.FullRange,
		"renderer", cfg.Mode, "width", cfg.Width, "height", cfg.Height)

	src := &source{reader: reader, name: name, pace: o.pace, hold: o.hold}
	return player.Play(cfg, player.Options{
		Title:       "termplay " + version.Get().Display(),
		Interactive: o.interactive,
		Adaptive:    o.adaptive,
		TargetFPS:   o.targetFPS,
	}, statusLog, src.run)
}

// openInput returns stdin, or the file named by the only argument ("-" is stdin).
func openInput(args []string) (io.ReadCloser, string, error) {
	switch {
	case len(args) == 0 || len(args) == 1 && args[0] == "-":
		return os.Stdin, "stdin", nil
	case len(args) == 1:
		f, err := os.Open(args[0])
		return f, args[0], err
	default:
		return nil, "", fmt.Errorf("one input at most, got %d", len(args))
	}
}

// lastFrameGrace is how long the last frame stays on screen at the end of
// input, at least (one frame interval when pacing is longer).
const lastFrameGrace = 200 * time.Millisecond

// source reads frames and hands them to the pipeline, scaled to its size.
type source struct {
	reader *y4m.Reader
	name   string
	pace   bool
	hold   bool
}

func (s *source) run(ctx context.Context, p *pipeline.Pipeline) error {
	p.Status("Playing " + filepath.Base(s.name))
	var interval time.Duration
	if fps := s.reader.Header().FPS(); s.pace && fps > 0 {
		interval = time.Duration(float64(time.Second) / fps)
	}

	start := time.Now()
	var frame *image.RGBA
	for i := 0; ctx.Err() == nil; i++ {
		var err error
		frame, err = s.reader.ReadFrame(frame)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		if interval > 0 {
			if wait := time.Until(start.Add(time.Duration(i) * interval)); wait > 0 {
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		w, h := p.TargetSize()
		p.Frame(scaled(frame, w, h))
	}

	p.Status("End of input")
	if s.hold {
		<-ctx.Done()
		return nil
	}
	// Give the last frame time to reach the screen before the player stops.
	select {
	case <-time.After(max(interval, lastFrameGrace)):
	case <-ctx.Done():
	}
	return nil
}

// scaled returns frame at w×h as an image the pipeline may keep: the reader
// reuses frame for the next one, so a frame of the right size is copied.
func scaled(frame *image.RGBA, w, h int) *image.RGBA {
	out := resize.Area(frame, w, h)
	if out == frame {
		out = &image.RGBA{Pix: append([]byte(nil), frame.Pix...), Stride: frame.Stride, Rect: frame.Rect}
	}
	return out
}
