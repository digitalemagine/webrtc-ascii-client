# Architecture

How the client and its two companion commands are put together: structure and data flow, with links to the
code rather than copies of it.

## One process, or a pipe

The client started as a single tool for one job: checking remote cameras from an SSH session, where there is
no browser. So it does everything in one process: signaling, decoding and drawing. `webrtc-recv` and
`termplay` were split out of it later, and `webrtc-recv | ffmpeg | termplay` does the same job as a pipe
([example](USAGE.md#webrtc-recv)). The single process stays the main tool because a pipe loses three things:

- **Recovery from decode errors.** The client asks the sender for a keyframe (RTCP PLI) after packet loss
  and after a decode error, so a broken picture recovers within a second. `webrtc-recv` still asks after
  packet loss, but a decode error in ffmpeg can't reach the sender: the picture stays broken until the
  sender's next scheduled keyframe.
- **Resizing while playing.** The client decodes straight to the renderer's pixel size. In a pipe, ffmpeg
  scales to the size chosen when it started; after a resize or a renderer switch, `termplay` rescales frames
  of the old size.
- **Latency.** Each process in a pipe may buffer unless told not to (`-fflags nobuffer`); in one process the
  stages hand frames over without queues.

## Packages

| Package | Role |
|---|---|
| [`cmd/webrtc-ascii-client`](../cmd/webrtc-ascii-client/main.go) | Flags, renderer configuration, wiring, shutdown |
| [`internal/client`](../internal/client/client.go) | `WebRTCClient`: peer connection, track handling, keyframe requests, connection monitoring (pure Go) |
| [`internal/adapters`](../internal/adapters/session.go) | Signaling: `Session` interface; Simple, Janus (WebSocket) and WHEP (HTTP) |
| [`internal/adapters/janustest`](../internal/adapters/janustest/server.go) | Mock Janus server with real pion offers, for tests |
| [`internal/h264rtp`](../internal/h264rtp/assembler.go) | RTP → H.264 access units in Annex-B (pion samplebuilder); `Writer`: a stream starting at a keyframe. Pure Go |
| [`internal/decoder`](../internal/decoder/h264_stream.go) | Access units → FFmpeg decode and scale (cgo) → RGBA; `H264Stream` is the client's `Track` |
| [`internal/decoder/decodertest`](../internal/decoder/decodertest/decodertest.go) | Splits test clips into access units and RTP packets |
| [`encode`](../encode/encode.go) (public) | `Config` and the seven `Encoder`s: image in, terminal bytes out |
| [`internal/pipeline`](../internal/pipeline/pipeline.go) | Encode stage, reconfiguration, adaptive quality |
| [`internal/display`](../internal/display/display.go) | The only stdout writer: alternate screen, frames, status line |
| [`internal/latest`](../internal/latest/slot.go) | `Slot[T]`: hand-off between goroutines where the newest value wins |
| [`internal/controls`](../internal/controls/manager.go) | Interactive keys and terminal resizes → new `encode.Config` |
| [`internal/demo`](../internal/demo/demo.go) | Moving test pattern for `-demo` |
| [`internal/logging`](../internal/logging/logging.go) | `log/slog` setup: warnings to the status line, `-V` levels to stderr |
| [`internal/version`](../internal/version/version.go) | Version (`-ldflags -X`) and VCS info (`debug.ReadBuildInfo`) |
| [`cmd/termplay`](../cmd/termplay/main.go) | Pure-Go player for YUV4MPEG2 on stdin, same renderers and display |
| [`internal/player`](../internal/player/player.go) | Runs a frame source through pipeline, display and controls (both commands) |
| [`internal/y4m`](../internal/y4m/y4m.go) | YUV4MPEG2 reader, BT.601 → RGBA |
| [`internal/resize`](../internal/resize/resize.go) | Area-averaging RGBA scaler (termplay) |
| [`cmd/webrtc-recv`](../cmd/webrtc-recv/main.go) | Pure-Go receiver: the client's signaling, first video track to stdout as H.264 Annex-B |
| [`cmd/bandwidth-analyzer`](../cmd/bandwidth-analyzer/main.go) | Bytes per frame for each renderer |

## Frame path

```
 source                         latest-wins             latest-wins
 ───────────────────────────    ───────────    ──────   ───────────    ───────
 pion track: ReadRTP                │          encode       │          display ──▶ stdout
   → samplebuilder + H264Packet  Frame()       Encoder   Show()        frame + status line
   → FFmpeg decode               ─────────▶    .Encode  ─────────▶     (alternate screen,
   → sws_scale to TargetSize()                                          synchronized update)
 or the demo pattern generator
          ◀── RTCP PLI (track start, packet loss, decode error; ≤ 1/s)
```

1. **Source.** pion calls `OnTrack` on its own goroutine. `handleVideoTrack` checks the codec (H.264
   only), asks the client's `Output` for a `Track`, asks the sender for a keyframe (RTCP PLI) and loops on
   `ReadRTP`, pushing each packet to the `Track` and requesting a keyframe when it asks for one. The client
   doesn't decode: in `webrtc-ascii-client` the `Track` is a `decoder.H264Stream`
   ([`h264_stream.go`](../internal/decoder/h264_stream.go)). Its
   [`h264rtp.Assembler`](../internal/h264rtp/assembler.go) feeds pion's `samplebuilder`
   with `codecs.H264Packet`: packets are reordered, access units that lost packets are dropped, and each
   frame (all its slices, with any SPS/PPS) comes out as one Annex-B buffer when the next one starts, so
   output lags by at most one frame interval. [`h264.go`](../internal/decoder/h264.go) decodes it with
   `libavcodec` in low-delay mode and converts it with `libswscale` straight into an `image.RGBA` of the
   size the pipeline asks for (`SWS_AREA`; rows are padded, since swscale's SIMD code writes past their
   end). Frames larger than H.264 allows (Level 6.2) are refused as decode errors. With `-demo`, the source is [`demo.Run`](../internal/demo/demo.go),
   which draws its pattern at that size. In `termplay` the source reads YUV4MPEG2
   ([`y4m`](../internal/y4m/y4m.go)), paces it at the stream's frame rate and scales each frame with
   [`resize.Area`](../internal/resize/resize.go); the rest of the path is the same
   ([`player.Play`](../internal/player/player.go)). `webrtc-recv` uses the same client with a `Track` that
   passes the assembler's access units to [`h264rtp.Writer`](../internal/h264rtp/writer.go) instead of a
   decoder; the stages below don't exist there.
2. **Encode.** The pipeline's encode goroutine takes the newest frame and calls the current
   [`Encoder`](../encode/encode.go). Encoders are pure: one per renderer, no I/O, no state other
   than scratch buffers; a frame of the wrong size (decoded before a reconfiguration) is skipped.
   `Config.PixelSize` maps each renderer to its pixel grid (classic 1×1 per cell, enhanced q×q, blocks 1×2,
   braille 2×4, sextant 2×3, image renderers in pixels).
3. **Display.** The display goroutine takes the newest encoded frame and writes, in one synchronized update:
   cursor home, the frame, the status line, and "clear to end of screen". It clears the whole screen only
   when the renderer changes or when the frame and status line wouldn't fit (drawing over would scroll).

The stages hand over through [`latest.Slot`](../internal/latest/slot.go), so a slow stage skips to the
newest frame instead of building up delay; the drops are counted in the status line. Decoding itself never
drops: H.264 frames depend on each other.

## Goroutines and ownership

| Goroutine | Started by | Owns / does | Ends when |
|---|---|---|---|
| main | — | waits for the source and the pipeline | source ends, failure, signal, quit key |
| source | `main` | `WebRTCClient.Connect` (or the demo) | connection fails or ctx is canceled |
| session `Run` | `Connect` | the signaling connection; Janus/Simple read loop; keepalive | ctx canceled or signaling fails |
| connection monitor | `Connect` | polls the peer connection state every 2 s | failure or ctx canceled |
| pion `OnTrack` | pion | one per track: **owns its `Track` (`H264Stream`: assembler and decoder)** | `ReadRTP` fails or shutdown |
| encode | `Pipeline.Run` | the current encoder | ctx canceled |
| display | `Pipeline.Run` | stdout and the terminal state | ctx canceled (restores the terminal) |
| keyboard `readLoop` | `KeyboardHandler.Start` | `/dev/tty` in raw input mode | `Stop` |
| `processCommands` | `ControlManager.Start` | the output configuration (keys and terminal resizes) | `Stop` |

Shutdown: Ctrl-C, SIGTERM and the quit key cancel one root context (`signal.NotifyContext`). `Connect`
waits for its session to clean up (Janus `destroy`, WHEP `DELETE`), then refuses new track handlers,
closes the peer connection (which ends `ReadRTP`) and waits for them; each closes its `Track`. `main` waits
at most 3 s for that, then for the pipeline, whose display restores the terminal.

Shared state: the client's status string is behind a mutex and forwarded to the display; the pipeline's
target size is an atomic; reconfigurations go through a `latest.Slot`; the display's status fields are
behind its mutex. Janus keeps its transaction map under `transMu` and session/handle IDs in atomics;
`baseAdapter` serializes WebSocket writes with `wsMu`.

## Signaling

Each protocol implements [`Session`](../internal/adapters/session.go):
`Run(ctx, pc)` negotiates the peer connection, keeps signaling alive until ctx ends or signaling fails, and
cleans up before returning. `Run` blocks for the whole session instead of returning after negotiation,
so each protocol's cleanup is in one place and runs on every exit path. Remote candidates that arrive before the remote description are held back.

**Simple** ([`simple.go`](../internal/adapters/simple.go), protocol in [SIGNALING.md](SIGNALING.md)): WebSocket, `join`, the
server's `offer`, the client's `answer`, `ice` both ways as `RTCIceCandidateInit`, keepalive every 30 s.

**Janus** ([`janus*.go`](../internal/adapters/janus.go)), streaming plugin, subprotocol `janus-protocol`:

```
create            → success {data.id = session}       (keepalive starts, every 30 s)
attach streaming  → success {data.id = handle}
message list      → success {plugindata.list}         (logged)
message watch {id}→ ack, then event {plugindata, jsep offer}
                     plugindata.error → Run fails with message and code
message start + jsep answer
trickle candidates both ways, ending with {"completed": true}
hangup            → Run ends
destroy           (on exit)
```

Requests that wait for a reply register a channel for their transaction ID; an `ack` doesn't complete a
transaction, because asynchronous plugin requests (watch) reply later with the same ID.

**WHEP** ([`whep.go`](../internal/adapters/whep.go)): add a recvonly video transceiver, create the offer,
wait up to 2 s for ICE gathering so that every candidate is in the offer (simpler than trickling them with
`PATCH`, and MediaMTX accepts it; media starts slightly later), `POST` it as `application/sdp`, apply the answer from the `201` response,
`DELETE` the `Location` URL on exit.

## Interactive mode

[`KeyboardHandler`](../internal/controls/keyboard_unix.go) opens `/dev/tty` and switches its input to raw
mode with termios (output processing stays on), reading with a 100 ms timeout so it can stop and tell a lone
Esc from escape sequences. [`ControlManager`](../internal/controls/manager.go) keeps the output size in
cells and turns keys into a new `encode.Config` (pixels for the image renderers, about 10×20 per cell),
which `main` passes to `Pipeline.Reconfigure`; the pipeline changes the decode size and the encoder.
It also handles SIGWINCH, with or without `-i` (then it starts without the keyboard, and only when stdout is
a terminal): it keeps the size asked for (command line or preset) and limits it to the terminal above the
status line, or follows the terminal in the full-terminal preset. Outside Unix there is no SIGWINCH, and
interactive mode reports that it isn't supported.

## Logging

All logging uses `log/slog` ([`internal/logging`](../internal/logging/logging.go)): warnings and errors go
to the display's status line; with `-V 1`/`-V 2`, Info/Debug and above also go to stderr. Nothing but the
display writes to stdout.

## Audio

There is none: WHEP offers only a video transceiver, and `OnTrack` ignores other tracks. What it would take:

- **Receiving:** an audio transceiver and an audio branch in `OnTrack`. WebRTC audio is nearly always Opus,
  which pion's default codecs include.
- **Buffering and decoding:** audio can't drop frames the way video does through `latest.Slot`. It needs
  reordering (pion's `samplebuilder`) and loss concealment from the decoder. `libavcodec`, already linked,
  decodes Opus.
- **Output:** a sound library ([oto](https://github.com/ebitengine/oto) or
  [malgo](https://github.com/gen2brain/malgo)) adds cgo outside `internal/decoder` on Linux, or on every
  platform with malgo. stdout stays video only, so writing audio means a separate file or pipe.
- **Timing:** audio has to play continuously from a small buffer and becomes the clock. Lip sync means
  mapping RTP timestamps through RTCP Sender Reports and holding video back, which works against showing the
  newest frame.

In `webrtc-recv`, writing Opus to an Ogg file needs no decoding (pion's `oggwriter`), but the two files would
only line up roughly; for recordings with sound, writing both tracks into one container would be the useful
version.

## Build

- cgo is required for `webrtc-ascii-client`: [`h264.go`](../internal/decoder/h264.go) links `libavcodec`,
  `libavutil` and `libswscale` through `pkg-config`. Only `internal/decoder` uses cgo: `internal/client`
  and `internal/h264rtp` are pure Go, and so are `termplay` and `webrtc-recv` (`CGO_ENABLED=0`).
- No feature build tags. `//go:build unix` selects the terminal code in `internal/controls`, and
  `termios_bsd.go`/`termios_linux.go` the termios ioctls.
- `make build` passes the version with `-ldflags -X`; commit and time come from Go's build information.

## Tests

| Area | Tests |
|---|---|
| Adapters | Janus, Simple and WHEP sessions against test servers with real pion peers (negotiation, errors, trickle, cleanup); ping timeout; keepalive timing (synctest) |
| Client | end to end over localhost (mock Janus → ICE/DTLS/SRTP → access units), shutdown while receiving, keyframe requests, cancellation |
| h264rtp | clips as RTP to access units (one and three slices per frame, reordered, lost packet), `FuzzAssembler`; `Writer` start at a keyframe |
| webrtc-recv | end to end over localhost (mock Janus → Annex-B, decoded by `ffprobe` when installed), reader gone (EPIPE) cleans up the session |
| Decoder | clips as RTP to decoded frames (same cases), scaling, `FuzzH264Stream` (depacketizing and FFmpeg) |
| Encode | golden output per renderer and mode ([`testdata/`](../encode/testdata)), deterministic across platforms |
| Pipeline, display, latest, controls, demo, logging | stages, reconfiguration, adaptive quality, status line, keys |

Run them with `go test -race ./...`; fuzz with `go test -fuzz FuzzH264Stream ./internal/decoder`;
update golden files with `go test ./encode -update`.
