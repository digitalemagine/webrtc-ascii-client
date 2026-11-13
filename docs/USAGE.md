# Usage

This page covers `webrtc-ascii-client`, then the two smaller commands, [`webrtc-recv`](#webrtc-recv) and
[`termplay`](#termplay).

```
webrtc-ascii-client [options] <server_url> [<session_id>]
webrtc-ascii-client [options] -demo
webrtc-ascii-client -list-adapters
```

Options must come before the server URL. Simple and Janus need a session ID (the room or the mountpoint);
WHEP identifies the stream by its URL. How each protocol is used: [SIGNALING.md](SIGNALING.md).

With `-adapter auto` (the default) the protocol is chosen from the URL: `http://` or `https://` is WHEP; a
path containing `janus` or port 8188 is Janus; anything else is Simple.

## Examples

```bash
# Demo: a moving test pattern at 5 FPS through the same encoding and display as real video
webrtc-ascii-client -demo -i

# Size in characters
webrtc-ascii-client -w 120 -H 40 ws://localhost:8080/ws myroom

# Width 160, height from a 16:9 ratio (corrected for tall characters)
webrtc-ascii-client -w 160 -ratio 16:9 ws://localhost:8080/ws myroom

# Enhanced ASCII in color, high quality
webrtc-ascii-client -render enhanced -color -quality 3 ws://localhost:8080/ws myroom

# Half blocks, sextants or braille in color
webrtc-ascii-client -render blocks -color ws://localhost:8080/ws myroom
webrtc-ascii-client -render sextant -color ws://localhost:8080/ws myroom
webrtc-ascii-client -render braille -color ws://localhost:8080/ws myroom

# Real images; sizes are in pixels
webrtc-ascii-client -render sixel -w 640 -H 480 ws://localhost:8080/ws myroom
webrtc-ascii-client -render iterm2 -ratio 16:9 ws://localhost:8080/ws myroom

# Debug logging to a file
webrtc-ascii-client -verbosity 2 ws://localhost:8080/ws myroom 2>debug.log
```

## Options

| Option | Default | Description |
|---|---|---|
| `-adapter`, `-a` | `auto` | Signaling adapter: `auto`, `simple`, `janus`, `whep` |
| `-render`, `-r` | `classic` | Renderer: `classic`, `enhanced`, `blocks`, `sextant`, `braille`, `sixel`, `iterm2` |
| `-width`, `-w` | 80 chars / 1280 px | Output width: characters for text renderers, pixels for `sixel` and `iterm2`. A size you don't give is reduced to fit a smaller terminal, leaving two rows for the status line |
| `-height`, `-H` | 24 chars / 720 px | Output height, same units as `-w` |
| `-ratio` | | Aspect ratio such as `16:9`; computes the missing dimension (for text renderers it assumes characters twice as tall as wide) |
| `-color`, `-c` | off | 24-bit color for `enhanced`, `blocks`, `sextant`, `braille` |
| `-quality`, `-q` | 2 | Supersampling for `enhanced`: 1 fast, 2 normal, 3 high, 4 ultra |
| `-interactive`, `-i` | off | Keyboard controls (below) |
| `-adaptive` | off | Lower `-quality` when encoding can't keep up with `-target-fps`, and raise it again (up to the starting value) when it can. Only `enhanced` has a quality to adapt |
| `-target-fps` | 30 | Frame rate that `-adaptive` aims for |
| `-stun` | `stun:stun.l.google.com:19302` | STUN server URL; repeat for several. `-stun ""` disables STUN (only local candidates, fine on a LAN) |
| `-verbosity`, `-V` | 0 | Log to stderr: 0 nothing (warnings and errors still appear in the status line), 1 progress (`INFO`), 2 everything (`DEBUG`). stdout only ever carries the video, so `2>file` keeps the picture clean |
| `-demo` | | Run the demo |
| `-list-adapters` | | List the signaling adapters |
| `-version`, `-v` | | Print the version |
| `-version-detailed` | | Print version, commit, commit time, Go version and platform |
| `-help`, `-h` | | Show the usage and all options |

## Interactive controls

With `-i`:

| Key | Action |
|---|---|
| `q`, `Esc`, Ctrl-C | Quit |
| `h`, `?` | Show or hide the keys in the status line |
| `c` | Toggle color |
| `+` / `-` | Raise / lower quality (`enhanced`) |
| `f` | Cycle sizes: 80×24 → 160×48 → 240×72 → full terminal, each limited to what fits above the status line |
| `1` … `7` | Renderer: classic, enhanced, blocks, braille, sextant, sixel, iterm2 |

When the terminal is resized, the picture keeps the size you asked for (with `-w`/`-H` or `f`) if it fits,
and otherwise shrinks to what fits above the status line; it grows back when the terminal does. In the
"full terminal" size, it follows the terminal. This works without `-i` too.

## Display and status line

The client draws on the terminal's alternate screen, so your scrollback is untouched, and restores the
terminal when it exits. Ctrl-Z suspends it and gives the terminal back; `fg` continues. Below the video, a two-line status line shows the version, renderer, FPS, frames
shown, frames dropped and warnings, then the connection state and the latest warning (or the keys, after
`h`).

Each frame is drawn over the previous one without clearing the screen, so there is no flicker, unless the
frame plus the status line is taller than the terminal. That only happens with an explicit `-w`/`-H` larger
than the terminal, until the terminal is first resized. When the terminal can't keep up with the stream,
frames are dropped rather than queued, so the picture never falls behind; the status line counts the drops.

## Renderers

### Text renderers (size in characters)

- **`classic`**: one character per cell from the ramp `" .:-=+*#%@"` (dark to bright). Monochrome, fast,
  smallest output.
- **`enhanced`**: supersampling (`-quality`), perceptual tone mapping and Floyd–Steinberg dithering;
  optional color.
- **`blocks`**: Unicode half blocks (`▀`), two pixels per cell, so twice the vertical resolution. In color,
  top and bottom pixels get their own color; without `-color` they are gray levels, which is no smaller
  ([bandwidth](BANDWIDTH_ANALYSIS.md)).
- **`sextant`**: Unicode sextants (U+1FB00–U+1FB3B), 2×3 pixels per cell, with a foreground and background
  color chosen to minimize the error.
- **`braille`**: Unicode braille patterns (`⠀`–`⣿`), 2×4 dots per cell. Without color a dot is on where the
  picture is light; in color each cell gets two colors, its lighter pixels as dots over the darker ones.

### Image renderers (size in pixels)

- **`sixel`**: the Sixel protocol, 256-color palette with dithering.
- **`iterm2`**: the iTerm2 inline image protocol with PNG frames, full 24-bit color.

## Terminal support

- **Color** (`-color`) uses 24-bit ANSI escape sequences. Most current terminals support them.
- **Sextants** need a font with the Unicode 13 "Symbols for Legacy Computing" block, such as
  [Cascadia Code](https://github.com/microsoft/cascadia-code/releases) or GNU Unifont. If you see � or
  boxes, your font lacks them (in iTerm2: Settings → Profiles → Text → Font).
- **Sixel** works in, among others, xterm (started with `-ti vt340`), mlterm, WezTerm, foot, iTerm2 and
  Windows Terminal; not in macOS Terminal.
- **iTerm2 images** work in iTerm2 and WezTerm.
- **Interactive mode** reads keys from `/dev/tty`, so it needs a Unix terminal.

## Versions

`-version` prints the version: the release tag for a released build (`v0.1.0`), `git describe` output for a
`make` build from a checkout, or a Go pseudo-version for a plain `go build`. `-version-detailed` adds the
commit, its time, whether the tree was modified, the Go version and the platform.

## webrtc-recv

```
webrtc-recv [options] <server_url> [<session_id>] > out.h264
```

`webrtc-recv` connects like `webrtc-ascii-client` (same signaling, same arguments, same `-adapter` and
`-stun`) but doesn't decode: it writes the video track to stdout, or to a file with `-o`, as an H.264
Annex-B stream for ffmpeg, ffplay, GStreamer or a recording.

```bash
webrtc-recv http://localhost:8889/mystream/whep > stream.h264                 # record (WHEP, e.g. MediaMTX)
webrtc-recv -adapter janus ws://localhost:8188 1 | ffplay -f h264 -fflags nobuffer -
webrtc-recv ws://localhost:8080/ws myroom | ffmpeg -f h264 -i - -c copy out.mp4

# the whole client as a pipeline (what it loses: ARCHITECTURE.md, "One process, or a pipe")
webrtc-recv http://localhost:8889/mystream/whep \
  | ffmpeg -v error -f h264 -fflags nobuffer -i - -vf scale=$(termplay -print-size -r sextant -w 80 -H 24) -pix_fmt yuv420p -f yuv4mpegpipe - \
  | termplay -r sextant -c -w 80 -H 24
```

- The output starts at the first keyframe (with its SPS/PPS), so files and pipes decode from the first byte.
  It asks the sender for a keyframe at the start, after packet loss, and until one arrives.
- Raw H.264 carries no timestamps: ffmpeg takes the frame rate from the stream when the sender's encoder
  wrote one (x264 does), and assumes 25 FPS otherwise; pass `-framerate N` before `-i` if needed.
- Only the first video track is written; audio isn't received.
- It ends on Ctrl-C, when the track ends, or when the reader goes away (`| head -c 1M`); in each case
  the signaling session is closed (Janus `destroy`, WHEP `DELETE`) and the exit status is 0. Stdout must not
  be a terminal. Logs go to stderr (`-V 1`, `-V 2` for more).
- Unlike `webrtc-ascii-client`, nothing downstream can ask for a keyframe after a decode error.

## termplay

```
<producer> | termplay [options]
termplay [options] file.y4m
termplay -print-size -r <renderer> -w <width> -H <height>
```

`termplay` plays YUV4MPEG2 video with the same renderers, display and keys as `webrtc-ascii-client`, without
WebRTC. The `ffmpeg` command turns any file, camera or stream into its input:

```bash
ffmpeg -v error -nostdin -re -i talk.mp4 -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r sextant -c
ffmpeg -v error -nostdin -f avfoundation -framerate 30 -i 0 -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r blocks -c -i   # macOS webcam
ffmpeg -v error -nostdin -i photo.jpg -pix_fmt yuv420p -f yuv4mpegpipe - | termplay -r iterm2 -hold
termplay -r braille clip.y4m
```

- `-v error -nostdin` keep ffmpeg off the terminal: without them its progress lines draw over the picture
  and it reads the keys meant for `termplay -i`.
- Input is YUV4MPEG2 in 4:2:0, 4:2:2, 4:4:4 or gray, limited range unless the header says full range (as
  ffmpeg marks it); details in the [`y4m` package](../internal/y4m/y4m.go).
- `-pix_fmt yuv420p` is needed for RGB sources (images, many cameras): the Y4M format can't carry them.
- `termplay` scales frames itself to the largest size that fits the terminal and keeps the picture's aspect
  ratio; with only `-w` or only `-H`, the other side follows from the aspect ratio. To keep the pipe small,
  let ffmpeg scale: `-vf scale=$(termplay -print-size -r braille -w 80 -H 24)`.
- Files play at their frame rate even without `ffmpeg -re` (`-pace=false` to disable).
- It exits when the input ends; `-hold` keeps the last frame until Ctrl-C (or `q` with `-i`).
- Renderer options (`-r`, `-c`, `-q`, `-w`, `-H`, `-adaptive`, `-target-fps`, `-i`, `-V`) are those of
  `webrtc-ascii-client`; `termplay -h` lists them all.
