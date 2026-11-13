# Dependency Analysis

What the project depends on, what each dependency is used for, and what it costs.

Shares are measured with [go-size-analyzer](https://github.com/Zxilly/go-size-analyzer) (`gsa` v1.14.1) on an
unstripped `make build` of `webrtc-ascii-client` (go1.27.1, darwin/arm64, about 17 MB). To reproduce:
`go install github.com/Zxilly/go-size-analyzer/cmd/gsa@latest`, then `gsa -f text bin/webrtc-ascii-client`.

## Binary size

Stripped builds (`make release`, go1.27.1, darwin/arm64), rounded:

| Command | Size | Needs at run time |
|---|---|---|
| `webrtc-ascii-client` | 11 MB | the FFmpeg libraries (`libavcodec`, `libavutil`, `libswscale`), linked dynamically |
| `webrtc-recv` | 11 MB | nothing |
| `termplay` | 3.3 MB | nothing |

The two WebRTC commands are almost the same size because most of it is the WebRTC stack (pion, below) and
`net/http`; decoding is in the FFmpeg libraries, outside the binary. `make build` keeps debug information and
is about 50% larger.

## Go dependencies

| Dependency | What we use | Binary share | Maintenance | Why |
|---|---|---|---|---|
| `github.com/pion/webrtc/v4` (+ ~14 pion modules) | PeerConnection, tracks, ICE, DTLS/SRTP: the core of the client | pion 2.89 MB in total (webrtc 559 kB, dtls 457 kB, ice 441 kB, sctp 418 kB, …), plus std `crypto` 1.75 MB, mostly for DTLS/SRTP | Active | No alternative in Go. `datachannel`/`sctp` are linked although unused (pion links them unconditionally). |
| `github.com/gorilla/websocket` | `Dialer`, JSON read/write, ping/close for Simple and Janus signaling | 81 kB | Active | Not worth writing a WebSocket client. WHEP uses plain `net/http`. |
| `github.com/mattn/go-sixel` + `github.com/soniakeys/quant` | `sixel.NewEncoder` (256 colors, dithering) in the Sixel renderer | 17 kB + 17 kB | go-sixel: active (v0.0.14); quant: last release 2018 | Small and MIT. If they break, a ~200-line encoder on stdlib `image/color/palette` + `draw.FloydSteinberg` can replace them (fixed palette, slightly lower quality). |
| `golang.org/x/term` | `GetSize` for terminal dimensions | < 1 kB | Go team | |
| `golang.org/x/sys` | `unix` termios and `/dev/tty` reads for interactive keys ([`keyboard_unix.go`](../internal/controls/keyboard_unix.go)) | 17 kB (shared with pion) | Go team | Already required by pion. |

`term.MakeRaw` is not used for interactive mode: it clears output processing (`OPOST`), and the renderers
rely on `\n` moving to the start of the next line. The keyboard handler sets the input flags itself and
leaves output alone.

**Standard library:** `net/http` (about 480 kB of the 960 kB `net` share) comes with the WHEP client;
`log/slog` adds about 90 kB; `image/png` is needed by the iTerm2 renderer.

**Where the size goes:** the project's own code is 219 kB. DWARF debug info is 4.9 MB (28%), which is why
`make release` strips it (`-ldflags="-s -w"`). No dependency comes close to that.

## System dependency: FFmpeg

H.264 decoding (`libavcodec`) and YUV→RGBA conversion (`libswscale`, with `libavutil`) go through cgo.
FFmpeg is linked dynamically, so it adds nothing to the binary, but it has to be installed:

```bash
# macOS (Homebrew)
brew install ffmpeg pkg-config

# Debian / Ubuntu
sudo apt install libavcodec-dev libavutil-dev libswscale-dev pkg-config
```

`make check-deps` checks that `pkg-config` finds the three libraries.

On macOS, Homebrew's `ffmpeg` formula brings 14 packages (146 MB: dav1d, x264, x265, svt-av1, libvpx, sdl3, …)
for one decoder and one color converter. On Debian the three `-dev` packages are enough.

### FFmpeg vs OpenH264

| | FFmpeg (current) | OpenH264 (Cisco, BSD-2) |
|---|---|---|
| macOS install | `brew install ffmpeg`: 14 packages, 146 MB | `brew install openh264`: no dependencies, one small library |
| Linux install | 3 `-dev` packages | 1 package (or Cisco's prebuilt binary) |
| Still cgo | yes | yes |
| Color conversion / scaling | `libswscale` (fast; scales each frame to the renderer's size in the same pass) | none: decode to YUV420, convert with stdlib `image.YCbCr` → RGBA, scale in Go |
| Profile support | all | designed around Constrained Baseline, which WebRTC usually negotiates (`42e01f`). Main/High support would need testing: Janus and MediaMTX can relay High-profile camera streams unchanged. |
| Code to write | — | ~150-line cgo wrapper replacing `internal/decoder/h264.go` |

**Recommendation:** keep FFmpeg. It handles every profile and allows scaling in `libswscale`; the heavy
install is a Homebrew packaging cost, not a code cost. Revisit OpenH264 only if install friction becomes a
real problem, and test High-profile streams first. Offering it as an optional backend would need a build tag
and a second build variant to test.

### Without native libraries

There is no mature pure-Go H.264 decoder. The options I know of, none tried:

- **Run the `ffmpeg` command** as a subprocess (Annex-B in, raw YUV out): no cgo, but `ffmpeg` is needed at
  run time, and a resize means restarting it or scaling in Go.
- **A C decoder compiled to WebAssembly**, run with [wazero](https://github.com/tetratelabs/wazero): one
  static binary without cgo; slower than native, to be measured against `BenchmarkDecodeClip`.
- **OpenH264 loaded at run time** with [purego](https://github.com/ebitengine/purego): no cgo, but the
  library is still needed.
- **A decoder written in Go**, limited to Constrained Baseline: several thousand lines, conformance testing
  against FFmpeg, and streams in other profiles stop working.

Replacing `libswscale` is the easy part: YUV420 to RGBA and downscaling to terminal sizes is cheap in Go.

### Licenses and patents

Not legal advice; check the current terms before relying on any of this.

- **FFmpeg's license.** This project is MIT and distributes source only; users link their own FFmpeg.
  Distributing a binary linked against FFmpeg would bring in FFmpeg's license: LGPL, or GPL for builds with
  `--enable-gpl` (Homebrew's is one).
- **H.264 patents.** H.264 is covered by patents licensed as a pool by Via LA (formerly MPEG LA). They concern
  products that encode or decode, whatever the language or library; the client only decodes, and
  `webrtc-recv` doesn't decode at all. Many of the patents have expired, the rest run out over the next
  years depending on the country.
- **Removing FFmpeg doesn't remove the patents.** A decoder compiled into the binary (WebAssembly or Go)
  makes whoever distributes that binary the distributor of an H.264 decoder. Cisco pays the royalties for
  OpenH264, but only for its prebuilt library downloaded from Cisco, not for one built from source or
  bundled.
- **Opus** (WebRTC's audio codec) is royalty-free.

## Other choices

- **One build.** Sixel is always compiled in: a build tag to leave it out would save 70 kB (0.4%) and add a
  second variant to test. There are no other build tags, and no Go `plugin` system: plugins need identical
  toolchain and dependency versions and don't work on every platform.
- **Only the FFmpeg libraries that are used:** `libavcodec`, `libavutil` and `libswscale`; not `libavformat`.
  Frames are scaled to the renderer's size by `libswscale` during the YUV→RGBA conversion, so there is no
  image-scaling dependency in Go.
- **`net/http` for WHEP** (about 480 kB): WHEP is HTTP.
- **Stripped release binaries** (`make release`, `make install`): debug information and symbols are about a
  third of an unstripped binary.
